package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"
)

func nowString() string {
	return time.Now().Format("2006-01-02 15:04:05")
}

func main() {
	var (
		wwwDir   = flag.String("www", "/usrdata/fibocom-webui/www", "Web 站点目录")
		stateDir = flag.String("state", "/usrdata/fibocom-webui/etc", "配置与状态目录")
		listen   = flag.String("listen", "auto", "监听地址；auto = 先 80 后 8080/8000")
		atSpec   = flag.String("at", "", "AT 设备路径，留空自动探测（也可用 FIBOWEB_AT）")
		selfTest = flag.Bool("selftest", false, "探测 AT 通道与型号后退出")
	)
	flag.Parse()

	_ = os.MkdirAll(*stateDir, 0755)
	_ = os.MkdirAll(filepath.Join(*stateDir, "..", "var"), 0755)
	if !*selfTest {
		logToFile(filepath.Join(*stateDir, "..", "var"))
	}
	logf("Fibocom 模组内置 WebUI %s", webUIVersion)

	if _, err := os.Stat(filepath.Join(*wwwDir, "index.html")); err != nil {
		logf("站点目录不可用: %v", err)
	}

	cfg := loadConfig(*stateDir)
	if *atSpec != "" {
		cfg.AT = *atSpec
	}
	at := newATManager(cfg)

	if *selfTest {
		at.startupDetect()
		snap := map[string]any{"at": at.Current(), "ok": at.Current() != ""}
		if at.Current() != "" {
			r := at.Exchange("AT+CGMM", 2*time.Second)
			snap["model"] = r.First()
			r = at.Exchange("AT+CGMI", 2*time.Second)
			snap["manufacturer"] = r.First()
		} else {
			snap["error"] = "未找到可用的 AT 通道"
		}
		b, _ := json.Marshal(snap)
		fmt.Println(string(b))
		if !snap["ok"].(bool) {
			os.Exit(1)
		}
		return
	}

	app := newApp(at, cfg)
	go at.startupDetect()
	go app.pollLoop()
	// FM350 缺失的自动拨号 / 路由 / 网桥 / DHCP 由内置网络管理器补全。
	go app.net.run()

	ln, addr, err := listenOn(*listen, *stateDir)
	if err != nil {
		logf("监听失败: %v", err)
		os.Exit(1)
	}
	port := "0"
	if ap, ok := ln.Addr().(*net.TCPAddr); ok {
		port = fmt.Sprintf("%d", ap.Port)
	}
	srv := &server{app: app, www: *wwwDir, port: port}
	httpSrv := &http.Server{
		Handler:           srv.handler(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	logf("已监听 %s，站点目录 %s", addr, *wwwDir)
	for _, ip := range localIPv4() {
		logf("访问地址: http://%s:%s/", ip, port)
	}
	_ = os.WriteFile(filepath.Join(*stateDir, "port"), []byte(port), 0644)

	go func() {
		if err := httpSrv.Serve(ln); err != nil && err != http.ErrServerClosed {
			logf("HTTP 服务退出: %v", err)
		}
	}()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = httpSrv.Shutdown(ctx)
	logf("已退出")
}

// listenOn 自动在 80 → 8080 → 8000 之间选择可用端口，避免与模组自带的
// 原厂 Web 服务（80/443）冲突；路由模式和网桥模式下都绑定 0.0.0.0。
func listenOn(spec, stateDir string) (net.Listener, string, error) {
	if spec != "" && spec != "auto" {
		ln, err := net.Listen("tcp", spec)
		return ln, spec, err
	}
	var lastErr error
	for _, a := range []string{"0.0.0.0:80", "0.0.0.0:8080", "0.0.0.0:8000", "0.0.0.0:8088"} {
		ln, err := net.Listen("tcp", a)
		if err == nil {
			return ln, a, nil
		}
		lastErr = err
		logf("端口 %s 不可用: %v", a, err)
	}
	return nil, "", lastErr
}
