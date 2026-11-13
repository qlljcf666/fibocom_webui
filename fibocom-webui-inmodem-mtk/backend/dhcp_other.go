//go:build !linux

package main

import "net"

func listenDHCP(iface string) (net.PacketConn, error) {
	return net.ListenPacket("udp4", ":67")
}
