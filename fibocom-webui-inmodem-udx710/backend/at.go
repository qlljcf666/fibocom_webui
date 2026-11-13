package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
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

// 常见 Fibocom 模组的 AT 通道候选，按优先级探测。
// FG652/FM650 基于 UNISOC UDX710（aarch64）：ofono 与 atrouter 会占用
// /dev/stty_lte4..7（atrouter 的 CPChnl），因此 ofono SendAtcmd 优先探测
// （见 prepareLocked），失败后再尝试下列串口。
var defaultATCandidates = []string{
	"/dev/ttyAT",
	"/dev/ttyAT0",
	"/dev/ttyAT1",
	"/dev/stty_lte4",
	"/dev/stty_lte5",
	"/dev/stty_lte6",
	"/dev/stty_lte7",
	"/dev/ttyGS0",
	"/dev/ttyGS1",
	"/dev/smd11",
	"/dev/smd9",
	"/dev/smd7",
	"/dev/smd8",
	"/dev/at_mdm0",
	"/dev/ttyHS0",
	"/dev/ttyMSM0",
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
	if strings.HasPrefix(spec, "ofono:") {
		path := strings.TrimPrefix(spec, "ofono:")
		if path == "" {
			path = "/ril_0"
		}
		return newOfonoConn(path), spec, nil
	}
	if strings.HasPrefix(spec, "tcp:") {
		addr := strings.TrimPrefix(spec, "tcp:")
		conn, err := net.DialTimeout("tcp", addr, 3*time.Second)
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
	// UDX710 上 ofono/atrouter 已占用模组 AT 通道，优先通过 ofono SendAtcmd 转发，
	// 失败后再直接打开串口（含 atrouter 使用的 /dev/stty_lte4..7）。
	for _, p := range ofonoPaths() {
		specs = append(specs, "ofono:"+p)
	}
	specs = append(specs, defaultATCandidates...)

	tried := map[string]bool{}
	for _, spec := range specs {
		spec = strings.TrimSpace(spec)
		if spec == "" || tried[spec] {
			continue
		}
		tried[spec] = true
		if spec == "ofono" || spec == "ofono:" {
			for _, p := range ofonoPaths() {
				specs = append(specs, "ofono:"+p)
			}
			continue
		}
		if strings.HasPrefix(spec, "ofono:") {
			path := strings.TrimPrefix(spec, "ofono:")
			if path == "" {
				path = "/ril_0"
			}
			if !m.probeOfono(path) {
				continue
			}
		} else {
			if !strings.HasPrefix(spec, "tcp:") {
				if _, err := os.Stat(spec); err != nil {
					continue
				}
			}
			if !m.probe(spec) {
				continue
			}
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
	return nil, errors.New("未找到可用的 AT 通道（可设置 FIBOWEB_AT=/dev/ttyAT 或 ofono:/ril_0）")
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
	if strings.HasPrefix(m.name, "ofono:") {
		path := strings.TrimPrefix(m.name, "ofono:")
		return ofonoSendSMS(path, number, body, timeout)
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

// ---------- ofono（UDX710）AT 回退 ----------
// FG652/FM650 的 UDX710 固件通常由 ofono 占用模组 AT 通道；
// 直接打开 /dev/ttyAT 可能失败，这里通过 org.ofono.Modem.SendAtcmd 转发。

func ofonoPaths() []string {
	if v := strings.TrimSpace(os.Getenv("FIBOWEB_OFONO_PATH")); v != "" {
		return []string{v}
	}
	return []string{"/ril_0", "/ril_1", "/ril_2"}
}

func (m *atManager) probeOfono(path string) bool {
	resp, err := ofonoSendAT(path, "AT", 3*time.Second)
	return err == nil && strings.Contains(strings.ToUpper(resp), "OK")
}

func ofonoRaw(path, method string, timeout time.Duration, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	var lastErr error

	if bin, err := exec.LookPath("gdbus"); err == nil {
		gargs := []string{"call", "--system", "--dest", "org.ofono",
			"--object-path", path, "--method", method}
		gargs = append(gargs, args...)
		out, err := exec.CommandContext(ctx, bin, gargs...).CombinedOutput()
		if err == nil {
			return string(out), nil
		}
		lastErr = fmt.Errorf("gdbus: %v (%s)", err, truncateText(string(out), 120))
	}

	if bin, err := exec.LookPath("dbus-send"); err == nil {
		dargs := []string{"--system", "--print-reply", "--dest=org.ofono", path, method}
		for _, a := range args {
			dargs = append(dargs, "string:"+a)
		}
		out, err := exec.CommandContext(ctx, bin, dargs...).CombinedOutput()
		if err == nil {
			return string(out), nil
		}
		lastErr = fmt.Errorf("dbus-send: %v (%s)", err, truncateText(string(out), 120))
	}

	if lastErr == nil {
		lastErr = errors.New("未安装 gdbus 或 dbus-send")
	}
	return "", lastErr
}

func ofonoSendAT(path, cmd string, timeout time.Duration) (string, error) {
	out, err := ofonoRaw(path, "org.ofono.Modem.SendAtcmd", timeout, cmd)
	if err != nil {
		return "", err
	}
	if s, ok := parseOfonoString(out); ok {
		return s, nil
	}
	return "", errors.New("无法解析 ofono 返回: " + truncateText(out, 160))
}

func parseOfonoString(out string) (string, bool) {
	s := strings.TrimSpace(out)
	// dbus-send:   string "OK\r\n"
	if i := strings.Index(s, "string \""); i >= 0 {
		body := s[i+len("string \""):]
		if j := strings.LastIndex(body, "\""); j >= 0 {
			return unescapeAT(body[:j]), true
		}
	}
	// gdbus: ('OK\r\n',)
	if i := strings.Index(s, "('"); i >= 0 {
		body := s[i+2:]
		if j := strings.LastIndex(body, "',)"); j >= 0 {
			return unescapeAT(body[:j]), true
		}
	}
	return "", false
}

func unescapeAT(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) {
			i++
			switch s[i] {
			case 'n':
				b.WriteByte('\n')
			case 'r':
				b.WriteByte('\r')
			case 't':
				b.WriteByte('\t')
			default:
				b.WriteByte(s[i])
			}
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func normalizeATResponse(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	s = strings.Trim(s, "\n")
	if s == "" {
		return "OK\n"
	}
	return s + "\n"
}

func truncateText(s string, n int) string {
	s = strings.ReplaceAll(strings.ReplaceAll(s, "\r", " "), "\n", " ")
	s = strings.TrimSpace(s)
	if len(s) > n {
		return s[:n]
	}
	return s
}

// ofonoConn 把 ofono 的请求/响应包装成 io.ReadWriteCloser，
// 复用 atEngine 的常驻读协程与超时收集逻辑。
type ofonoConn struct {
	pr        *io.PipeReader
	pw        *io.PipeWriter
	path      string
	closeOnce sync.Once
}

func newOfonoConn(path string) *ofonoConn {
	pr, pw := io.Pipe()
	return &ofonoConn{pr: pr, pw: pw, path: path}
}

func (c *ofonoConn) Read(p []byte) (int, error) { return c.pr.Read(p) }

func (c *ofonoConn) Write(p []byte) (int, error) {
	cmd := strings.TrimSpace(strings.Trim(string(p), "\r\n\x1a"))
	if cmd == "" {
		return len(p), nil
	}
	resp, err := ofonoSendAT(c.path, cmd, 8*time.Second)
	if err != nil {
		_, _ = c.pw.Write([]byte("ERROR\n"))
		return len(p), nil
	}
	_, _ = c.pw.Write([]byte(normalizeATResponse(resp)))
	return len(p), nil
}

func (c *ofonoConn) Close() error {
	c.closeOnce.Do(func() {
		_ = c.pw.Close()
		_ = c.pr.Close()
	})
	return nil
}

func ofonoSendSMS(path, number, body string, timeout time.Duration) error {
	if timeout <= 0 || timeout > 30*time.Second {
		timeout = 20 * time.Second
	}
	if _, err := ofonoRaw(path, "org.ofono.SmsManager.SendMessage", timeout, number, body); err != nil {
		return fmt.Errorf("发送短信失败: %v", err)
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

// Config 是持久化到安装目录 etc/config.json 的配置。
type Config struct {
	AT        string `json:"at,omitempty"`
	Language  string `json:"language"`
	TimeZone  int    `json:"timezone"`
	SMSCenter string `json:"sms_center,omitempty"`
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
