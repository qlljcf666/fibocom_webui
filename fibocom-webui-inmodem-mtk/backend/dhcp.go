package main

import (
	"bytes"
	"encoding/binary"
	"net"
	"sort"
	"sync"
	"time"
)

type dhcpLease struct {
	IP     string    `json:"ip"`
	MAC    string    `json:"mac"`
	Expiry time.Time `json:"expiry"`
}

type dhcpServer struct {
	mu       sync.Mutex
	iface    string
	p        dhcpParams
	serverIP net.IP
	mask     net.IPMask
	router   net.IP
	dns      []net.IP
	poolFrom net.IP
	poolTo   net.IP
	leases   map[string]dhcpLease
	conn     net.PacketConn
	stopCh   chan struct{}
	once     sync.Once
}

func newDHCPServer(iface string, p dhcpParams) *dhcpServer {
	d := &dhcpServer{iface: iface, p: p, leases: map[string]dhcpLease{}, stopCh: make(chan struct{})}
	d.serverIP = net.ParseIP(p.lanIP).To4()
	if d.serverIP == nil {
		d.serverIP = net.ParseIP("192.168.8.1").To4()
	}
	d.router = net.ParseIP(p.router)
	if d.router == nil {
		d.router = d.serverIP
	}
	mask := net.ParseIP(p.subnetMask)
	if mask == nil && p.staticIP != "" {
		mask = net.ParseIP("255.255.255.0")
	}
	if mask != nil {
		if m := mask.To4(); m != nil {
			d.mask = net.IPv4Mask(m[0], m[1], m[2], m[3])
		}
	}
	if d.mask == nil {
		d.mask = net.IPv4Mask(255, 255, 255, 0)
	}
	for _, s := range p.dns {
		if ip := net.ParseIP(s); ip != nil {
			d.dns = append(d.dns, ip.To4())
		}
	}
	if p.mode != NetModeBridge {
		d.poolFrom = net.ParseIP(p.poolFrom).To4()
		d.poolTo = net.ParseIP(p.poolTo).To4()
	}
	return d
}

func (d *dhcpServer) start() error {
	conn, err := listenDHCP(d.iface)
	if err != nil {
		return err
	}
	d.conn = conn
	go d.serve()
	return nil
}

func (d *dhcpServer) stop() {
	d.once.Do(func() {
		close(d.stopCh)
		if d.conn != nil {
			_ = d.conn.Close()
		}
	})
}

func (d *dhcpServer) count() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.leases)
}

func (d *dhcpServer) snapshot() []dhcpLease {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]dhcpLease, 0, len(d.leases))
	for _, l := range d.leases {
		out = append(out, l)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].IP < out[j].IP })
	return out
}

func (d *dhcpServer) serve() {
	buf := make([]byte, 1500)
	for {
		select {
		case <-d.stopCh:
			return
		default:
		}
		_ = d.conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		n, addr, err := d.conn.ReadFrom(buf)
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				continue
			}
			return
		}
		d.handle(buf[:n], addr)
	}
}

