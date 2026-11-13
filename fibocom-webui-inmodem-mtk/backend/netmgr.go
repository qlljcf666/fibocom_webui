package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	NetModeRoute  = "route"
	NetModeBridge = "bridge"
	NetModeOff    = "off"
)

// NetSettings 是 FM350 缺失能力的本地补全配置：
// 自动拨号、路由（NAT）、网桥/透传、内置 DHCP 服务器。
type NetSettings struct {
	Mode       string `json:"mode"`
	AutoDial   bool   `json:"auto_dial"`
	APN        string `json:"apn,omitempty"`
	User       string `json:"user,omitempty"`
	Pass       string `json:"pass,omitempty"`
	Auth       int    `json:"auth,omitempty"` // 0 无、1 PAP、2 CHAP
	PDPType    string `json:"pdp_type,omitempty"`
	CID        int    `json:"cid,omitempty"`
	LanIface   string `json:"lan_iface,omitempty"`
	WanIface   string `json:"wan_iface,omitempty"`
	LanIP      string `json:"lan_ip,omitempty"`
	PoolStart  string `json:"pool_start,omitempty"`
	PoolEnd    string `json:"pool_end,omitempty"`
	LeaseHours int    `json:"lease_hours,omitempty"`
	DNS1       string `json:"dns1,omitempty"`
	DNS2       string `json:"dns2,omitempty"`
}

func (s *NetSettings) applyDefaults() {
	if s.Mode == "" {
		s.Mode = NetModeRoute
		s.AutoDial = true
	}
	if s.Mode != NetModeRoute && s.Mode != NetModeBridge && s.Mode != NetModeOff {
		s.Mode = NetModeRoute
	}
	if s.PDPType == "" {
		s.PDPType = "IPV4V6"
	}
	if s.CID <= 0 {
		s.CID = 1
	}
	if s.LanIP == "" {
		s.LanIP = "192.168.8.1"
	}
	if s.PoolStart == "" {
		s.PoolStart = "192.168.8.100"
	}
	if s.PoolEnd == "" {
		s.PoolEnd = "192.168.8.200"
	}
	if s.LeaseHours <= 0 {
		s.LeaseHours = 12
	}
}

type netManager struct {
	mu  sync.Mutex
	cfg *Config
	at  *atManager

	lan, wan              string
	lanIP                 string
	wanIP, wanMask, wanGW string
	dns1, dns2            string
	natOK                 bool
	natChecked            time.Time
	dhcp                  *dhcpServer
	dhcpSig               string
	dialing               bool
	lastDial              time.Time
	failCount             int
	state                 string
	started               bool
	lastTick              time.Time
	fallbackPassthrough   bool
}

func newNetManager(cfg *Config, at *atManager) *netManager {
	return &netManager{cfg: cfg, at: at, state: "尚未初始化"}
}

func (n *netManager) run() {
	n.tick()
	t := time.NewTicker(8 * time.Second)
	defer t.Stop()
	for range t.C {
		n.tick()
	}
}

func (n *netManager) kick() {
	go n.tick()
}

func (n *netManager) settings() NetSettings {
	n.mu.Lock()
	defer n.mu.Unlock()
	s := n.cfg.Net
	return s
}

// update 在锁内修改配置，确保 tick/status 看到一致的 NetSettings。
func (n *netManager) update(fn func(s *NetSettings)) {
	n.mu.Lock()
	fn(&n.cfg.Net)
	n.mu.Unlock()
}

