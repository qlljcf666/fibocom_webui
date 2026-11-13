//go:build linux

package main

import (
	"context"
	"net"
	"syscall"
)

// listenDHCP 在 Linux 上把 DHCP 套接字绑定到指定网卡，
// 避免干扰模组上可能存在的其它 DHCP 服务。
func listenDHCP(iface string) (net.PacketConn, error) {
	lc := net.ListenConfig{Control: func(network, address string, c syscall.RawConn) error {
		var serr error
		err := c.Control(func(fd uintptr) {
			if iface != "" {
				if e := syscall.SetsockoptString(int(fd), syscall.SOL_SOCKET, syscall.SO_BINDTODEVICE, iface); e != nil {
					serr = e
				}
			}
			_ = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_BROADCAST, 1)
			_ = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_REUSEADDR, 1)
		})
		if err != nil {
			return err
		}
		return serr
	}}
	return lc.ListenPacket(context.Background(), "udp4", ":67")
}
