package main

import (
	"encoding/json"
	"io"
	"mime"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const webUIVersion = "WEBUI_V_02.02.28"

type server struct {
	app  *App
	www  string
	port string
}

func (s *server) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/fibo/webapi", s.webapi)
	mux.HandleFunc("/fibo/fibologin/", s.fibologin)
	mux.HandleFunc("/fiboweb/status", s.status)
	mux.HandleFunc("/", s.static)
	return mux
}

type rpcRequest struct {
	JSONRPC string         `json:"jsonrpc"`
	Method  string         `json:"method"`
	Params  map[string]any `json:"params"`
	ID      any            `json:"id"`
}

func (s *server) webapi(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		writeJSON(w, rpcError(nil, "读取请求失败"))
		return
	}
	var req rpcRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeJSON(w, rpcError(nil, "请求格式错误"))
		return
	}
	result, herr := s.app.handle(req.Method, req.Params)
	if herr != nil {
		writeJSON(w, rpcError(req.ID, herr.Error()))
		return
	}
	writeJSON(w, map[string]any{
		"jsonrpc": "2.0",
		"id":      req.ID,
		"result":  result,
	})
}

func rpcError(id any, message string) map[string]any {
	return map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"error": map[string]any{
			"code":    1,
			"message": message,
		},
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(v)
}

// fibologin 只用于首页显示“本机地址”，不做认证。
func (s *server) fibologin(w http.ResponseWriter, r *http.Request) {
	ip := ""
	if ips := localIPv4(); len(ips) > 0 {
		ip = ips[0]
	}
	writeJSON(w, map[string]any{
		"code": 0,
		"result": map[string]any{
			"ip":         ip,
			"loginState": 0,
		},
	})
}

func (s *server) status(w http.ResponseWriter, r *http.Request) {
	snap := s.app.snapshot()
	writeJSON(w, map[string]any{
		"ok":      true,
		"webui":   webUIVersion,
		"model":   snap.Model,
		"at":      s.app.at.Describe(),
		"port":    s.port,
		"ips":     localIPv4(),
		"uptime":  int64(time.Since(s.app.start).Seconds()),
		"network": boolInt(snap.PDNActive, 1, 0),
	})
}

func (s *server) static(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/")
	if strings.Contains(path, "..") {
		http.NotFound(w, r)
		return
	}
	if path == "" {
		path = "index.html"
	}
	full := filepath.Join(s.www, filepath.FromSlash(path))
	if st, err := os.Stat(full); err != nil || st.IsDir() {
		// 无扩展名的 SPA 路由回退到 index.html
		if strings.Contains(filepath.Base(path), ".") {
			http.NotFound(w, r)
			return
		}
		path = "index.html"
		full = filepath.Join(s.www, "index.html")
	}
	w.Header().Set("Cache-Control", "no-cache")
	if ct := mime.TypeByExtension(filepath.Ext(full)); ct != "" {
		w.Header().Set("Content-Type", ct)
	}
	http.ServeFile(w, r, full)
}

func localIPv4() []string {
	var out []string
	ifaces, err := net.Interfaces()
	if err != nil {
		return out
	}
	for _, i := range ifaces {
		addrs, err := i.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			ipnet, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			ip := ipnet.IP.To4()
			if ip == nil || ip.IsLoopback() {
				continue
			}
			out = append(out, ip.String())
		}
	}
	return out
}
