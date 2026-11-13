//go:build linux

package main

import (
	"io"
	"os"
	"syscall"
	"unsafe"
)

// openSerialDevice 打开 AT 串口/设备，并把 tty 设置为 raw 模式。
// /dev/smd* 这类字符设备不支持 termios，ioctl 失败时直接忽略。
func openSerialDevice(path string) (io.ReadWriteCloser, error) {
	f, err := os.OpenFile(path, os.O_RDWR|syscall.O_NOCTTY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	_ = setRaw(f)
	// 清除 O_NONBLOCK，让常驻读协程可以阻塞等待数据。
	_ = syscall.SetNonblock(int(f.Fd()), false)
	return f, nil
}

func setRaw(f *os.File) error {
	var t syscall.Termios
	if _, _, errno := syscall.Syscall6(syscall.SYS_IOCTL, f.Fd(), syscall.TCGETS,
		uintptr(unsafe.Pointer(&t)), 0, 0, 0); errno != 0 {
		return errno
	}
	t.Iflag &^= syscall.IGNBRK | syscall.BRKINT | syscall.PARMRK | syscall.ISTRIP |
		syscall.INLCR | syscall.IGNCR | syscall.ICRNL | syscall.IXON
	t.Oflag &^= syscall.OPOST
	t.Lflag &^= syscall.ECHO | syscall.ECHONL | syscall.ICANON | syscall.ISIG | syscall.IEXTEN
	t.Cflag &^= syscall.CSIZE | syscall.PARENB
	t.Cflag |= syscall.CS8 | syscall.CREAD | syscall.CLOCAL
	t.Cc[syscall.VMIN] = 0
	t.Cc[syscall.VTIME] = 0
	if _, _, errno := syscall.Syscall6(syscall.SYS_IOCTL, f.Fd(), syscall.TCSETS,
		uintptr(unsafe.Pointer(&t)), 0, 0, 0); errno != 0 {
		return errno
	}
	return nil
}
