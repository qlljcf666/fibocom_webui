package main

import (
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

func validIPv4(s string) bool {
	ip := net.ParseIP(s)
	return ip != nil && ip.To4() != nil
}

// Snap 是周期轮询得到的模组状态快照。
type Snap struct {
	Manufacturer string
	Model        string
	FW           string
	IMEI         string
	ICCID        string
	IMSI         string
	MSISDN       string
	SimState     int
	PinState     int
	PinRemain    int
	PukRemain    int
	CSQ          int
	SignalLevel  int
	RegState     int
	Operator     string
	RAT          int
	ACT          int
	Roaming      int
	PDNActive    bool
	IPv4         string
	IPv6         string
	RxRate       uint64
	TxRate       uint64
	RxBytes      uint64
	TxBytes      uint64
	CellID       int64
	RSRP         int
	RSRQ         int
	SINR         int
	Band         string
	USBMode      int
}

type App struct {
	at    *atManager
	net   *netManager
	cfg   *Config
	cfgMu sync.Mutex

	snapMu sync.Mutex
	snap   Snap
	start  time.Time

	netMu     sync.Mutex
	lastNetRx uint64
	lastNetTx uint64
	lastNetAt time.Time
	gotGTStat bool
}

func newApp(at *atManager, cfg *Config) *App {
	return &App{at: at, net: newNetManager(cfg, at), cfg: cfg, start: time.Now()}
}

func (a *App) snapshot() Snap {
	a.snapMu.Lock()
	defer a.snapMu.Unlock()
	return a.snap
}

func (a *App) setSnapshot(s Snap) {
	a.snapMu.Lock()
	a.snap = s
	a.snapMu.Unlock()
}

func (a *App) cmd(cmd string, ms int) AtResp {
	return a.at.Exchange(cmd, time.Duration(ms)*time.Millisecond)
}

func (a *App) cmdOK(cmd string, ms int) (AtResp, error) {
	return a.at.CmdOK(cmd, time.Duration(ms)*time.Millisecond)
}

// pollLoop 每 5 秒刷新一次快照，让页面轮询请求可以立即返回。
func (a *App) pollLoop() {
	a.refresh()
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	for range t.C {
		a.refresh()
	}
}

func (a *App) refresh() {
	s := a.snapshot()

	if s.Manufacturer == "" {
		if r := a.cmd("AT+CGMI", 900); r.OK {
			s.Manufacturer = r.First()
		}
	}
	if s.Model == "" {
		if r := a.cmd("AT+CGMM", 900); r.OK {
			if v := r.First(); v != "" {
				s.Model = v
			}
		}
	}
	if s.FW == "" {
		if r := a.cmd("AT+CGMR", 900); r.OK {
			s.FW = r.First()
		}
	}

	if r := a.cmd("AT+CGSN", 900); r.OK {
		if v := digits(r.First()); len(v) >= 15 {
			s.IMEI = v
		}
	}

	if r := a.cmd("AT+CPIN?", 900); r.OK {
		v := strings.ToUpper(r.After("+CPIN"))
		switch {
		case strings.HasPrefix(v, "READY"):
			s.SimState, s.PinState = 7, 2
		case strings.Contains(v, "SIM PIN"):
			s.SimState, s.PinState = 7, 1
		case strings.Contains(v, "SIM PUK"):
			s.SimState, s.PinState = 7, 4
		default:
			s.SimState, s.PinState = 0, 0
		}
	}
	if s.SimState == 7 && s.PinState == 2 {
		if r := a.cmd("AT+CLCK=\"SC\",2", 900); r.OK {
			if strings.TrimSpace(r.After("+CLCK")) != "" {
				if strings.HasPrefix(strings.TrimSpace(r.After("+CLCK")), "1") {
					s.PinState = 2
				} else {
					s.PinState = 3
				}
			}
		}
	}
	if r := a.cmd("AT+CPINR=\"SIM PIN\"", 900); r.OK {
		v := r.After("+CPINR")
		if i := strings.LastIndexByte(v, ','); i >= 0 {
			if n, err := strconv.Atoi(strings.TrimSpace(v[i+1:])); err == nil {
				s.PinRemain = n
			}
		}
	} else if s.PinRemain == 0 {
		s.PinRemain = 3
	}
	if s.PukRemain == 0 {
		s.PukRemain = 10
	}

	if s.ICCID == "" {
		r := a.cmd("AT+ICCID", 900)
		if !r.OK || len(r.Lines) == 0 {
			r = a.cmd("AT+CCID", 900)
		}
		if r.OK {
			v := r.After("+ICCID")
			if v == "" {
				v = r.After("+CCID")
			}
			if v == "" {
				v = r.First()
			}
			if d := digits(v); len(d) >= 8 {
				s.ICCID = d
			}
		}
	}
	if s.IMSI == "" {
		if r := a.cmd("AT+CIMI", 900); r.OK {
			if d := digits(r.First()); len(d) >= 10 {
				s.IMSI = d
			}
		}
	}
	if s.MSISDN == "" {
		if r := a.cmd("AT+CNUM", 900); r.OK {
			v := r.After("+CNUM")
			if parts := strings.Split(v, "\""); len(parts) > 1 && strings.TrimSpace(parts[1]) != "" {
				s.MSISDN = strings.TrimSpace(parts[1])
			}
		}
	}

	if r := a.cmd("AT+CSQ", 900); r.OK {
		s.CSQ = parseCSQ(r.Lines)
		if s.CSQ == 99 || s.CSQ <= 0 {
			s.SignalLevel = 0
		} else {
			lv := s.CSQ / 5
			if lv > 4 {
				lv = 4
			}
			if lv < 0 {
				lv = 0
			}
			s.SignalLevel = lv
		}
	}

	if r := a.cmd("AT+COPS?", 1200); r.OK {
		name, act := parseCOPS(r.Lines)
		if name != "" {
			s.Operator = name
		}
		if act > 0 {
			s.ACT = act
			s.RAT = actToUIRat(act)
		}
	}

	for _, prefix := range []string{"+CEREG", "+CGREG", "+CREG"} {
		r := a.cmd("AT"+prefix+"?", 900)
		if !r.OK {
			continue
		}
		if st, ok := parseReg(r.Lines, prefix); ok {
			s.RegState = st
			if st == 5 {
				s.Roaming = 1
			} else {
				s.Roaming = 0
			}
			break
		}
	}

	if r := a.cmd("AT+CGACT?", 900); r.OK {
		active := false
		for _, l := range r.Lines {
			t := strings.TrimSpace(l)
			if strings.HasPrefix(t, "+CGACT:") {
				f := strings.Split(strings.TrimPrefix(t, "+CGACT:"), ",")
				if len(f) >= 2 && strings.TrimSpace(f[1]) == "1" {
					active = true
				}
			}
		}
		s.PDNActive = active
	}

	if r := a.cmd("AT+CGCONTRDP=1", 1200); r.OK {
		for _, part := range strings.Split(r.After("+CGCONTRDP"), ",") {
			q := strings.Trim(strings.TrimSpace(part), "\"")
			if strings.Count(q, ".") == 3 && !strings.HasPrefix(q, "0.0.0.0") && s.IPv4 == "" {
				s.IPv4 = q
			} else if strings.Contains(q, ":") && len(q) > 6 && s.IPv6 == "" {
				s.IPv6 = q
			}
		}
	}
	if s.IPv4 == "" {
		if r := a.cmd("AT+CGPADDR=1", 900); r.OK {
			for _, part := range strings.Split(r.After("+CGPADDR"), ",") {
				q := strings.Trim(strings.TrimSpace(part), "\"")
				if strings.Contains(q, ".") && !strings.HasPrefix(q, "0.0.0.0") {
					s.IPv4 = q
				}
			}
		}
	}
	// 网卡侧地址优先（FM350 由 mtk_netagent 在自动拨号后配置 ccmni0）。
	if st := a.net.status(); st != nil {
		if v, _ := st["wanIP"].(string); v != "" {
			if s.IPv4 == "" || !s.PDNActive {
				s.IPv4 = v
				s.PDNActive = true
			}
		}
	}

	// 流量：优先 Fibocom GTSTATIS，其次读取 /proc/net/dev。
	got := false
	if r := a.cmd("AT+GTSTATIS?", 1000); r.OK {
		v := r.After("+GTSTATIS")
		if v != "" {
			f := strings.Split(v, ",")
			if len(f) >= 4 {
				s.RxRate = parseU64(f[0])
				s.TxRate = parseU64(f[1])
				s.RxBytes = parseU64(f[2])
				s.TxBytes = parseU64(f[3])
				got = true
			}
		}
	}
	if !got {
		rx, tx, rateRx, rateTx := a.netDevSample()
		s.RxBytes, s.TxBytes = rx, tx
		s.RxRate, s.TxRate = rateRx, rateTx
	}
	a.gotGTStat = got

	if r := a.cmd("AT+GTCCINFO?", 1200); r.OK {
		if cell, rsrp, rsrq, sinr, band, ok := parseCell(r.Lines); ok {
			s.CellID, s.RSRP, s.RSRQ, s.SINR, s.Band = cell, rsrp, rsrq, sinr, band
		}
	}

	a.setSnapshot(s)
}

func (a *App) netDevSample() (rx, tx, rateRx, rateTx uint64) {
	b, err := os.ReadFile("/proc/net/dev")
	if err != nil {
		return 0, 0, 0, 0
	}
	for _, line := range strings.Split(string(b), "\n") {
		if !strings.Contains(line, ":") {
			continue
		}
		name := strings.TrimSpace(strings.SplitN(line, ":", 2)[0])
		if name == "lo" || name == "" {
			continue
		}
		f := strings.Fields(strings.SplitN(line, ":", 2)[1])
		if len(f) < 9 {
			continue
		}
		rx += parseU64(f[0])
		tx += parseU64(f[8])
	}
	a.netMu.Lock()
	defer a.netMu.Unlock()
	now := time.Now()
	dt := now.Sub(a.lastNetAt).Seconds()
	if a.lastNetAt.IsZero() || dt <= 0 {
		a.lastNetRx, a.lastNetTx, a.lastNetAt = rx, tx, now
		return rx, tx, 0, 0
	}
	if rx >= a.lastNetRx {
		rateRx = uint64(float64(rx-a.lastNetRx) / dt)
	}
	if tx >= a.lastNetTx {
		rateTx = uint64(float64(tx-a.lastNetTx) / dt)
	}
	a.lastNetRx, a.lastNetTx, a.lastNetAt = rx, tx, now
	return rx, tx, rateRx, rateTx
}

func (a *App) language() string {
	a.cfgMu.Lock()
	defer a.cfgMu.Unlock()
	if a.cfg.Language == "" || a.cfg.Language == "zh" {
		return "ch"
	}
	return a.cfg.Language
}

func (a *App) setLanguage(v string) {
	a.cfgMu.Lock()
	defer a.cfgMu.Unlock()
	if v == "zh" {
		v = "ch"
	}
	a.cfg.Language = v
	_ = saveConfig(a.cfg)
}

func (a *App) timeZone() int {
	a.cfgMu.Lock()
	defer a.cfgMu.Unlock()
	return a.cfg.TimeZone
}

func (a *App) setTimeZone(v int) {
	a.cfgMu.Lock()
	defer a.cfgMu.Unlock()
	a.cfg.TimeZone = v
	_ = saveConfig(a.cfg)
}

func (a *App) smsCenter() string {
	a.cfgMu.Lock()
	defer a.cfgMu.Unlock()
	return a.cfg.SMSCenter
}

func (a *App) setSMSCenter(v string) {
	a.cfgMu.Lock()
	defer a.cfgMu.Unlock()
	a.cfg.SMSCenter = v
	_ = saveConfig(a.cfg)
}

// ---------------- 参数辅助 ----------------

func sParam(p map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := p[k]; ok {
			switch x := v.(type) {
			case string:
				return x
			case float64:
				return strconv.FormatInt(int64(x), 10)
			}
		}
	}
	return ""
}

