//go:build !linux

package main

import (
	"io"
	"os"
)

// openSerialDevice 在非 Linux 环境（仅用于本机冒烟测试）直接以读写方式打开。
func openSerialDevice(path string) (io.ReadWriteCloser, error) {
	return os.OpenFile(path, os.O_RDWR, 0)
}
