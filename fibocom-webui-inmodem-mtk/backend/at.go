package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"sync"
	"time"
)

// AtResp 是一次 AT 交互的结果。
type AtResp struct {
	Lines []string
	OK    bool
	Raw   string
}

func (r AtResp) First() string {
	for _, l := range r.Lines {
		t := strings.TrimSpace(l)
		if t != "" {
			return t
		}
	}
	return ""
}

// After 返回形如 "+CPIN: READY" 冒号后的内容。
func (r AtResp) After(prefix string) string {
	for _, l := range r.Lines {
		t := strings.TrimSpace(l)
		if strings.HasPrefix(t, prefix) {
			v := strings.TrimSpace(t[len(prefix):])
			v = strings.TrimSpace(strings.TrimPrefix(v, ":"))
			return v
		}
	}
	return ""
}

// atEngine 持有一个常驻读协程，避免阻塞读取导致超时后无法恢复。
type atEngine struct {
	name      string
	rw        io.ReadWriteCloser
	lines     chan string
	done      chan struct{}
	closeOnce sync.Once
}

func newAtEngine(name string, rw io.ReadWriteCloser) *atEngine {
	e := &atEngine{
		name:  name,
		rw:    rw,
		lines: make(chan string, 512),
		done:  make(chan struct{}),
	}
	go e.readLoop()
	return e
}

func (e *atEngine) close() {
	e.closeOnce.Do(func() {
		_ = e.rw.Close()
	})
}

func (e *atEngine) readLoop() {
	defer close(e.done)
	buf := make([]byte, 4096)
	pending := ""
	for {
		n, err := e.rw.Read(buf)
		if n > 0 {
			pending += string(buf[:n])
			for {
				i := strings.IndexByte(pending, '\n')
				if i < 0 {
					break
				}
				line := strings.TrimSpace(strings.TrimRight(pending[:i], "\r"))
				pending = pending[i+1:]
				if line == "" {
					continue
				}
				// 通道写满时直接丢弃，避免读协程卡死。
				select {
				case e.lines <- line:
				default:
				}
			}
			if len(pending) > 65536 {
				pending = ""
			}
		}
		if err != nil {
			return
		}
	}
}

func (e *atEngine) drain(idle time.Duration) {
	timer := time.NewTimer(idle)
	defer timer.Stop()
	for {
		select {
		case <-e.lines:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(idle)
		case <-timer.C:
			return
		case <-e.done:
			return
		}
	}
}

func (e *atEngine) collect(cmd string, timeout time.Duration) (lines []string, ok bool, raw string, timedOut bool) {
	var all []string
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for {
		select {
		case line, open := <-e.lines:
			if !open {
				return lines, false, strings.Join(all, "\n"), false
			}
			all = append(all, line)
			t := strings.TrimSpace(line)
			if t == "" || (cmd != "" && strings.EqualFold(t, cmd)) {
				continue
			}
			if t == "OK" {
				return lines, true, strings.Join(all, "\n"), false
			}
			if t == "ERROR" || strings.HasPrefix(t, "+CME ERROR") || strings.HasPrefix(t, "+CMS ERROR") {
				return lines, false, strings.Join(all, "\n"), false
			}
			lines = append(lines, t)
		case <-e.done:
			return lines, false, strings.Join(all, "\n"), false
		case <-timer.C:
			return lines, false, strings.Join(all, "\n"), true
		}
	}
}

// 常见 Fibocom/MTK 模组的 AT 通道候选，按优先级探测。
// FM350（MTK T700 / OpenWrt）对外是 USB ACM，模组内 atcid 持有 /dev/ttyGS3
// 并开放 /dev/atci-service（Unix socket），优先使用该 socket 可避免与
// 主机侧 AT 抢占同一个 ACM 口。
var defaultATCandidates = []string{
	"unix:/dev/atci-service",
	"/dev/ttyGS3",
	"/dev/ttyGS2",
	"/dev/ttyGS0",
	"/dev/ttyGS1",
	"/dev/smd11",
	"/dev/smd9",
	"/dev/smd7",
	"/dev/smd8",
	"/dev/smd10",
	"/dev/smd6",
	"/dev/smd5",
	"/dev/at_mdm0",
	"/dev/at0",
	"/dev/ttyHS0",
	"/dev/ttyMSM0",
	"/dev/ttyACM0",
}