func iParam(p map[string]any, keys ...string) int64 {
	for _, k := range keys {
		if v, ok := p[k]; ok {
			switch x := v.(type) {
			case float64:
				return int64(x)
			case string:
				if n, err := strconv.ParseInt(strings.TrimSpace(x), 10, 64); err == nil {
					return n
				}
			}
		}
	}
	return 0
}

func bParam(p map[string]any, keys ...string) bool {
	for _, k := range keys {
		if v, ok := p[k]; ok {
			switch x := v.(type) {
			case bool:
				return x
			case float64:
				return x != 0
			case string:
				return x == "1" || x == "true" || x == "True"
			}
		}
	}
	return false
}

func hasParam(p map[string]any, keys ...string) bool {
	for _, k := range keys {
		if _, ok := p[k]; ok {
			return true
		}
	}
	return false
}

func octets(ip string) []string {
	parts := strings.Split(ip, ".")
	if len(parts) != 4 {
		return []string{"192", "168", "8", "1"}
	}
	return parts
}

func anyInt(v any) int {
	switch x := v.(type) {
	case float64:
		return int(x)
	case int:
		return x
	case string:
		n, _ := strconv.Atoi(strings.TrimSpace(x))
		return n
	}
	return 0
}

func digits(s string) string {
	var b strings.Builder
	for _, c := range s {
		if c >= '0' && c <= '9' {
			b.WriteRune(c)
		}
	}
	return b.String()
}