// tick 周期性完成：接口发现、自动拨号、LAN 配置、NAT、DHCP。
func (n *netManager) tick() {
	n.mu.Lock()
	now := time.Now()
	if !n.started {
		n.started = true
	}
	n.lastTick = now
	s := n.cfg.Net
	n.mu.Unlock()

	if s.Mode == NetModeOff {
		n.mu.Lock()
		n.stopDHCPLocked()
		n.lanIP, n.state = "", "网络功能已关闭"
		n.mu.Unlock()
		return
	}

	lan := n.pickLAN(s)
	wan := n.pickWAN(s, lan)

	n.mu.Lock()
	n.lan, n.wan = lan, wan
	if wan != "" {
		if ip, mask, ok := ifaceIPv4(wan); ok {
			n.wanIP, n.wanMask = ip, mask
		}
	}
	dialNeeded := s.AutoDial && n.wanIP == ""
	canDial := !n.dialing && (n.lastDial.IsZero() || now.Sub(n.lastDial) > n.backoff())
	n.mu.Unlock()

	if dialNeeded && canDial {
		n.dialAsync()
	}

	if s.Mode == NetModeRoute {
		n.ensureLAN(s)
		nat := n.natAvailable()
		n.mu.Lock()
		n.natOK = nat
		n.fallbackPassthrough = false
		hasWAN := n.wanIP != ""
		n.mu.Unlock()

		if hasWAN && nat {
			n.applyNAT()
			n.syncDHCP(dhcpParams{
				mode:     NetModeRoute,
				lanIP:    s.LanIP,
				poolFrom: s.PoolStart,
				poolTo:   s.PoolEnd,
				router:   s.LanIP,
				dns:      n.dnsList(s),
				lease:    time.Duration(s.LeaseHours) * time.Hour,
			})
			n.setState("路由模式：NAT 已启用")
		} else if hasWAN && !nat {
			// 内核没有 nat 表：退回透传，让 PC/CPE 直接拿到模组分配的地址
			n.mu.Lock()
			n.fallbackPassthrough = true
			n.mu.Unlock()
			n.applyPassthrough(s)
			n.setState("路由模式：内核缺少 NAT，已回退透传模式")
		} else {
			// 未联网也保留 LAN 与 DHCP，保证 192.168.8.x 能进入 WebUI
			n.syncDHCP(dhcpParams{
				mode:     NetModeRoute,
				lanIP:    s.LanIP,
				poolFrom: s.PoolStart,
				poolTo:   s.PoolEnd,
				router:   s.LanIP,
				dns:      n.dnsList(s),
				lease:    time.Duration(s.LeaseHours) * time.Hour,
			})
			n.setState("路由模式：等待拨号")
		}
		return
	}

	// 网桥 / 透传模式：FM350 的 PDP 地址直接交给 PC/CPE，无需 NAT
	if n.wanIP != "" {
		n.applyPassthrough(s)
		n.setState("网桥/透传模式：已下发运营商地址")
	} else {
		n.ensureLAN(s)
		n.syncDHCP(dhcpParams{
			mode:     NetModeRoute,
			lanIP:    s.LanIP,
			poolFrom: s.PoolStart,
			poolTo:   s.PoolEnd,
			router:   s.LanIP,
			dns:      n.dnsList(s),
			lease:    time.Duration(s.LeaseHours) * time.Hour,
		})
		n.setState("网桥/透传模式：等待拨号（已保留管理地址）")
	}
}

func (n *netManager) setState(s string) {
	n.mu.Lock()
	n.state = s
	n.mu.Unlock()
}

func (n *netManager) backoff() time.Duration {
	n.mu.Lock()
	defer n.mu.Unlock()
	d := 8 * time.Second
	for i := 0; i < n.failCount && d < 90*time.Second; i++ {
		d *= 2
	}
	return d
}

// dialAsync 在后台执行 PDP 激活，避免阻塞状态接口。
func (n *netManager) dialAsync() {
	n.mu.Lock()
	if n.dialing {
		n.mu.Unlock()
		return
	}
	n.dialing = true
	n.lastDial = time.Now()
	s := n.cfg.Net
	n.mu.Unlock()

	err := n.doDial(s)
	n.mu.Lock()
	n.dialing = false
	if err != nil {
		n.failCount++
		n.state = "拨号失败: " + err.Error()
	} else {
		n.failCount = 0
		n.state = "已自动拨号"
	}
	n.mu.Unlock()
	if err == nil {
		n.kick()
	}
}

func (n *netManager) doDial(s NetSettings) error {
	if n.at == nil {
		return errors.New("AT 通道未就绪")
	}
	cid := s.CID
	if cid <= 0 {
		cid = 1
	}
	pdp := s.PDPType
	if pdp == "" {
		pdp = "IPV4V6"
	}
	if s.APN != "" {
		_, _ = n.at.CmdOK(fmt.Sprintf("AT+CGDCONT=%d,\"%s\",\"%s\"", cid, pdp, s.APN), 5*time.Second)
	}
	if s.Auth > 0 && s.User != "" {
		_, _ = n.at.CmdOK(fmt.Sprintf("AT+CGAUTH=%d,%d,\"%s\",\"%s\"", cid, s.Auth, s.User, s.Pass), 5*time.Second)
	}
	_, _ = n.at.CmdOK("AT+CGATT=1", 20*time.Second)
	_, actErr := n.at.CmdOK(fmt.Sprintf("AT+CGACT=1,%d", cid), 30*time.Second)

	ip, mask, gw, d1, d2 := n.readPDP(cid)
	if ip == "" {
		if actErr != nil {
			return actErr
		}
		return errors.New("PDP 已激活但未读到地址")
	}
	n.mu.Lock()
	n.wanIP, n.wanMask, n.wanGW = ip, mask, gw
	if d1 != "" {
		n.dns1 = d1
	}
	if d2 != "" {
		n.dns2 = d2
	}
	n.mu.Unlock()
	logf("自动拨号成功: cid=%d ip=%s gw=%s dns=%s/%s", cid, ip, gw, n.dns1, n.dns2)
	return nil
}