type atManager struct {
	mu      sync.Mutex // 串行化所有 AT 读写
	eng     *atEngine
	name    string // 当前使用的设备
	state   string // 最近一次探测结果说明
	nextTry time.Time
	cfg     *Config
}

func newATManager(cfg *Config) *atManager {
	return &atManager{cfg: cfg, state: "尚未探测"}
}

// startupDetect 后台探测，避免服务启动被串口探测阻塞。
func (m *atManager) startupDetect() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.prepareLocked()
}

func (m *atManager) Describe() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.eng != nil {
		return m.name
	}
	return "未就绪: " + m.state
}

func (m *atManager) Current() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.name
}

func (m *atManager) openDevice(spec string) (io.ReadWriteCloser, string, error) {
	if strings.HasPrefix(spec, "tcp:") {
		addr := strings.TrimPrefix(spec, "tcp:")
		conn, err := net.DialTimeout("tcp", addr, 3*time.Second)
		if err != nil {
			return nil, "", err
		}
		return conn, spec, nil
	}
	if strings.HasPrefix(spec, "unix:") {
		path := strings.TrimPrefix(spec, "unix:")
		conn, err := net.DialTimeout("unix", path, 3*time.Second)
		if err != nil {
			return nil, "", err
		}
		return conn, spec, nil
	}
	rw, err := openSerialDevice(spec)
	if err != nil {
		return nil, "", err
	}
	return rw, spec, nil
}

func (m *atManager) probe(spec string) bool {
	rw, _, err := m.openDevice(spec)
	if err != nil {
		return false
	}
	e := newAtEngine(spec, rw)
	ok := false
	// 部分模组上电后需要多次 AT 才能唤醒 AT 通道。
	for attempt := 0; attempt < 3; attempt++ {
		e.drain(100 * time.Millisecond)
		if _, err := e.rw.Write([]byte("AT\r\n")); err != nil {
			break
		}
		if _, got, _, _ := e.collect("AT", 1000*time.Millisecond); got {
			ok = true
			break
		}
	}
	e.close()
	return ok
}

func (m *atManager) prepareLocked() (*atEngine, error) {
	if m.eng != nil {
		select {
		case <-m.eng.done:
			m.eng.close()
			m.eng = nil
			m.state = m.name + " 读线程已退出"
		default:
			return m.eng, nil
		}
	}
	if time.Now().Before(m.nextTry) {
		return nil, errors.New("AT 通道未就绪：" + m.state)
	}

	var specs []string
	if m.cfg != nil && m.cfg.AT != "" {
		specs = append(specs, m.cfg.AT)
	}
	if v := os.Getenv("FIBOWEB_AT"); v != "" {
		specs = append(specs, v)
	}
	if v := os.Getenv("FIBOWEB_AT_CANDIDATES"); v != "" {
		specs = append(specs, strings.Split(v, ",")...)
	}
	specs = append(specs, defaultATCandidates...)

	tried := map[string]bool{}
	for _, spec := range specs {
		spec = strings.TrimSpace(spec)
		if spec == "" || tried[spec] {
			continue
		}
		tried[spec] = true
		if strings.HasPrefix(spec, "unix:") {
			if _, err := os.Stat(strings.TrimPrefix(spec, "unix:")); err != nil {
				continue
			}
		} else if !strings.HasPrefix(spec, "tcp:") {
			if _, err := os.Stat(spec); err != nil {
				continue
			}
		}
		if !m.probe(spec) {
			continue
		}
		rw, _, err := m.openDevice(spec)
		if err != nil {
			continue
		}
		m.name = spec
		m.eng = newAtEngine(spec, rw)
		m.state = "已就绪"
		if m.cfg != nil && m.cfg.AT != spec {
			m.cfg.AT = spec
			_ = saveConfig(m.cfg)
		}
		logf("AT 通道: %s", spec)
		return m.eng, nil
	}
	m.state = "未找到可用的 AT 通道"
	m.nextTry = time.Now().Add(8 * time.Second)
	return nil, errors.New("未找到可用的 AT 通道（可设置 FIBOWEB_AT=/dev/ttyGS3 或 unix:/dev/atci-service）")
}