func parseU64(s string) uint64 {
	s = strings.TrimSpace(s)
	if n, err := strconv.ParseUint(s, 10, 64); err == nil {
		return n
	}
	if n, err := strconv.ParseInt(s, 10, 64); err == nil && n >= 0 {
		return uint64(n)
	}
	return 0
}

func parseCSQ(lines []string) int {
	for _, l := range lines {
		t := strings.TrimSpace(l)
		if strings.HasPrefix(t, "+CSQ:") {
			v := strings.Split(strings.TrimPrefix(t, "+CSQ:"), ",")[0]
			if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
				return n
			}
			return 99
		}
	}
	return 99
}

func parseCOPS(lines []string) (string, int) {
	for _, l := range lines {
		t := strings.TrimSpace(l)
		if !strings.HasPrefix(t, "+COPS:") {
			continue
		}
		parts := strings.Split(strings.TrimPrefix(t, "+COPS:"), ",")
		name, act := "", 0
		for _, p := range parts {
			tr := strings.TrimSpace(p)
			if strings.HasPrefix(tr, "\"") {
				name = strings.Trim(tr, "\"")
			}
		}
		if len(parts) > 0 {
			last := strings.TrimSpace(parts[len(parts)-1])
			var num strings.Builder
			for _, c := range last {
				if c < '0' || c > '9' {
					break
				}
				num.WriteRune(c)
			}
			if n, err := strconv.Atoi(num.String()); err == nil {
				act = n
			}
		}
		return name, act
	}
	return "", 0
}

func parseReg(lines []string, prefix string) (int, bool) {
	for _, l := range lines {
		t := strings.TrimSpace(l)
		if !strings.HasPrefix(t, prefix) {
			continue
		}
		rest := strings.TrimPrefix(t, prefix)
		rest = strings.TrimSpace(strings.TrimPrefix(rest, ":"))
		parts := strings.Split(rest, ",")
		if len(parts) >= 2 {
			if n, err := strconv.Atoi(strings.TrimSpace(parts[1])); err == nil {
				return n, true
			}
		}
	}
	return 0, false
}

func actToUIRat(act int) int {
	switch act {
	case 0, 3:
		return 1
	case 1, 2, 4, 5, 6:
		return 4
	case 7:
		return 6
	case 10, 11, 12, 13, 14:
		return 8
	}
	return 0
}

func uiRatName(rat int) string {
	switch rat {
	case 1:
		return "2G"
	case 4:
		return "3G"
	case 6:
		return "4G"
	case 8:
		return "5G"
	}
	return ""
}

func parseCell(lines []string) (cellID int64, rsrp, rsrq, sinr int, band string, ok bool) {
	for _, l := range lines {
		t := strings.TrimSpace(l)
		if !strings.HasPrefix(t, "+GTCCINFO:") {
			continue
		}
		f := strings.Split(strings.TrimPrefix(t, "+GTCCINFO:"), ",")
		for i := range f {
			f[i] = strings.TrimSpace(f[i])
		}
		if len(f) < 8 {
			continue
		}
		if n, _ := strconv.Atoi(f[0]); n != 1 {
			continue
		}
		rat, _ := strconv.Atoi(f[1])
		cellIdx, rsrpIdx, rsrqIdx, sinrIdx, bandIdx := 5, 12, 13, 10, 8
		switch rat {
		case 4, 5, 6, 9:
			cellIdx, rsrpIdx, rsrqIdx, sinrIdx, bandIdx = 5, 12, 13, 10, 8
		case 2:
			cellIdx, rsrpIdx, rsrqIdx, sinrIdx, bandIdx = 5, 10, 9, 9, 8
		}
		if cellIdx < len(f) {
			cellID, _ = strconv.ParseInt(f[cellIdx], 10, 64)
		}
		if rsrpIdx < len(f) {
			rsrp, _ = strconv.Atoi(f[rsrpIdx])
		}
		if rsrqIdx < len(f) {
			rsrq, _ = strconv.Atoi(f[rsrqIdx])
		}
		if sinrIdx < len(f) {
			sinr, _ = strconv.Atoi(f[sinrIdx])
		}
		if bandIdx < len(f) {
			band = f[bandIdx]
		}
		return cellID, rsrp, rsrq, sinr, band, true
	}
	return 0, 0, 0, 0, "", false
}

type pdpEntry struct {
	cid int
	apn string
}

func parsePDP(lines []string) []pdpEntry {
	var out []pdpEntry
	for _, l := range lines {
		t := strings.TrimSpace(l)
		if !strings.HasPrefix(t, "+CGDCONT:") {
			continue
		}
		parts := strings.Split(strings.TrimPrefix(t, "+CGDCONT:"), ",")
		if len(parts) < 3 {
			continue
		}
		cid, err := strconv.Atoi(strings.TrimSpace(parts[0]))
		if err != nil {
			continue
		}
		out = append(out, pdpEntry{cid: cid, apn: strings.Trim(strings.TrimSpace(parts[2]), "\"")})
	}
	return out
}

// ---------------- 短信辅助 ----------------

func (a *App) smsUnread() int64 {
	_, _ = a.cmdOK("AT+CMGF=1", 1200)
	r := a.cmd("AT+CMGL=\"REC UNREAD\"", 3000)
	var n int64
	for _, l := range r.Lines {
		if strings.HasPrefix(strings.TrimSpace(l), "+CMGL:") {
			n++
		}
	}
	return n
}