// readPDP 解析 +CGCONTRDP，失败时退回 +CGPADDR。
func (n *netManager) readPDP(cid int) (ip, mask, gw, d1, d2 string) {
	r := n.at.Exchange(fmt.Sprintf("AT+CGCONTRDP=%d", cid), 3*time.Second)
	for _, l := range r.Lines {
		t := strings.TrimSpace(l)
		if !strings.HasPrefix(t, "+CGCONTRDP:") {
			continue
		}
		f := csvFields(strings.TrimPrefix(t, "+CGCONTRDP:"))
		for i := range f {
			f[i] = strings.Trim(strings.TrimSpace(f[i]), "\"")
		}
		if len(f) < 4 {
			continue
		}
		if id, err := strconv.Atoi(f[0]); err == nil && id != cid {
			continue
		}
		parts := strings.Split(f[3], ".")
		if len(parts) == 8 {
			ip = strings.Join(parts[:4], ".")
			mask = strings.Join(parts[4:], ".")
		}
		if len(f) >= 5 {
			gw = f[4]
		}
		if len(f) >= 6 {
			d1 = f[5]
		}
		if len(f) >= 7 {
			d2 = f[6]
		}
		if mask == "" && ip != "" {
			mask = "255.255.255.0"
		}
		return
	}

	r = n.at.Exchange(fmt.Sprintf("AT+CGPADDR=%d", cid), 2*time.Second)
	for _, l := range r.Lines {
		t := strings.TrimSpace(l)
		if !strings.HasPrefix(t, "+CGPADDR:") {
			continue
		}
		f := csvFields(strings.TrimPrefix(t, "+CGPADDR:"))
		if len(f) < 2 {
			continue
		}
		v := strings.Trim(strings.TrimSpace(f[1]), "\"")
		if strings.Count(v, ".") == 3 && !strings.HasPrefix(v, "0.0.0.0") {
			ip, mask = v, "255.255.255.0"
		}
	}
	return
}

func (n *netManager) dnsList(s NetSettings) []string {
	n.mu.Lock()
	defer n.mu.Unlock()
	dns := []string{}
	if s.DNS1 != "" {
		dns = append(dns, s.DNS1)
	} else if n.dns1 != "" {
		dns = append(dns, n.dns1)
	}
	if s.DNS2 != "" {
		dns = append(dns, s.DNS2)
	} else if n.dns2 != "" {
		dns = append(dns, n.dns2)
	}
	if len(dns) == 0 {
		dns = []string{"8.8.8.8", "1.1.1.1"}
	}
	return dns
}

// applyPassthrough 把 PDP 地址交给 PC/CPE（FM350 的 IP 透传方式）。
func (n *netManager) applyPassthrough(s NetSettings) {
	n.mu.Lock()
	lan, ip, mask, gw := n.lan, n.wanIP, n.wanMask, n.wanGW
	d1, d2 := n.dns1, n.dns2
	n.mu.Unlock()
	if lan == "" || ip == "" {
		return
	}
	// 透传依赖把 PC/CPE 的报文转发到蜂窝网卡；源地址就是运营商分配的地址，
	// 因此不需要 NAT，只需要开启转发并对运营商网关做 proxy ARP。
	runCmd(3*time.Second, ipPath(), "link", "set", "dev", lan, "up")
	_ = os.WriteFile("/proc/sys/net/ipv4/ip_forward", []byte("1\n"), 0644)
	_ = os.WriteFile("/proc/sys/net/ipv4/conf/"+lan+"/proxy_arp", []byte("1\n"), 0644)
	if mask == "" {
		mask = "255.255.255.0"
	}
	if gw == "" {
		gw = subnetGateway(ip)
	}
	dns := []string{}
	if s.DNS1 != "" {
		dns = append(dns, s.DNS1)
	} else if d1 != "" {
		dns = append(dns, d1)
	}
	if s.DNS2 != "" {
		dns = append(dns, s.DNS2)
	} else if d2 != "" {
		dns = append(dns, d2)
	}
	if len(dns) == 0 {
		dns = []string{"8.8.8.8", "1.1.1.1"}
	}
	n.syncDHCP(dhcpParams{
		mode:       NetModeBridge,
		lanIP:      ip,
		staticIP:   ip,
		subnetMask: mask,
		router:     gw,
		dns:        dns,
		lease:      time.Duration(s.LeaseHours) * time.Hour,
	})
}

