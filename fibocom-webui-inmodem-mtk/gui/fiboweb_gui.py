#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""Fibocom 模组内置原厂 WebUI 图形化管理工具（Windows / Python tkinter）

设计目标：把 fibocom-webui-inmodem 项目的构建、adb 安装、启停、状态、日志、
浏览器访问与卸载集中到一个窗口里，操作方式参考常见的模组 WebUI 刷入工具。

依赖：仅 Python 标准库（tkinter、subprocess、urllib、webbrowser）。
"""

import json
import os
import queue
import shutil
import socket
import subprocess
import sys
import threading
import time
import urllib.request
import webbrowser
from pathlib import Path

import tkinter as tk
from tkinter import messagebox, ttk

APP_NAME = "Fibocom 内置 WebUI 管理工具"
APP_VERSION = "1.0"
NO_WINDOW = 0x08000000 if os.name == "nt" else 0
HELP_HINT = "先选择设备，再点击“一键安装”。安装完成后可点“打开管理页”。"


def project_root() -> Path:
    if getattr(sys, "frozen", False):
        return Path(sys.executable).resolve().parent
    return Path(__file__).resolve().parent.parent


ROOT = project_root()
DIST = ROOT / "dist"
WWW = ROOT / "www"


def adb_path() -> str:
    env = os.environ.get("ADB")
    if env and Path(env).exists():
        return env
    found = shutil.which("adb")
    if found:
        return found
    for p in (r"C:\platform-tools\adb.exe", r"C:\adb\adb.exe"):
        if Path(p).exists():
            return p
    return "adb"


def ps_quote(s: str) -> str:
    return "'" + str(s).replace("'", "''") + "'"


class App(tk.Tk):
    def __init__(self):
        super().__init__()
        self.title(f"{APP_NAME} v{APP_VERSION}")
        sw, sh = self.winfo_screenwidth(), self.winfo_screenheight()
        w, h = min(1060, max(900, sw - 60)), min(730, max(520, sh - 70))
        self.geometry(f"{w}x{h}")
        self.minsize(min(880, w), min(520, h))
        self.configure(bg="#eef2f8")

        self.adb = adb_path()
        self.queue = queue.Queue()
        self.busy = False
        self.info = {}
        self.devices = {}
        self.forward_local = None
        self.forward_remote = None
        self.action_buttons = []

        self.device_var = tk.StringVar()
        self.port_var = tk.StringVar(value="自动")
        self.keep_data = tk.BooleanVar(value=False)

        self.st_device = tk.StringVar(value="未选择设备")
        self.st_arch = tk.StringVar(value="-")
        self.st_state = tk.StringVar(value="未安装")
        self.st_version = tk.StringVar(value="-")
        self.st_port = tk.StringVar(value="-")
        self.st_at = tk.StringVar(value="-")
        self.st_net = tk.StringVar(value="-")
        self.st_url = tk.StringVar(value="-")
        self.status_text = tk.StringVar(value=HELP_HINT)

        self._init_style()
        self._build_ui()
        self._bind_events()
        self.after(100, self._drain_queue)
        self.after(300, self.refresh_devices)

    # ---------------- 界面 ----------------
    def _init_style(self):
        style = ttk.Style(self)
        try:
            style.theme_use("clam")
        except tk.TclError:
            pass
        style.configure("TFrame", background="#eef2f8")
        style.configure("Card.TFrame", background="#ffffff")
        style.configure("TLabel", background="#eef2f8", foreground="#1f2937",
                        font=("Microsoft YaHei UI", 9))
        style.configure("Card.TLabel", background="#ffffff", foreground="#1f2937",
                        font=("Microsoft YaHei UI", 9))
        style.configure("Muted.TLabel", background="#ffffff", foreground="#6b7280",
                        font=("Microsoft YaHei UI", 8))
        style.configure("TButton", font=("Microsoft YaHei UI", 9), padding=(10, 6))
        style.configure("Accent.TButton", font=("Microsoft YaHei UI", 9, "bold"),
                        foreground="#ffffff", background="#0f6cbd", padding=(12, 8))
        style.map("Accent.TButton",
                  background=[("active", "#0d5aa0"), ("disabled", "#9db4cc")])
        style.configure("TCombobox", font=("Microsoft YaHei UI", 9))
        style.configure("TCheckbutton", background="#ffffff",
                        font=("Microsoft YaHei UI", 9))
        style.configure("Horizontal.TProgressbar", troughcolor="#dfe6ef",
                        background="#0f6cbd", thickness=4)

    def _build_ui(self):
        self.grid_rowconfigure(1, weight=1)
        self.grid_columnconfigure(1, weight=1)

        # 顶栏
        header = tk.Frame(self, bg="#0b1f33", height=72)
        header.grid(row=0, column=0, columnspan=2, sticky="ew")
        header.grid_propagate(False)
        tk.Label(header, text=APP_NAME, bg="#0b1f33", fg="#ffffff",
                 font=("Microsoft YaHei UI", 17, "bold")).place(x=24, y=12)
        tk.Label(header, text="原厂 WEBUI  ·  adb 安装 · 路由/网桥/DHCP 自动补全",
                 bg="#0b1f33", fg="#9fb6d1",
                 font=("Microsoft YaHei UI", 9)).place(x=26, y=44)
        tk.Label(header, text=f"v{APP_VERSION}", bg="#0b1f33", fg="#9fb6d1",
                 font=("Microsoft YaHei UI", 9)).place(relx=0.97, y=22, anchor="e")

        # 左侧操作区（可滚动，低分辨率屏幕也能访问全部按钮）
        left_holder = tk.Frame(self, bg="#eef2f8", width=392)
        left_holder.grid(row=1, column=0, sticky="nsew", padx=(16, 8), pady=14)
        left_holder.grid_propagate(False)
        left_holder.grid_rowconfigure(0, weight=1)
        left_holder.grid_columnconfigure(0, weight=1)

        left_canvas = tk.Canvas(left_holder, bg="#eef2f8", highlightthickness=0)
        left_scroll = ttk.Scrollbar(left_holder, orient="vertical", command=left_canvas.yview)
        left_canvas.configure(yscrollcommand=left_scroll.set)
        left_canvas.grid(row=0, column=0, sticky="nsew")
        left_scroll.grid(row=0, column=1, sticky="ns")

        left = tk.Frame(left_canvas, bg="#eef2f8")
        left_win = left_canvas.create_window((0, 0), window=left, anchor="nw")
        left.bind("<Configure>", lambda e: left_canvas.configure(
            scrollregion=left_canvas.bbox("all")))
        left_canvas.bind("<Configure>", lambda e: left_canvas.itemconfigure(
            left_win, width=e.width))

        def _wheel(event):
            left_canvas.yview_scroll(int(-event.delta / 120), "units")

        left_canvas.bind("<Enter>", lambda e: left_canvas.bind_all("<MouseWheel>", _wheel))
        left_canvas.bind("<Leave>", lambda e: left_canvas.unbind_all("<MouseWheel>"))

        self._section(left, "设备连接").pack(fill="x")
        dev_card = self._card(left)
        dev_card.pack(fill="x", pady=(4, 8))
        row = tk.Frame(dev_card, bg="#ffffff")
        row.pack(fill="x", padx=12, pady=10)
        self.cmb_device = ttk.Combobox(row, textvariable=self.device_var,
                                       state="readonly", height=10)
        self.cmb_device.pack(side="left", fill="x", expand=True)
        ttk.Button(row, text="刷新", width=6, command=self.refresh_devices).pack(
            side="left", padx=(8, 0))

        self._section(left, "安装设置").pack(fill="x")
        opt_card = self._card(left)
        opt_card.pack(fill="x", pady=(4, 8))
        orow = tk.Frame(opt_card, bg="#ffffff")
        orow.pack(fill="x", padx=12, pady=(10, 4))
        tk.Label(orow, text="监听端口", bg="#ffffff",
                 font=("Microsoft YaHei UI", 9)).pack(side="left")
        self.cmb_port = ttk.Combobox(orow, textvariable=self.port_var, width=10,
                                     state="readonly",
                                     values=("自动", "80", "8080", "8000"))
        self.cmb_port.pack(side="right")
        orow2 = tk.Frame(opt_card, bg="#ffffff")
        orow2.pack(fill="x", padx=12, pady=(0, 8))
        ttk.Checkbutton(orow2, text="卸载时保留配置与日志", variable=self.keep_data).pack(
            side="left")

        self._section(left, "操作").pack(fill="x")
        act = tk.Frame(left, bg="#eef2f8")
        act.pack(fill="x", pady=(6, 0))
        for c in range(3):
            act.grid_columnconfigure(c, weight=1)

        self.btn_install = tk.Button(act, text="一键安装", bg="#0f6cbd", fg="#ffffff",
                                     activebackground="#0d5aa0", activeforeground="#ffffff",
                                     relief="flat", cursor="hand2",
                                     font=("Microsoft YaHei UI", 10, "bold"),
                                     command=self.do_install)
        self.btn_install.grid(row=0, column=0, columnspan=3, sticky="ew", pady=(0, 8), ipady=3)
        self.btn_build_install = ttk.Button(act, text="重新构建并安装",
                                            command=self.do_build_install)
        self.btn_build_install.grid(row=1, column=0, columnspan=3, sticky="ew", pady=(0, 8))

        self.btn_open = ttk.Button(act, text="打开管理页", command=self.do_open)
        self.btn_open.grid(row=2, column=0, columnspan=2, sticky="ew", padx=(0, 4), pady=(0, 8))
        self.btn_status = ttk.Button(act, text="刷新状态", command=self.do_status)
        self.btn_status.grid(row=2, column=2, sticky="ew", pady=(0, 8))

        self.btn_start = ttk.Button(act, text="启动", command=lambda: self.do_control("start"))
        self.btn_start.grid(row=3, column=0, sticky="ew", padx=(0, 4), pady=(0, 8))
        self.btn_stop = ttk.Button(act, text="停止", command=lambda: self.do_control("stop"))
        self.btn_stop.grid(row=3, column=1, sticky="ew", padx=(0, 4), pady=(0, 8))
        self.btn_restart = ttk.Button(act, text="重启", command=lambda: self.do_control("restart"))
        self.btn_restart.grid(row=3, column=2, sticky="ew", pady=(0, 8))

        self.btn_log = ttk.Button(act, text="查看日志", command=self.do_log)
        self.btn_log.grid(row=4, column=0, sticky="ew", padx=(0, 4), pady=(0, 4))
        self.btn_build = ttk.Button(act, text="构建 dist", command=self.do_build)
        self.btn_build.grid(row=4, column=1, sticky="ew", padx=(0, 4), pady=(0, 4))
        self.btn_uninstall = ttk.Button(act, text="卸载", command=self.do_uninstall)
        self.btn_uninstall.grid(row=4, column=2, sticky="ew", pady=(0, 4))
        self.action_buttons = [self.btn_install, self.btn_build_install, self.btn_open,
                               self.btn_status, self.btn_start, self.btn_stop,
                               self.btn_restart, self.btn_log, self.btn_build,
                               self.btn_uninstall]

        # 右侧状态 + 日志
        right = tk.Frame(self, bg="#eef2f8")
        right.grid(row=1, column=1, sticky="nsew", padx=(8, 16), pady=14)
        right.grid_columnconfigure(0, weight=1)
        right.grid_rowconfigure(2, weight=1)

        self._section(right, "运行状态").grid(row=0, column=0, sticky="ew")
        st = self._card(right)
        st.grid(row=0, column=0, sticky="ew", pady=(6, 12))
        st.grid_columnconfigure(1, weight=1)
        st.grid_columnconfigure(3, weight=1)
        self._kv(st, 0, 0, "安装状态", self.st_state)
        self._kv(st, 0, 2, "WebUI 版本", self.st_version)
        self._kv(st, 2, 0, "监听端口", self.st_port)
        self._kv(st, 2, 2, "AT 通道", self.st_at)
        self._kv(st, 4, 0, "访问地址", self.st_url, colspan=3)
        self._kv(st, 6, 0, "模组架构", self.st_arch)
        self._kv(st, 6, 2, "网络模式", self.st_net)

        log_head = tk.Frame(right, bg="#eef2f8")
        log_head.grid(row=1, column=0, sticky="ew")
        tk.Label(log_head, text="运行日志", bg="#eef2f8", fg="#374151",
                 font=("Microsoft YaHei UI", 9, "bold")).pack(side="left")
        ttk.Button(log_head, text="清空", width=6,
                   command=self.clear_log).pack(side="right")
        ttk.Button(log_head, text="打开 dist", width=9,
                   command=self.open_dist).pack(side="right", padx=6)

        log_frame = tk.Frame(right, bg="#0f172a")
        log_frame.grid(row=2, column=0, sticky="nsew", pady=(6, 0))
        right.grid_rowconfigure(2, weight=1)
        self.txt = tk.Text(log_frame, bg="#0f172a", fg="#d7e3f4", insertbackground="#ffffff",
                           relief="flat", wrap="word", font=("Consolas", 9),
                           padx=10, pady=8, state="disabled")
        ys = ttk.Scrollbar(log_frame, orient="vertical", command=self.txt.yview)
        self.txt.configure(yscrollcommand=ys.set)
        self.txt.pack(side="left", fill="both", expand=True)
        ys.pack(side="right", fill="y")
        self.txt.tag_configure("cmd", foreground="#7dd3fc")
        self.txt.tag_configure("ok", foreground="#86efac")
        self.txt.tag_configure("warn", foreground="#fcd34d")
        self.txt.tag_configure("err", foreground="#fca5a5")
        self.txt.tag_configure("info", foreground="#cbd5e1")

        # 底栏
        footer = tk.Frame(self, bg="#eef2f8")
        footer.grid(row=2, column=0, columnspan=2, sticky="ew", padx=16, pady=(0, 10))
        footer.grid_columnconfigure(0, weight=1)
        tk.Label(footer, textvariable=self.status_text, bg="#eef2f8", fg="#4b5563",
                 font=("Microsoft YaHei UI", 9), anchor="w").grid(row=0, column=0, sticky="ew")
        self.progress = ttk.Progressbar(footer, style="Horizontal.TProgressbar",
                                        mode="indeterminate", length=220)
        self.progress.grid(row=0, column=1, sticky="e")

        # 菜单
        menubar = tk.Menu(self)
        m_file = tk.Menu(menubar, tearoff=0)
        m_file.add_command(label="打开 dist 目录", command=self.open_dist)
        m_file.add_command(label="打开项目目录", command=lambda: self._open_path(ROOT))
        m_file.add_separator()
        m_file.add_command(label="退出", command=self.on_close)
        menubar.add_cascade(label="文件", menu=m_file)
        m_tool = tk.Menu(menubar, tearoff=0)
        m_tool.add_command(label="构建 dist", command=self.do_build)
        m_tool.add_command(label="刷新设备", command=self.refresh_devices)
        m_tool.add_command(label="刷新状态", command=self.do_status)
        m_tool.add_command(label="ADB 版本", command=self.do_adb_info)
        m_net = tk.Menu(m_tool, tearoff=0)
        m_net.add_command(label="路由模式（NAT）", command=lambda: self.do_net_mode("route"))
        m_net.add_command(label="网桥/透传模式", command=lambda: self.do_net_mode("bridge"))
        m_net.add_command(label="关闭网络功能", command=lambda: self.do_net_mode("off"))
        m_net.add_separator()
        m_net.add_command(label="自动拨号 开", command=lambda: self.do_auto_dial(True))
        m_net.add_command(label="自动拨号 关", command=lambda: self.do_auto_dial(False))
        m_tool.add_cascade(label="网络模式", menu=m_net)
        menubar.add_cascade(label="工具", menu=m_tool)
        m_help = tk.Menu(menubar, tearoff=0)
        m_help.add_command(label="使用说明", command=self.show_help)
        m_help.add_command(label="关于", command=self.show_about)
        menubar.add_cascade(label="帮助", menu=m_help)
        self.configure(menu=menubar)

        self.log(f"项目目录: {ROOT}", "info")
        self.log(f"ADB: {self.adb}", "info")
        if not DIST.exists():
            self.log("dist 未构建，首次安装前请先点击“构建 dist”。", "warn")

    def _section(self, parent, text):
        lbl = tk.Label(parent, text=text, bg="#eef2f8", fg="#111827",
                       font=("Microsoft YaHei UI", 9, "bold"), anchor="w")
        return lbl

    def _card(self, parent):
        return tk.Frame(parent, bg="#ffffff", highlightbackground="#dbe3ee",
                        highlightthickness=1, bd=0)

    def _kv(self, parent, row, col, key, var, colspan=1):
        tk.Label(parent, text=key, bg="#ffffff", fg="#6b7280",
                 font=("Microsoft YaHei UI", 8), anchor="w").grid(
            row=row, column=col, sticky="w", padx=(12, 4), pady=(8 if row == 0 else 4, 2))
        tk.Label(parent, textvariable=var, bg="#ffffff", fg="#111827",
                 font=("Microsoft YaHei UI", 9), anchor="w").grid(
            row=row + 1, column=col, columnspan=colspan, sticky="w",
            padx=(12, 12), pady=(0, 6))

    def _bind_events(self):
        self.cmb_device.bind("<<ComboboxSelected>>", lambda e: self.do_status())
        self.protocol("WM_DELETE_WINDOW", self.on_close)

    # ---------------- 日志与任务 ----------------
    def log(self, text, tag="info"):
        self.queue.put(("log", text, tag))

    def set_status(self, text):
        self.queue.put(("status", text, ""))

    def set_vars(self, **kw):
        """从工作线程安全地更新界面变量。"""
        self.queue.put(("vars", kw, ""))

    def _drain_queue(self):
        try:
            while True:
                kind, text, tag = self.queue.get_nowait()
                if kind == "log":
                    now = time.strftime("%H:%M:%S")
                    self.txt.configure(state="normal")
                    self.txt.insert("end", f"[{now}] {text}\n", tag)
                    self.txt.see("end")
                    self.txt.configure(state="disabled")
                elif kind == "devices":
                    self._apply_devices(text)
                elif kind == "vars":
                    for k, v in text.items():
                        getattr(self, k).set(v)
                elif kind == "status":
                    self.status_text.set(text)
                elif kind == "done":
                    self._finish_busy(text, tag)
        except queue.Empty:
            pass
        self.after(120, self._drain_queue)

    def _finish_busy(self, ok, msg):
        self.busy = False
        for b in self.action_buttons:
            b.configure(state="normal")
        self.progress.stop()
        self.progress.configure(mode="indeterminate")
        if msg:
            self.status_text.set(msg)

    def run_task(self, title, func):
        if self.busy:
            messagebox.showinfo("请稍候", "当前操作尚未完成。")
            return
        self.busy = True
        for b in self.action_buttons:
            b.configure(state="disabled")
        self.status_text.set(title)
        self.progress.configure(mode="indeterminate")
        self.progress.start(12)
        self.log(f"== {title} ==", "cmd")

        def wrapper():
            ok, msg = True, ""
            try:
                msg = func() or ""
            except Exception as e:  # noqa: BLE001
                ok = False
                msg = str(e)
                self.log(f"失败: {e}", "err")
            finally:
                self.queue.put(("done", ok, msg))
        threading.Thread(target=wrapper, daemon=True).start()

    # ---------------- ADB ----------------
    def _serial(self):
        name = self.device_var.get()
        return self.devices.get(name)

    def _adb(self, args, timeout=40):
        cmd = [self.adb]
        serial = self._serial()
        if serial:
            cmd += ["-s", serial]
        cmd += args
        self.log("> adb " + " ".join(args), "cmd")
        try:
            p = subprocess.run(cmd, capture_output=True, text=True, encoding="utf-8",
                               errors="replace", timeout=timeout, creationflags=NO_WINDOW)
        except FileNotFoundError:
            raise RuntimeError("未找到 adb，请安装 platform-tools 或设置 ADB 环境变量")
        except subprocess.TimeoutExpired:
            raise RuntimeError("adb 命令超时")
        out = (p.stdout or "").strip()
        err = (p.stderr or "").strip()
        if out:
            self.log(out, "info")
        if err:
            self.log(err, "warn")
        return p.returncode, out, err

    def do_adb_info(self):
        def work():
            rc, out, err = self._adb(["version"], 15)
            return "ADB 版本已输出到日志" if rc == 0 else (err or "ADB 不可用")
        self.run_task("检查 ADB", work)

    def refresh_devices(self):
        def work():
            rc, out, err = self._adb(["devices", "-l"], 20)
            if rc != 0:
                raise RuntimeError(err or "adb devices 执行失败")
            self.devices = {}
            for line in out.splitlines():
                line = line.strip()
                if not line or line.startswith("List of devices") or "* daemon" in line:
                    continue
                parts = line.split()
                if len(parts) < 2:
                    continue
                serial, state = parts[0], parts[1]
                model = ""
                for t in parts[2:]:
                    if t.startswith("model:"):
                        model = t.split(":", 1)[1]
                if state == "device":
                    label = f"{serial}  |  {model or 'Fibocom 模组'}"
                elif state == "unauthorized":
                    label = f"{serial}  |  未授权（请在模组上允许调试）"
                else:
                    label = f"{serial}  |  {state}"
                self.devices[label] = serial
            names = list(self.devices.keys())
            self.queue.put(("devices", names, ""))
            if not names:
                return "未发现设备，请检查 USB 连接与 adb 授权"
            return f"发现 {len(names)} 个设备"
        self.run_task("刷新设备", work)

    def _apply_devices(self, names):
        self.cmb_device.configure(values=names)
        if self.device_var.get() not in names:
            self.device_var.set(names[0] if names else "")
        label = self.device_var.get()
        self.st_device.set(label if label else "未选择设备")

    def _ensure_device(self):
        if not self._serial():
            raise RuntimeError("请先选择设备并确保 adb 状态为 device")

    # ---------------- 项目命令 ----------------
    def _find_root(self):
        script = r'''
R=""
for d in /data /usrdata /mnt/data /home/root /userdata /opt; do
  if [ -d "$d/fibocom-webui" ]; then R="$d/fibocom-webui"; break; fi
done
echo "ROOT=$R"
echo "UNAME=$(uname -m)"
echo "UID=$(id -u)"
if [ -n "$R" ]; then
  echo "PORT=$(cat "$R/etc/port" 2>/dev/null)"
  echo "LISTEN=$(cat "$R/etc/listen" 2>/dev/null)"
  echo "WEBUI=$(cat "$R/www/webui_version" 2>/dev/null)"
  P=$(pidof fiboweb 2>/dev/null); [ -n "$P" ] || P=$(pgrep -f "$R/bin/fiboweb" 2>/dev/null)
  set -- $P
  echo "PID=$1"
  echo "CONFIG=$(cat "$R/etc/config.json" 2>/dev/null | tr -d '\n')"
fi
IPS=$(ip -4 addr show 2>/dev/null | awk '/inet /{sub(/\/.*/,"",$2); if($2!="127.0.0.1" && $2!="") printf "%s ", $2}')
echo "IPS=$IPS"
'''
        rc, out, err = self._adb(["shell", script], 30)
        if rc != 0:
            raise RuntimeError(err or "读取模组状态失败")
        info = {}
        for line in out.splitlines():
            if "=" in line:
                k, v = line.split("=", 1)
                info[k.strip()] = v.strip()
        if info.get("CONFIG"):
            try:
                cfg = json.loads(info["CONFIG"])
                info["AT"] = cfg.get("at", "")
            except Exception:
                pass
        return info

    def _forward(self, remote_port):
        if not remote_port:
            return None
        if self.forward_local and self.forward_remote == remote_port:
            return self.forward_local
        self._remove_forward()
        local = 18090
        for _ in range(60):
            try:
                s = socket.socket()
                s.bind(("127.0.0.1", local))
                s.close()
                break
            except OSError:
                local += 1
        rc, out, err = self._adb(["forward", f"tcp:{local}", f"tcp:{remote_port}"], 20)
        if rc != 0 or "error" in (out + err).lower():
            return None
        self.forward_local, self.forward_remote = local, remote_port
        return local

    def _remove_forward(self):
        if self.forward_local:
            cmd = [self.adb]
            serial = self._serial()
            if serial:
                cmd += ["-s", serial]
            cmd += ["forward", "--remove", f"tcp:{self.forward_local}"]
            subprocess.run(cmd, capture_output=True, text=True,
                           encoding="utf-8", errors="replace", creationflags=NO_WINDOW)
        self.forward_local = None
        self.forward_remote = None

    def _probe(self):
        self._ensure_device()
        info = self._find_root()
        self.info = info
        root = info.get("ROOT", "")
        installed = bool(root)
        values = {
            "st_arch": info.get("UNAME", "-"),
            "st_state": "已安装" if installed else "未安装",
            "st_version": info.get("WEBUI") or "-",
            "st_port": info.get("PORT") or "-",
            "st_at": info.get("AT") or "-",
            "st_net": "-",
        }
        if installed and info.get("PORT"):
            local = self._forward(info["PORT"])
            if local:
                try:
                    with urllib.request.urlopen(
                            f"http://127.0.0.1:{local}/fiboweb/status", timeout=3) as r:
                        st = json.loads(r.read().decode("utf-8", "replace"))
                    self.info.update(st)
                    values["st_version"] = st.get("webui", values["st_version"])
                    values["st_at"] = st.get("at", values["st_at"])
                    ips = st.get("ips") or []
                    if ips:
                        self.info["IPS"] = " ".join(ips)
                    net = st.get("net") or {}
                    mode = {"route": "路由", "bridge": "网桥/透传", "off": "关闭"}.get(
                        net.get("mode"), net.get("mode") or "")
                    state = net.get("state") or ""
                    wan = net.get("wanIP") or ""
                    values["st_net"] = " · ".join(x for x in (mode, state, wan) if x) or "-"
                except Exception:
                    pass
        ips = info.get("IPS", "")
        urls = []
        if installed and info.get("PORT"):
            for ip in ips.split():
                urls.append(f"http://{ip}:{info['PORT']}/")
            if self.forward_local:
                urls.append(f"http://127.0.0.1:{self.forward_local}/")
        values["st_url"] = "   ".join(urls) if urls else "-"
        self.set_vars(**values)
        if installed:
            return f"状态已刷新（端口 {info.get('PORT', '?')}）"
        return "模组尚未安装本项目"

    def do_status(self):
        self.run_task("刷新状态", self._probe)

    def _ps(self, script_path, extra=""):
        ps = shutil.which("powershell") or "powershell.exe"
        cmd = (f"[Console]::OutputEncoding=[Text.Encoding]::UTF8; & {ps_quote(script_path)}"
               + (f" {extra}" if extra else ""))
        self.log("> powershell " + script_path.name, "cmd")
        p = subprocess.run([ps, "-NoProfile", "-ExecutionPolicy", "Bypass", "-Command", cmd],
                           capture_output=True, text=True, encoding="utf-8",
                           errors="replace", timeout=900, creationflags=NO_WINDOW)
        out = (p.stdout or "").strip()
        err = (p.stderr or "").strip()
        if out:
            for line in out.splitlines():
                self.log(line, "info")
        if err:
            for line in err.splitlines():
                self.log(line, "warn")
        if p.returncode != 0:
            raise RuntimeError(f"脚本退出码 {p.returncode}，请查看上方日志")

    def do_build(self):
        def work():
            script = ROOT / "build.ps1"
            if not script.exists():
                raise RuntimeError("未找到 build.ps1")
            self._ps(script)
            return "构建完成，dist 已更新"
        self.run_task("构建 dist", work)

    def _install(self, rebuild=False):
        def work():
            self._ensure_device()
            if rebuild or not (DIST / "install.sh").exists():
                script = ROOT / "build.ps1"
                if not script.exists():
                    raise RuntimeError("dist 不存在且未找到 build.ps1")
                self.log("dist 不存在或需要重建，先执行构建。", "warn")
                self._ps(script)
            if not (DIST / "install.sh").exists():
                raise RuntimeError("构建后仍未找到 dist/install.sh")
            script = ROOT / "tools" / "push.ps1"
            if not script.exists():
                raise RuntimeError("未找到 tools/push.ps1")
            port = "auto" if self.port_var.get() == "自动" else self.port_var.get()
            extra = f"-Port {ps_quote(port)}"
            serial = self._serial()
            if serial:
                extra = f"-Serial {ps_quote(serial)} " + extra
            self._ps(script, extra)
            self._probe()
            return "安装完成，可点击“打开管理页”"
        self.run_task("构建并安装" if rebuild else "一键安装", work)

    def do_install(self):
        if not messagebox.askyesno(
                "开始安装",
                "将把内置 WebUI 推送到所选模组并安装。\n"
                "安装目录优先 /data/fibocom-webui（FM350），其次 /usrdata 等。\n\n是否继续？"):
            return
        self._install(False)

    def do_build_install(self):
        self._install(True)

    def do_control(self, action):
        def work():
            self._ensure_device()
            info = self._find_root()
            root = info.get("ROOT", "")
            if not root:
                raise RuntimeError("模组尚未安装本项目")
            self._remove_forward()
            rc, out, err = self._adb(
                ["shell", f"{root}/bin/fibowebctl {action}"], 60)
            if rc != 0:
                raise RuntimeError(err or f"{action} 执行失败")
            time.sleep(1.5)
            self._probe()
            return {"start": "已启动服务", "stop": "已停止服务",
                    "restart": "服务已重启"}.get(action, "操作完成")
        title = {"start": "启动服务", "stop": "停止服务", "restart": "重启服务"}.get(action, action)
        self.run_task(title, work)

    def _net_rpc(self, params, title):
        def work():
            self._ensure_device()
            info = self.info or self._find_root()
            port = info.get("PORT")
            if not port:
                raise RuntimeError("服务未运行，请先安装并启动")
            local = self._forward(port)
            if not local:
                raise RuntimeError("adb 端口转发失败")
            body = json.dumps({"jsonrpc": "2.0", "method": "SetNetworkMode",
                               "params": params, "id": 1}).encode("utf-8")
            req = urllib.request.Request(
                f"http://127.0.0.1:{local}/fibo/webapi", data=body,
                headers={"Content-Type": "application/json"})
            with urllib.request.urlopen(req, timeout=10) as r:
                res = json.loads(r.read().decode("utf-8", "replace"))
            if res.get("error"):
                raise RuntimeError(res["error"].get("message") or "设置失败")
            time.sleep(1.5)
            self._probe()
            return f"{title} 完成"
        self.run_task(title, work)

    def do_net_mode(self, mode):
        label = {"route": "路由模式（NAT）", "bridge": "网桥/透传模式",
                 "off": "关闭网络"}.get(mode, mode)
        self._net_rpc({"Mode": mode}, f"切换为{label}")

    def do_auto_dial(self, on):
        self._net_rpc({"AutoDial": bool(on)}, "开启自动拨号" if on else "关闭自动拨号")

    def do_open(self):
        def work():
            self._ensure_device()
            info = self._find_root()
            if not info.get("ROOT"):
                raise RuntimeError("模组尚未安装本项目")
            port = info.get("PORT")
            if not port:
                raise RuntimeError("未读取到服务端口，请先启动服务或刷新状态")
            local = self._forward(port)
            if not local:
                raise RuntimeError("adb 端口转发失败")
            ips = info.get("IPS", "").split()
            url = f"http://127.0.0.1:{local}/"
            self.set_vars(st_url="   ".join([url] + [f"http://{ip}:{port}/" for ip in ips]))
            webbrowser.open(url)
            return f"已通过 adb 转发打开 {url}"
        self.run_task("打开管理页", work)

    def do_log(self):
        def work():
            self._ensure_device()
            info = self._find_root()
            root = info.get("ROOT", "")
            if not root:
                raise RuntimeError("模组尚未安装本项目")
            rc, out, err = self._adb(["shell", f"tail -n 80 {root}/var/fiboweb.log"], 30)
            if rc != 0:
                raise RuntimeError(err or "读取日志失败")
            if not out:
                self.log("日志为空。", "warn")
            else:
                for line in out.splitlines():
                    self.log(line, "info")
            return "日志已输出到下方"
        self.run_task("查看日志", work)

    def do_uninstall(self):
        if not messagebox.askyesno("卸载确认",
                                   "将停止服务并移除模组上的内置 WebUI，是否继续？"):
            return

        def work():
            self._ensure_device()
            info = self._find_root()
            root = info.get("ROOT", "")
            script = DIST / "uninstall.sh"
            if not script.exists():
                raise RuntimeError("未找到 dist/uninstall.sh，请先构建")
            self._adb(["push", str(script), "/data/local/tmp/fiboweb-uninstall.sh"], 60)
            env = "KEEP_DATA=1" if self.keep_data.get() else "KEEP_DATA=0"
            if root:
                env += f" FIBOWEB_ROOT={root}"
            self._adb(["shell", f"{env} sh /data/local/tmp/fiboweb-uninstall.sh"], 90)
            self._remove_forward()
            self.info = {}
            self.set_vars(st_state="未安装", st_version="-", st_port="-",
                          st_at="-", st_url="-")
            return "卸载完成"
        self.run_task("卸载", work)

    # ---------------- 辅助 ----------------
    def clear_log(self):
        self.txt.configure(state="normal")
        self.txt.delete("1.0", "end")
        self.txt.configure(state="disabled")

    def _open_path(self, path):
        try:
            os.startfile(str(path))  # type: ignore[attr-defined]
        except Exception as e:
            messagebox.showerror("打开失败", str(e))

    def open_dist(self):
        if DIST.exists():
            self._open_path(DIST)
        else:
            messagebox.showinfo("提示", "dist 尚未生成，请先构建。")

    def show_help(self):
        doc = ROOT / "使用说明.md"
        if doc.exists():
            self._open_path(doc)
        else:
            messagebox.showinfo("使用说明",
                                "1. 选择设备\n2. 点击一键安装\n3. 点击打开管理页\n"
                                "4. 服务随模组开机自启")

    def show_about(self):
        messagebox.showinfo(
            "关于",
            f"{APP_NAME} v{APP_VERSION}\n\n"
            "用于 fibocom-webui-inmodem 项目的构建、adb 安装、启停与访问。\n"
            "支持 FM350（MTK T700/OpenWrt，ARM64）以及 FG132 / FM150 / FM160 / FM190。\n"
            "FM350 无路由/网桥/DHCP 时，由内置网络管理器自动补全：\n"
            "自动拨号、NAT 路由、IP 透传与 DHCP 地址下发。\n"
            "访问经 adb 端口转发，不依赖电脑与模组在同一网段。")

    def on_close(self):
        try:
            self._remove_forward()
        except Exception:
            pass
        self.destroy()


def main():
    selftest = "--selftest" in sys.argv
    app = App()
    if selftest:
        app.after(1500, app.destroy)
        app.mainloop()
        print("GUI selftest OK")
        return 0
    app.mainloop()
    return 0


if __name__ == "__main__":
    sys.exit(main())