func (a *App) smsList() []any {
	_, _ = a.cmdOK("AT+CMGF=1", 2000)
	r := a.cmd("AT+CMGL=\"ALL\"", 5000)
	var items []any
	var idx int64
	var phone, status, tm string
	var body []string
	flush := func() {
		if idx <= 0 {
			return
		}
		text := strings.Join(body, "\n")
		unread := strings.Contains(strings.ToUpper(status), "UNREAD")
		items = append(items, map[string]any{
			"SMSId":           idx,
			"PhoneNumber":     phone,
			"SMSContent":      text,
			"SMSTime":         tm,
			"StorageLocation": boolInt(unread, 1, 2),
			"SMSCount":        1,
			"Unread":          unread,
			"SMSStatus":       boolInt(unread, 0, 1),
		})
	}
	for _, l := range r.Lines {
		t := strings.TrimSpace(l)
		if strings.HasPrefix(t, "+CMGL:") {
			flush()
			body = nil
			parts := csvFields(strings.TrimPrefix(t, "+CMGL:"))
			if len(parts) > 0 {
				idx, _ = strconv.ParseInt(strings.TrimSpace(parts[0]), 10, 64)
			}
			status, phone, tm = "", "", ""
			if len(parts) > 1 {
				status = parts[1]
			}
			if len(parts) > 2 {
				phone = parts[2]
			}
			if len(parts) > 4 {
				tm = parts[4]
			}
		} else if idx > 0 {
			body = append(body, t)
		}
	}
	flush()
	return items
}

func boolInt(b bool, t, f int) int {
	if b {
		return t
	}
	return f
}

// csvFields 解析带引号的逗号分隔字段（短信时间字段本身含逗号）。
func csvFields(s string) []string {
	var out []string
	var cur strings.Builder
	inQuotes := false
	for _, c := range s {
		switch c {
		case '"':
			inQuotes = !inQuotes
		case ',':
			if !inQuotes {
				out = append(out, strings.TrimSpace(cur.String()))
				cur.Reset()
				continue
			}
			cur.WriteRune(c)
		default:
			cur.WriteRune(c)
		}
	}
	out = append(out, strings.TrimSpace(cur.String()))
	return out
}

// ---------------- JSON-RPC 方法 ----------------