func subnetGateway(ip string) string {
	parts := strings.Split(ip, ".")
	if len(parts) != 4 {
		return ip
	}
	parts[3] = "1"
	return strings.Join(parts, ".")
}

func (n *netManager) ensureLAN(s NetSettings) {
	n.mu.Lock()
	lan := n.lan
	n.mu.Unlock()
	if lan == "" {
		return
	}
	ip := s.LanIP
	if ip == "" {
		ip = "192.168.8.1"
	}
	runCmd(3*time.Second, ipPath(), "link", "set", "dev", lan, "up")
	cidr := ip + "/24"
	if !ifaceHasAddr(lan, cidr) {
		if out, err := runCmd(5*time.Second, ipPath(), "addr", "add", cidr, "dev", lan); err != nil {
			logf("设置 LAN 地址失败(%s %s): %v %s", lan, cidr, err, out)
		}
	}
	n.mu.Lock()
	n.lanIP = ip
	n.mu.Unlock()
	_ = os.WriteFile("/proc/sys/net/ipv4/ip_forward", []byte("1\n"), 0644)
}

func (n *netManager) natAvailable() bool {
	n.mu.Lock()
	if time.Since(n.natChecked) < 60*time.Second && !n.natChecked.IsZero() {
		v := n.natOK
		n.mu.Unlock()
		return v
	}
	n.mu.Unlock()
	out, err := runCmd(3*time.Second, iptablesPath(), "-t", "nat", "-L", "-n")
	ok := err == nil
	n.mu.Lock()
	n.natOK = ok
	n.natChecked = time.Now()
	n.mu.Unlock()
	if !ok {
		logf("内核 nat 表不可用: %v %s", err, strings.TrimSpace(out))
	}
	return ok
}

func (n *netManager) applyNAT() {
	n.mu.Lock()
	lan, wan := n.lan, n.wan
	n.mu.Unlock()
	if lan == "" || wan == "" {
		return
	}
	ipt := iptablesPath()
	addRule := func(table string, args ...string) {
		base := []string{}
		if table != "" {
			base = append(base, "-t", table)
		}
		check := append(append([]string{}, base...), append([]string{"-C"}, args...)...)
		if _, err := runCmd(3*time.Second, ipt, check...); err == nil {
			return
		}
		ins := append(append([]string{}, base...), append([]string{"-I"}, args...)...)
		if out, err := runCmd(3*time.Second, ipt, ins...); err != nil {
			logf("iptables 规则失败 %v: %v %s", args, err, out)
		}
	}
	addRule("nat", "POSTROUTING", "-o", wan, "-j", "MASQUERADE")
	addRule("", "FORWARD", "-i", lan, "-o", wan, "-j", "ACCEPT")
	addRule("", "FORWARD", "-i", wan, "-o", lan, "-m", "state", "--state", "RELATED,ESTABLISHED", "-j", "ACCEPT")
	addRule("mangle", "FORWARD", "-o", wan, "-p", "tcp", "--tcp-flags", "SYN,RST", "SYN", "-j", "TCPMSS", "--clamp-mss-to-pmtu")
}

type dhcpParams struct {
	mode       string
	lanIP      string
	staticIP   string
	subnetMask string
	poolFrom   string
	poolTo     string
	router     string
	dns        []string
	lease      time.Duration
}

func (n *netManager) syncDHCP(p dhcpParams) {
	n.mu.Lock()
	lan := n.lan
	old := n.dhcpSig
	n.dhcpSig = fmt.Sprintf("%s|%s|%s|%s|%s|%s|%s|%v|%s", lan, p.mode, p.lanIP, p.staticIP, p.poolFrom, p.poolTo, p.router, p.dns, p.lease)
	needRestart := n.dhcp == nil || old != n.dhcpSig
	if !needRestart {
		n.mu.Unlock()
		return
	}
	if n.dhcp != nil {
		n.dhcp.stop()
		n.dhcp = nil
	}
	n.mu.Unlock()

	if lan == "" {
		return
	}
	server := newDHCPServer(lan, p)
	if err := server.start(); err != nil {
		logf("DHCP 启动失败(%s): %v", lan, err)
		return
	}
	n.mu.Lock()
	n.dhcp = server
	n.mu.Unlock()
	logf("DHCP 已启动: 接口=%s 地址=%s 池=%s-%s", lan, p.lanIP, p.poolFrom, p.poolTo)
}

func (n *netManager) stopDHCPLocked() {
	if n.dhcp != nil {
		n.dhcp.stop()
		n.dhcp = nil
		n.dhcpSig = ""
	}
}

