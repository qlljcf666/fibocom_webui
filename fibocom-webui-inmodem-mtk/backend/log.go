package main

import (
	"fmt"
	"log"
	"os"
)

func logf(format string, args ...any) {
	log.Printf(format, args...)
}

// logToFile 在 systemd 之外的启动方式下把日志追加到 var/fiboweb.log。
func logToFile(dir string) {
	if dir == "" {
		return
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return
	}
	f, err := os.OpenFile(dir+"/fiboweb.log", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return
	}
	log.SetOutput(f)
	fmt.Fprintf(f, "\n==== fiboweb start %s ====\n", nowString())
}