func (a *App) handle(method string, params map[string]any) (any, error) {
	if params == nil {
		params = map[string]any{}
	}
	s := a.snapshot()

	switch method {
	case "Login", "Logout", "ChangePassword":
		return map[string]any{}, nil
	case "GetLoginState":
		return map[string]any{
			"LoginState": 0, "LockedTime": 0, "LoginVerify": 0, "LoginVerifyMax": 5,
		}, nil
	case "GetCurrentLanguage":
		return map[string]any{"Language": a.language()}, nil
	case "SetLanguage":
		if v := sParam(params, "Language", "language"); v != "" {
			a.setLanguage(v)
		}
		return map[string]any{}, nil

	case "GetDeviceInfo":
		if s.Model == "" {
			a.refresh()
			s = a.snapshot()
		}
		return map[string]any{
			"DeviceName": s.Model, "ModelName": s.Model, "Manufacturer": s.Manufacturer,
			"IMEI": s.IMEI, "IMSI": s.IMSI, "ICCID": s.ICCID, "MSISDN": s.MSISDN,
			"SN": s.IMEI, "sn": s.IMEI,
			"SwVersion": s.FW, "swVersion": s.FW, "SoftwareVersion": s.FW,
			"HwVersion": "", "hwVersion": "", "HardwareVersion": "",
			"WebUiVersion": webUIVersion, "webUiVersion": webUIVersion,
			"ConfigVersion": "", "MACAddress": "", "macAddress": "",
			"WANIPaddress": s.IPv4, "WanIPAddress": s.IPv4, "wanIP": s.IPv4,
			"WANConnectionStatus": boolInt(s.PDNActive, 1, 0),
			"CellId":              s.CellID, "PLMN": s.Operator, "RAT": s.RAT,
			"CQI": 0, "RSRP": s.RSRP, "RSRQ": s.RSRQ, "SINR": s.SINR, "Band": s.Band,
		}, nil
	case "GetDeviceState":
		return map[string]any{
			"RunTime": int64(time.Since(a.start).Seconds()), "CellId": s.CellID,
			"CQI": 0, "RSRP": s.RSRP, "RSRQ": s.RSRQ, "SINR": s.SINR, "Band": s.Band,
			"PLMN": s.Operator, "RAT": s.RAT, "CSQ": s.CSQ, "SimState": s.SimState,
			"WANConnectionStatus": boolInt(s.PDNActive, 1, 0),
		}, nil
	case "GetCaInfo":
		return map[string]any{"LTE_CaState": 0, "NR_CaState": 0}, nil

	case "GetSimStatus":
		return map[string]any{
			"SIMState": s.SimState, "PinState": s.PinState,
			"PinRemainingTimes": s.PinRemain, "PukRemainingTimes": s.PukRemain,
			"IMSI": s.IMSI, "ICCID": s.ICCID, "MSISDN": s.MSISDN,
		}, nil
	case "UnlockPin":
		pin := sParam(params, "Pin", "PIN", "pin")
		if pin == "" {
			return nil, errors.New("请输入 PIN 码")
		}
		if _, err := a.cmdOK(fmt.Sprintf("AT+CPIN=\"%s\"", pin), 3000); err != nil {
			return nil, err
		}
		a.refresh()
		return map[string]any{}, nil
	case "UnlockPuk":
		puk := sParam(params, "Puk", "PUK", "puk")
		pin := sParam(params, "Pin", "PIN", "pin")
		if puk == "" || pin == "" {
			return nil, errors.New("请输入 PUK 和新的 PIN")
		}
		if _, err := a.cmdOK(fmt.Sprintf("AT+CPIN=\"%s\",\"%s\"", puk, pin), 5000); err != nil {
			return nil, err
		}
		a.refresh()
		return map[string]any{}, nil
	case "ChangePinCode":
		old := sParam(params, "Pin", "OldPin")
		nw := sParam(params, "NewPin", "newPin")
		if old == "" || nw == "" {
			return nil, errors.New("请输入原 PIN 与新 PIN")
		}
		if _, err := a.cmdOK(fmt.Sprintf("AT+CPIN=\"%s\",\"%s\"", old, nw), 5000); err != nil {
			return nil, err
		}
		return map[string]any{}, nil
	case "ChangePinState":
		pin := sParam(params, "Pin", "PIN")
		enable := 0
		if iParam(params, "State") == 2 {
			enable = 1
		}
		if _, err := a.cmdOK(fmt.Sprintf("AT+CLCK=\"SC\",%d,\"%s\"", enable, pin), 4000); err != nil {
			return nil, err
		}
		a.refresh()
		return map[string]any{}, nil

	case "GetNetworkInfo":
		return map[string]any{
			"NetworkName": s.Operator, "RAT": s.RAT, "CSQ": s.CSQ, "PLMN": s.Operator,
			"ListNetworkInfo": []any{map[string]any{
				"Roaming": s.Roaming, "MCC": "", "MNC": "", "NetworkName": s.Operator,
			}},
		}, nil
	case "GetSignalStrength":
		rssi := -113
		if s.CSQ != 99 {
			rssi = -113 + 2*s.CSQ
		}
		return map[string]any{
			"SignalLevel": s.SignalLevel, "RSRP": s.RSRP, "RSRQ": s.RSRQ,
			"SINR": s.SINR, "RSSI": rssi, "CSQ": s.CSQ,
		}, nil
	case "GetNetworkRegisterState":
		reg := 0
		switch s.RegState {
		case 1, 5:
			reg = 2
		case 2:
			reg = 1
		case 3:
			reg = 3
		}
		return map[string]any{"regist_state": reg}, nil
	case "GetNetworkSettings":
		gtRat := s.RAT
		bands := []any{}
		if r := a.cmd("AT+GTACT?", 1200); r.OK {
			if v := r.After("+GTACT"); v != "" {
				f := csvFields(v)
				if len(f) > 0 {
					if n, err := strconv.Atoi(strings.TrimSpace(f[0])); err == nil && n > 0 {
						gtRat = n
					}
				}
				for i := 3; i < len(f); i++ {
					if n, err := strconv.Atoi(strings.TrimSpace(f[i])); err == nil && n > 0 {
						bands = append(bands, n)
					}
				}
			}
		}
		return map[string]any{
			"NetselectionMode": 0, "Band_LIST": bands, "RATPrefer": 0,
			"RATPriority": 0, "RAT": s.RAT, "GTACTRat": gtRat,
		}, nil
	case "SetNetworkSettings":
		// 自动/手动选网
		if hasParam(params, "NetselectionMode") && iParam(params, "NetselectionMode") == 1 {
			plmn := sParam(params, "PLMN", "Plmn", "OperatorCode")
			if plmn == "" {
				plmn = sParam(params, "MCC", "MNC")
			}
			if plmn != "" {
				if _, err := a.cmdOK(fmt.Sprintf("AT+COPS=1,2,\"%s\"", plmn), 8000); err != nil {
					return nil, err
				}
			}
		} else if hasParam(params, "NetselectionMode") {
			if _, err := a.cmdOK("AT+COPS=0", 8000); err != nil {
				return nil, err
			}
		}
		// 锁频段：AT+GTACT=<rat>,0,0,<band...>（0 表示自动）
		if hasParam(params, "Band_LIST", "BandList", "Bands") {
			rat := int(iParam(params, "GTACTRat", "RATPrefer"))
			if rat <= 0 {
				rat = 20
			}
			var items []any
			for _, k := range []string{"Band_LIST", "BandList", "Bands"} {
				if v, ok := params[k]; ok {
					items, _ = v.([]any)
					if items != nil {
						break
					}
				}
			}
			var codes []string
			for _, it := range items {
				if n := anyInt(it); n > 0 {
					codes = append(codes, strconv.Itoa(n))
				}
			}
			cmd := fmt.Sprintf("AT+GTACT=%d,0,0", rat)
			if len(codes) == 0 {
				cmd += ",0"
			} else {
				cmd += "," + strings.Join(codes, ",")
			}
			if _, err := a.cmdOK(cmd, 8000); err != nil {
				return nil, err
			}
			logf("锁频段: %s", cmd)
		}
		a.refresh()
		return map[string]any{}, nil
	case "GetNRNetworkMode":
		return map[string]any{"NRState": boolInt(s.RAT == 8, 1, 0), "NRMode": 3}, nil
	case "SetNRNetworkMode":
		on := bParam(params, "NRState") || iParam(params, "NRState") == 1
		cmd, alt := "AT+GTRAT=3", "AT+GTACT=3"
		if on {
			cmd, alt = "AT+GTRAT=20", "AT+GTACT=17"
		}
		if _, err := a.cmdOK(cmd, 4000); err == nil {
			return map[string]any{}, nil
		}
		if _, err := a.cmdOK(alt, 4000); err != nil {
			return nil, err
		}
		return map[string]any{}, nil
	case "GetMobileDataStatus":
		st := a.net.status()
		ip, _ := st["wanIP"].(string)
		return map[string]any{
			"MobileDateSwitch": boolInt(ip != "", 1, 0), "RoamingSwitch": s.Roaming,
			"AutoDial": st["autoDial"], "NetMode": st["mode"], "NetState": st["state"],
		}, nil
	case "SetMobileDataStatus":
		if bParam(params, "MobileDateSwitch") || iParam(params, "MobileDateSwitch") == 1 {
			a.net.update(func(ns *NetSettings) { ns.AutoDial = true })
			_ = saveConfig(a.cfg)
			go a.net.dialAsync()
		} else {
			ns := a.net.settings()
			a.net.update(func(n *NetSettings) { n.AutoDial = false })
			_ = saveConfig(a.cfg)
			_, _ = a.cmdOK(fmt.Sprintf("AT+CGACT=0,%d", ns.CID), 10000)
		}
		return map[string]any{"MobileDateSwitch": boolInt(bParam(params, "MobileDateSwitch"), 1, 0)}, nil

	case "GetWANInfo":
		st := a.net.status()
		return map[string]any{
			"ConnectionStatus": boolInt(s.PDNActive, 1, 0),
			"WANIPaddress":     s.IPv4, "IPv4Adrress": s.IPv4, "IPv6Adrress": s.IPv6,
			"IPaddress":      s.IPv4,
			"DefaultGateway": st["gateway"], "PrimaryDNS": st["dns1"], "SecondaryDNS": st["dns2"],
			"DialProtocol": 0, "Duration": int64(time.Since(a.start).Seconds()),
		}, nil

	case "GetNetworkMode", "GetNetStatus":
		return a.net.status(), nil
	case "SetNetworkMode":
		a.net.update(func(ns *NetSettings) {
			mode := sParam(params, "Mode", "mode")
			switch mode {
			case "0", "route", "Route", "NAT", "nat":
				ns.Mode = NetModeRoute
			case "1", "bridge", "Bridge", "passthrough":
				ns.Mode = NetModeBridge
			case "2", "off", "Off", "none":
				ns.Mode = NetModeOff
			}
			if hasParam(params, "AutoDial", "autoDial", "AutoDialSwitch") {
				ns.AutoDial = bParam(params, "AutoDial", "autoDial", "AutoDialSwitch")
			}
			if v := sParam(params, "APN", "apn"); v != "" {
				ns.APN = v
			}
			if v := sParam(params, "UserName", "User", "user"); v != "" {
				ns.User = v
			}
			if v := sParam(params, "Password", "Pass", "pass"); v != "" {
				ns.Pass = v
			}
			if hasParam(params, "AuthType", "Auth") {
				ns.Auth = int(iParam(params, "AuthType", "Auth"))
			}
			if v := sParam(params, "PdpType", "PDPType"); v != "" {
				switch v {
				case "1":
					ns.PDPType = "IPV6"
				case "2":
					ns.PDPType = "IPV4V6"
				default:
					if strings.EqualFold(v, "IP") || strings.EqualFold(v, "IPV4") || strings.EqualFold(v, "IPV4V6") || strings.EqualFold(v, "IPV6") {
						ns.PDPType = strings.ToUpper(v)
					}
				}
			}
			if hasParam(params, "ProfileIndex", "CID", "Cid") {
				if v := int(iParam(params, "ProfileIndex", "CID", "Cid")); v > 0 {
					ns.CID = v
				}
			}
			if v := sParam(params, "LanIface"); v != "" {
				ns.LanIface = v
			}
			if v := sParam(params, "WanIface"); v != "" {
				ns.WanIface = v
			}
			if v := sParam(params, "LanIP", "IPaddress"); validIPv4(v) {
				ns.LanIP = v
			}
			if v := sParam(params, "PoolStart", "AddressPoolBegin"); validIPv4(v) {
				ns.PoolStart = v
			}
			if v := sParam(params, "PoolEnd", "AddressPoolEnd"); validIPv4(v) {
				ns.PoolEnd = v
			}
			if v := int(iParam(params, "LeaseHours", "AddressLeaseTerm", "Leasetime")); v > 0 {
				ns.LeaseHours = v
			}
			if v := sParam(params, "DNS1", "PrimaryDNS"); validIPv4(v) {
				ns.DNS1 = v
			}
			if v := sParam(params, "DNS2", "SecondaryDNS"); validIPv4(v) {
				ns.DNS2 = v
			}
		})
		_ = saveConfig(a.cfg)
		a.net.kick()
		return a.net.status(), nil

	case "GetDHCPSettings", "GetLanSettings":
		ns := a.net.settings()
		st := a.net.status()
		lanIP, _ := st["lanIP"].(string)
		if lanIP == "" {
			lanIP = ns.LanIP
		}
		o := octets(lanIP)
		so := octets(ns.PoolStart)
		eo := octets(ns.PoolEnd)
		d1, _ := st["dns1"].(string)
		d2, _ := st["dns2"].(string)
		return map[string]any{
			"DHCPStatus": 1, "setDHCPServe": true,
			"IPaddress": lanIP, "NetMask": "255.255.255.0",
			"NetMask1": "255", "NetMask2": "255", "NetMask3": "255", "NetMask4": "0",
			"Gateway1": o[0], "Gateway2": o[1], "Gateway3": o[2], "Gateway4": o[3],
			"AddressPoolBegin": ns.PoolStart, "AddressPoolEnd": ns.PoolEnd,
			"AddressPoolBegin1": so[2], "AddressPoolBegin2": so[3],
			"AddressPoolEnd1": eo[2], "AddressPoolEnd2": eo[3],
			"AddressLeaseTerm": ns.LeaseHours, "Leasetime": ns.LeaseHours,
			"PrimaryDNS": d1, "SecondaryDNS": d2,
		}, nil
	case "SetDHCPSettings", "SetLanSettings":
		a.net.update(func(ns *NetSettings) {
			g1, g2, g3, g4 := sParam(params, "Gateway1"), sParam(params, "Gateway2"), sParam(params, "Gateway3"), sParam(params, "Gateway4")
			cand := strings.Join([]string{g1, g2, g3, g4}, ".")
			if v := sParam(params, "IPaddress"); validIPv4(v) {
				ns.LanIP = v
			} else if g1 != "" && g2 != "" && g3 != "" && g4 != "" && validIPv4(cand) {
				ns.LanIP = cand
			}
			b1, b2 := sParam(params, "AddressPoolBegin1"), sParam(params, "AddressPoolBegin2")
			e1, e2 := sParam(params, "AddressPoolEnd1"), sParam(params, "AddressPoolEnd2")
			if b1 != "" && b2 != "" && g3 != "" {
				if v := fmt.Sprintf("%s.%s.%s.%s", g1, g2, b1, b2); validIPv4(v) {
					ns.PoolStart = v
				}
			} else if v := sParam(params, "AddressPoolBegin"); validIPv4(v) {
				ns.PoolStart = v
			}
			if e1 != "" && e2 != "" && g3 != "" {
				if v := fmt.Sprintf("%s.%s.%s.%s", g1, g2, e1, e2); validIPv4(v) {
					ns.PoolEnd = v
				}
			} else if v := sParam(params, "AddressPoolEnd"); validIPv4(v) {
				ns.PoolEnd = v
			}
			if v := int(iParam(params, "AddressLeaseTerm", "Leasetime", "LeaseHours")); v > 0 {
				ns.LeaseHours = v
			}
			if v := sParam(params, "PrimaryDNS"); validIPv4(v) {
				ns.DNS1 = v
			}
			if v := sParam(params, "SecondaryDNS"); validIPv4(v) {
				ns.DNS2 = v
			}
		})
		_ = saveConfig(a.cfg)
		a.net.kick()
		return map[string]any{}, nil
	case "GetDhcpLeases", "GetDHCPLeases":
		list := []any{}
		for _, l := range a.net.leases() {
			list = append(list, map[string]any{
				"IPAddress": l.IP, "MACAddress": l.MAC, "Leasetime": l.Expiry.Format("2006-01-02 15:04:05"),
			})
		}
		return map[string]any{"DeviceList": list, "Device_count": len(list)}, nil

	case "GetEthnetInfo", "GetEthernetInfo":
		port := int(iParam(params, "Port"))
		st := a.net.status()
		if port == 1 {
			wan, _ := st["wanIface"].(string)
			wanIP, _ := st["wanIP"].(string)
			return map[string]any{"Port": 1, "LinkStatus": boolInt(wan != "" && wanIP != "", 1, 0), "IPaddress": wanIP}, nil
		}
		lan, _ := st["lanIface"].(string)
		return map[string]any{"Port": 0, "LinkStatus": boolInt(lan != "", 1, 0), "IPaddress": st["lanIP"]}, nil

	case "DialNow", "StartDial":
		a.net.update(func(ns *NetSettings) { ns.AutoDial = true })
		_ = saveConfig(a.cfg)
		go a.net.dialAsync()
		return a.net.status(), nil
	case "Hangup", "StopDial":
		ns := a.net.settings()
		a.net.update(func(n *NetSettings) { n.AutoDial = false })
		_ = saveConfig(a.cfg)
		_, _ = a.cmdOK(fmt.Sprintf("AT+CGACT=0,%d", ns.CID), 10000)
		a.net.setState("已断开数据连接")
		return a.net.status(), nil
	case "GetDialStatus":
		return a.net.status(), nil

	case "GetConnectionInfo":
		return map[string]any{
			"ConnectionStatus": boolInt(s.PDNActive, 2, 1),
			"DlRate":           s.RxRate * 8, "UlRate": s.TxRate * 8,
			"IPv4Adrress": s.IPv4, "IPv6Adrress": s.IPv6,
			"DlBytes": s.RxBytes, "UlBytes": s.TxBytes, "ConnectionTime": 0,
		}, nil
	case "GetConnectionDevices", "GetDiffConnectionDevices":
		return map[string]any{"DeviceList": []any{}}, nil
	case "EditConnectionDevices", "DeleteConnectionDevices":
		return map[string]any{}, nil
	case "GetConnectionSettings":
		ns := a.net.settings()
		pt := 2
		switch strings.ToUpper(ns.PDPType) {
		case "IP", "IPV4":
			pt = 0
		case "IPV6":
			pt = 1
		}
		return map[string]any{
			"ConnectMode": 0, "PdpType": pt, "APN": ns.APN,
			"UserName": ns.User, "Password": ns.Pass, "AuthType": ns.Auth,
			"ProfileIndex": ns.CID,
		}, nil
	case "SetConnectionSettings":
		a.net.update(func(ns *NetSettings) {
			if v := sParam(params, "APN", "apn"); v != "" {
				ns.APN = v
			}
			if v := sParam(params, "UserName", "User"); v != "" {
				ns.User = v
			}
			if v := sParam(params, "Password", "Pass"); v != "" {
				ns.Pass = v
			}
			if hasParam(params, "AuthType") {
				ns.Auth = int(iParam(params, "AuthType"))
			}
			if hasParam(params, "PdpType") {
				switch int(iParam(params, "PdpType")) {
				case 0:
					ns.PDPType = "IPV4"
				case 1:
					ns.PDPType = "IPV6"
				default:
					ns.PDPType = "IPV4V6"
				}
			}
		})
		_ = saveConfig(a.cfg)
		a.net.kick()
		return map[string]any{}, nil

	case "GetProfileList":
		r := a.cmd("AT+CGDCONT?", 1200)
		var list []any
		if r.OK {
			for _, p := range parsePDP(r.Lines) {
				name := fmt.Sprintf("Profile %d", p.cid)
				if p.cid == 1 {
					name = "默认配置"
				}
				list = append(list, map[string]any{
					"ProfileIndex": p.cid, "ProfileName": name, "APN": p.apn,
					"UserName": "", "Password": "", "PdpType": 0, "AuthType": 0,
					"IsPredefine": p.cid == 1, "AlwaysSaved": 1,
				})
			}
		}
		if len(list) == 0 {
			list = append(list, map[string]any{
				"ProfileIndex": 1, "ProfileName": "默认配置", "APN": "",
				"UserName": "", "Password": "", "PdpType": 0, "AuthType": 0,
				"IsPredefine": true, "AlwaysSaved": 1,
			})
		}
		return map[string]any{"Profile_count": len(list), "ProfileList": list}, nil
	case "AddProfile", "EditProfile":
		cid := iParam(params, "ProfileIndex")
		if cid <= 0 {
			return nil, errors.New("无效的配置索引")
		}
		pdp := "IP"
		switch iParam(params, "PdpType") {
		case 1:
			pdp = "IPV6"
		case 2:
			pdp = "IPV4V6"
		}
		if _, err := a.cmdOK(fmt.Sprintf("AT+CGDCONT=%d,\"%s\",\"%s\"", cid, pdp, sParam(params, "APN")), 2000); err != nil {
			return nil, err
		}
		return map[string]any{}, nil
	case "DeleteProfile":
		cid := iParam(params, "ProfileIndex")
		if cid <= 0 {
			return nil, errors.New("无效的配置索引")
		}
		_, _ = a.cmdOK(fmt.Sprintf("AT+CGDCONT=%d", cid), 2000)
		return map[string]any{}, nil
	case "SetDefaultProfile":
		cid := iParam(params, "ProfileIndex")
		_, _ = a.cmdOK(fmt.Sprintf("AT+CGACT=1,%d", cid), 4000)
		_, _ = a.cmdOK(fmt.Sprintf("AT+GTRNDIS=1,%d", cid), 4000)
		return map[string]any{}, nil

	case "GetSMS":
		list := a.smsList()
		return map[string]any{
			"SMSList": list, "SMSCount": len(list), "SIMState": s.SimState,
		}, nil
	case "GetSMSStatisticsInfo":
		used, max := 0, 0
		if r := a.cmd("AT+CPMS?", 1200); r.OK {
			v := r.After("+CPMS")
			parts := strings.Split(v, ",")
			if len(parts) >= 3 {
				used, _ = strconv.Atoi(strings.TrimSpace(parts[1]))
				max, _ = strconv.Atoi(strings.TrimSpace(parts[2]))
			}
		}
		return map[string]any{"Unread": a.smsUnread(), "Used": used, "Max": max}, nil
	case "SendSMS":
		var numbers []string
		switch v := params["PhoneNumber"].(type) {
		case []any:
			for _, x := range v {
				if sv, ok := x.(string); ok && sv != "" {
					numbers = append(numbers, sv)
				}
			}
		case string:
			if v != "" {
				numbers = append(numbers, v)
			}
		}
		content := sParam(params, "SMSContent")
		if len(numbers) == 0 || content == "" {
			return nil, errors.New("请输入号码和短信内容")
		}
		if _, err := a.cmdOK("AT+CMGF=1", 2000); err != nil {
			return nil, err
		}
		for _, n := range numbers {
			if err := a.at.SendSMS(n, content, 20*time.Second); err != nil {
				return nil, err
			}
		}
		return map[string]any{}, nil
	case "DeleteSMS":
		var ids []int64
		if arr, ok := params["SMSIdArry"].([]any); ok {
			for _, v := range arr {
				if f, ok := v.(float64); ok {
					ids = append(ids, int64(f))
				}
			}
		} else if arr, ok := params["SMSId"].([]any); ok {
			for _, v := range arr {
				if f, ok := v.(float64); ok {
					ids = append(ids, int64(f))
				}
			}
		}
		if len(ids) > 0 {
			for _, id := range ids {
				if id > 0 {
					_, _ = a.cmdOK(fmt.Sprintf("AT+CMGD=%d", id), 2000)
				}
			}
		} else if v := iParam(params, "SMSId"); v > 0 {
			_, _ = a.cmdOK(fmt.Sprintf("AT+CMGD=%d", v), 2000)
		} else {
			_, _ = a.cmdOK("AT+CMGD=1,4", 4000)
		}
		return map[string]any{}, nil
	case "ReadSMS", "SaveSMS", "ImportFromSim":
		return map[string]any{}, nil
	case "GetSMSSettings":
		center := ""
		if r := a.cmd("AT+CSCA?", 1200); r.OK {
			v := r.After("+CSCA")
			center = strings.Trim(strings.TrimSpace(strings.Split(v, ",")[0]), "\"")
		}
		store := 0
		if a.smsCenter() != "" {
			store = 1
		}
		return map[string]any{"SMSCenter": center, "StoreFlag": store}, nil
	case "SetSMSSettings":
		center := sParam(params, "SMSCenter")
		if center != "" {
			if _, err := a.cmdOK(fmt.Sprintf("AT+CSCA=\"%s\"", center), 2000); err != nil {
				return nil, err
			}
			a.setSMSCenter(center)
		}
		return map[string]any{}, nil
	case "GetSMSReportState":
		return map[string]any{"ReportFlag": 0}, nil
	case "SetSMSReportState", "SetAPSMSFULLSwitch":
		return map[string]any{}, nil
	case "GetAPSMSFULLSwitch":
		return map[string]any{"GetAPSMSFULLSwitch": 0}, nil

	case "GetUsageRecord":
		total := s.RxBytes + s.TxBytes
		return map[string]any{
			"UsageRecordList": []any{}, "Total": total, "Used": total, "Unit": 0,
			"Duration": int64(time.Since(a.start).Seconds()), "clear_time": "", "LastClearTime": "",
		}, nil
	case "GetDailyUsageRecord", "GetMonthlyUsageRecord":
		return map[string]any{"Total": s.RxBytes + s.TxBytes, "Unit": 0}, nil
	case "GetDailyUsageSettings":
		return map[string]any{"LimitSwitch": 0, "Total": 0, "Unit": 0, "AutoDisconnect": 0}, nil
	case "GetMonthlyUsageSettings":
		return map[string]any{"LimitSwitch": 0, "Total": 0, "Unit": 1, "AutoDisconnect": 0}, nil
	case "SetDailyUsageSettings", "SetMonthlyUsageSettings", "SetUsageRecordClear":
		return map[string]any{}, nil

	case "SetDeviceReset", "Restart", "Reboot":
		_ = a.cmd("AT+CFUN=1,1", 1500)
		return map[string]any{}, nil
	case "GetFactoryResetState":
		return map[string]any{"FactoryResetState": 0}, nil
	case "FactoryReset":
		_ = a.cmd("AT+GTFACTORYRESET=1", 2000)
		return map[string]any{}, nil
	case "GetTimeZone":
		return map[string]any{"TimeZone": a.timeZone()}, nil
	case "SetTimeZone":
		a.setTimeZone(int(iParam(params, "TimeZone")))
		return map[string]any{}, nil
	case "GetSNTPInfo":
		return map[string]any{"SNTPState": 0, "SNTPServer": "", "SNTPInterval": 0}, nil
	case "SetSNTPInfo":
		return map[string]any{}, nil
	case "GetLogLvl":
		return map[string]any{"LogLvl": 3}, nil
	case "SetLogLvl":
		return map[string]any{}, nil
	case "GetDeviceNewVersion", "HostGetDeviceNewVersion":
		return map[string]any{
			"NewVersion": 0, "CurrentVersion": s.FW, "Version": s.FW,
			"Description": "", "ReleaseNote": "",
		}, nil
	case "GetDownloadState", "GetUpgradeState", "GetUpgradeStateEx":
		return map[string]any{"State": 0, "Progress": 0}, nil
	case "SetDownloadConfig", "StartDownload", "StartUpgrade", "StartUpdate", "StartUpgradeEx":
		return nil, errors.New("当前版本不支持在线升级，请使用厂商升级工具")
	case "StartDiagnosis":
		host := sParam(params, "DiagnosisIP", "IP", "Ip")
		if host == "" {
			return nil, errors.New("请输入诊断目标地址")
		}
		r := a.cmd(fmt.Sprintf("AT+GTPING=\"%s\",4", host), 15000)
		return map[string]any{"DiagnosisResult": boolInt(r.OK, 1, 0), "Raw": r.Raw}, nil
	case "DiagnosisSet":
		return map[string]any{}, nil
	case "OneClickCheck":
		return map[string]any{
			"SIMState": s.SimState, "regState": s.RegState,
			"ConnectionStatus": boolInt(s.PDNActive, 1, 0),
			"ServiceStatus":    s.RegState == 1 || s.RegState == 5,
		}, nil

	case "SendRawData":
		cmd := sParam(params, "ATCommand", "Command", "cmd", "data")
		if cmd == "" || !strings.HasPrefix(strings.ToUpper(cmd), "AT") {
			return map[string]any{}, nil
		}
		r := a.cmd(cmd, 5000)
		return map[string]any{"Raw": r.Raw, "Result": r.Raw}, nil

	// 以太网 / WLAN / 路由 / VPN 等模组不具备的页面，统一返回空对象，
	// 保证前端不会因字段缺失而报错。
	default:
		return map[string]any{}, nil
	}
}