func (n *netManager) leases() []dhcpLease {
	n.mu.Lock()
	s := n.dhcp
	n.mu.Unlock()
	if s == nil {
		return nil
	}
	return s.snapshot()
}

func (n *netManager) status() map[string]any {
	n.mu.Lock()
	defer n.mu.Unlock()
	dhcpOn := n.dhcp != nil
	leases := 0
	if n.dhcp != nil {
		leases = n.dhcp.count()
	}
	return map[string]any{
		"mode":        n.cfg.Net.Mode,
		"autoDial":    n.cfg.Net.AutoDial,
		"apn":         n.cfg.Net.APN,
		"cid":         n.cfg.Net.CID,
		"lanIface":    n.lan,
		"lanIP":       n.lanIP,
		"wanIface":    n.wan,
		"wanIP":       n.wanIP,
		"wanMask":     n.wanMask,
		"gateway":     n.wanGW,
		"dns1":        n.dns1,
		"dns2":        n.dns2,
		"nat":         n.natOK,
		"passthrough": n.fallbackPassthrough,
		"dhcp":        dhcpOn,
		"leases":      leases,
		"state":       n.state,
		"dialing":     n.dialing,
		"poolStart":   n.cfg.Net.PoolStart,
		"poolEnd":     n.cfg.Net.PoolEnd,
		"leaseHours":  n.cfg.Net.LeaseHours,
	}
}

func (n *netManager) ifaceLink(name string) bool {
	if name == "" {
		return false
	}
	ifi, err := net.InterfaceByName(name)
	return err == nil && ifi.Flags&net.FlagUp != 0
}

func (n *netManager) pickLAN(s NetSettings) string {
	if s.LanIface != "" && ifaceExists(s.LanIface) {
		return s.LanIface
	}
	if v := ifaceByMAC("00:00:11:12:13:14"); v != "" {
		return v
	}
	for _, c := range []string{"eth0", "usb0", "rndis0", "br-lan", "br0"} {
		if ifaceExists(c) {
			return c
		}
	}
	return ""
}

func (n *netManager) pickWAN(s NetSettings, lan string) string {
	if s.WanIface != "" && ifaceExists(s.WanIface) {
		return s.WanIface
	}
	cands := []string{"ccmni0", "ccmni1", "ccmni2", "ccmni3", "ccmni4", "ccmni5", "wwan0", "rmnet_data0", "rmnet0", "usb0", "eth1"}
	for _, c := range cands {
		if c == lan || !ifaceExists(c) {
			continue
		}
		if _, _, ok := ifaceIPv4(c); ok {
			return c
		}
	}
	return ""
}

func ifaceExists(name string) bool {
	_, err := os.Stat("/sys/class/net/" + name)
	return err == nil
}

func ifaceByMAC(mac string) string {
	ents, err := os.ReadDir("/sys/class/net")
	if err != nil {
		return ""
	}
	for _, e := range ents {
		b, err := os.ReadFile("/sys/class/net/" + e.Name() + "/address")
		if err != nil {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(string(b)), mac) {
			return e.Name()
		}
	}
	return ""
}

func ifaceIPv4(name string) (string, string, bool) {
	ifi, err := net.InterfaceByName(name)
	if err != nil {
		return "", "", false
	}
	addrs, err := ifi.Addrs()
	if err != nil {
		return "", "", false
	}
	for _, a := range addrs {
		ipnet, ok := a.(*net.IPNet)
		if !ok {
			continue
		}
		v4 := ipnet.IP.To4()
		if v4 == nil || v4.IsLoopback() || v4.IsLinkLocalUnicast() {
			continue
		}
		mask := net.IP(ipnet.Mask).String()
		return v4.String(), mask, true
	}
	return "", "", false
}

func ifaceHasAddr(name, cidr string) bool {
	out, err := runCmd(3*time.Second, ipPath(), "-4", "addr", "show", "dev", name)
	if err != nil {
		return false
	}
	return strings.Contains(out, "inet "+cidr)
}

func ipPath() string {
	for _, p := range []string{"/sbin/ip", "/usr/sbin/ip", "/bin/ip", "/usr/bin/ip"} {
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			return p
		}
	}
	return "ip"
}

func iptablesPath() string {
	for _, p := range []string{"/usr/sbin/iptables", "/sbin/iptables", "/usr/bin/iptables"} {
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			return p
		}
	}
	return "iptables"
}

func runCmd(timeout time.Duration, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	c := exec.CommandContext(ctx, name, args...)
	b, err := c.CombinedOutput()
	return string(b), err
}