func (m *atManager) Exchange(cmd string, timeout time.Duration) AtResp {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, err := m.prepareLocked()
	if err != nil {
		return AtResp{Raw: err.Error()}
	}
	e.drain(120 * time.Millisecond)
	if _, err := e.rw.Write([]byte(cmd + "\r\n")); err != nil {
		e.close()
		m.state = "写入失败: " + err.Error()
		return AtResp{Raw: m.state}
	}
	lines, ok, raw, timedOut := e.collect(cmd, timeout)
	if timedOut {
		raw += "\n(超时)"
	}
	return AtResp{Lines: lines, OK: ok, Raw: raw}
}

func (m *atManager) CmdOK(cmd string, timeout time.Duration) (AtResp, error) {
	r := m.Exchange(cmd, timeout)
	if r.OK {
		return r, nil
	}
	return r, errors.New(extractError(r))
}

// SendSMS 走 CMGS 两步流程（等待 ">" 提示后写入正文并以 Ctrl-Z 结束）。
func (m *atManager) SendSMS(number, body string, timeout time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, err := m.prepareLocked()
	if err != nil {
		return err
	}
	e.drain(120 * time.Millisecond)
	if _, err := e.rw.Write([]byte(fmt.Sprintf("AT+CMGS=\"%s\"\r\n", number))); err != nil {
		e.close()
		return err
	}
	// 等待 ">" 提示
	deadline := time.Now().Add(5 * time.Second)
	prompt := false
	for time.Now().Before(deadline) {
		select {
		case line := <-e.lines:
			if strings.Contains(line, ">") {
				prompt = true
			}
			if strings.Contains(line, "ERROR") {
				return errors.New("发送短信失败: " + line)
			}
		case <-time.After(50 * time.Millisecond):
		}
		if prompt {
			break
		}
	}
	if !prompt {
		return errors.New("等待短信输入提示超时")
	}
	if _, err := e.rw.Write([]byte(body + "\x1a")); err != nil {
		e.close()
		return err
	}
	_, ok, raw, _ := e.collect("", timeout)
	if !ok {
		if raw == "" {
			raw = "无响应"
		}
		return errors.New("发送短信失败: " + raw)
	}
	return nil
}

func extractError(r AtResp) string {
	for _, l := range r.Lines {
		t := strings.TrimSpace(l)
		if strings.HasPrefix(t, "+CME ERROR") || strings.HasPrefix(t, "+CMS ERROR") || t == "ERROR" {
			return t
		}
	}
	raw := strings.ReplaceAll(strings.ReplaceAll(r.Raw, "\r", " "), "\n", " ")
	if strings.TrimSpace(raw) == "" {
		return "模组无响应（超时）"
	}
	if len(raw) > 160 {
		raw = raw[:160]
	}
	return "执行失败: " + raw
}

// Config 是持久化到 /usrdata/fibocom-webui/etc/config.json 的配置。
type Config struct {
	AT        string      `json:"at,omitempty"`
	Language  string      `json:"language"`
	TimeZone  int         `json:"timezone"`
	SMSCenter string      `json:"sms_center,omitempty"`
	Net       NetSettings `json:"net"`
}

var configMu sync.Mutex
var configPath string

func loadConfig(dir string) *Config {
	configPath = dir + "/config.json"
	c := &Config{Language: "ch", TimeZone: 8}
	if b, err := os.ReadFile(configPath); err == nil {
		if err := json.Unmarshal(b, c); err != nil {
			logf("配置文件解析失败，使用默认值: %v", err)
		}
	}
	if c.Language == "" {
		c.Language = "ch"
	}
	c.Net.applyDefaults()
	return c
}

func saveConfig(c *Config) error {
	configMu.Lock()
	defer configMu.Unlock()
	if configPath == "" {
		return nil
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	tmp := configPath + ".new"
	if err := os.WriteFile(tmp, b, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, configPath)
}