func (d *dhcpServer) handle(pkt []byte, from net.Addr) {
	if len(pkt) < 244 || binary.BigEndian.Uint32(pkt[236:240]) != 0x63825363 {
		return
	}
	if pkt[0] != 1 { // BOOTREQUEST
		return
	}
	mac := net.HardwareAddr(pkt[28:34]).String()
	msgType := byte(0)
	var requested net.IP
	i := 240
	for i+1 < len(pkt) {
		code := pkt[i]
		if code == 255 {
			break
		}
		if code == 0 {
			i++
			continue
		}
		l := int(pkt[i+1])
		if i+2+l > len(pkt) {
			break
		}
		opt := pkt[i+2 : i+2+l]
		switch code {
		case 53:
			if l >= 1 {
				msgType = opt[0]
			}
		case 50:
			if l == 4 {
				requested = net.IP(opt).To4()
			}
		}
		i += 2 + l
	}

	switch msgType {
	case 1, 3: // DISCOVER / REQUEST
	default:
		return
	}

	yip := d.allocate(mac, requested)
	if yip == nil {
		return
	}
	respType := byte(2) // OFFER
	if msgType == 3 {
		respType = 5 // ACK
	}

	resp := make([]byte, 240)
	copy(resp, pkt[:240])
	resp[0] = 2
	copy(resp[16:20], yip.To4())
	resp[10], resp[11] = 0, 0
	resp[236], resp[237], resp[238], resp[239] = 0x63, 0x82, 0x53, 0x63

	var opts []byte
	addOpt := func(code byte, val []byte) {
		opts = append(opts, code, byte(len(val)))
		opts = append(opts, val...)
	}
	addOpt(53, []byte{respType})
	addOpt(54, d.serverIP.To4())
	if m := maskBytes(d.mask); m != nil {
		addOpt(1, m)
	}
	if r := d.router.To4(); r != nil {
		addOpt(3, r)
	}
	if len(d.dns) > 0 {
		var b []byte
		for _, ip := range d.dns {
			b = append(b, ip.To4()...)
		}
		addOpt(6, b)
	}
	seconds := int64(d.p.lease.Seconds())
	if seconds <= 0 {
		seconds = 12 * 3600
	}
	v := make([]byte, 4)
	binary.BigEndian.PutUint32(v, uint32(seconds))
	addOpt(51, v)
	binary.BigEndian.PutUint32(v, uint32(seconds/2))
	addOpt(58, v)
	binary.BigEndian.PutUint32(v, uint32(seconds*7/8))
	addOpt(59, v)
	opts = append(opts, 255)

	out := append(resp, opts...)
	dst := &net.UDPAddr{IP: net.IPv4bcast, Port: 68}
	if !net.IP(pkt[24:28]).IsUnspecified() {
		dst = &net.UDPAddr{IP: net.IP(pkt[24:28]), Port: 67}
	}
	_, _ = d.conn.WriteTo(out, dst)
}

func maskBytes(m net.IPMask) []byte {
	if m == nil {
		return nil
	}
	b := make([]byte, 4)
	copy(b, m)
	if bytes.Equal(b, []byte{0, 0, 0, 0}) {
		return nil
	}
	return b
}

func (d *dhcpServer) allocate(mac string, requested net.IP) net.IP {
	d.mu.Lock()
	defer d.mu.Unlock()
	now := time.Now()
	for k, l := range d.leases {
		if now.After(l.Expiry) {
			delete(d.leases, k)
		}
	}
	lease := time.Duration(d.p.lease)
	if lease <= 0 {
		lease = 12 * time.Hour
	}
	if d.p.mode == NetModeBridge {
		ip := net.ParseIP(d.p.staticIP).To4()
		if ip == nil {
			return nil
		}
		d.leases[mac] = dhcpLease{IP: ip.String(), MAC: mac, Expiry: now.Add(lease)}
		return ip
	}
	if l, ok := d.leases[mac]; ok {
		if ip := net.ParseIP(l.IP).To4(); ip != nil {
			l.Expiry = now.Add(lease)
			d.leases[mac] = l
			return ip
		}
	}
	if requested != nil && d.inPool(requested) && !d.usedByOther(mac, requested) {
		d.leases[mac] = dhcpLease{IP: requested.String(), MAC: mac, Expiry: now.Add(lease)}
		return requested
	}
	if d.poolFrom == nil || d.poolTo == nil {
		return nil
	}
	cur := append(net.IP{}, d.poolFrom...)
	for {
		if !d.usedByOther(mac, cur) {
			d.leases[mac] = dhcpLease{IP: cur.String(), MAC: mac, Expiry: now.Add(lease)}
			return cur
		}
		if bytes.Equal(cur, d.poolTo) || len(cur) != 4 {
			break
		}
		cur[3]++
		if cur[3] == 0 {
			cur[2]++
			if cur[2] == 0 {
				break
			}
		}
	}
	return nil
}

func (d *dhcpServer) inPool(ip net.IP) bool {
	if d.poolFrom == nil || d.poolTo == nil {
		return false
	}
	return bytes.Compare(ip.To4(), d.poolFrom) >= 0 && bytes.Compare(ip.To4(), d.poolTo) <= 0
}

func (d *dhcpServer) usedByOther(mac string, ip net.IP) bool {
	for m, l := range d.leases {
		if m != mac && l.IP == ip.String() {
			return true
		}
	}
	return false
}
