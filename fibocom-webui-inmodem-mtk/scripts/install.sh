#!/bin/sh
# Fibocom 模组内置原厂 WEBUI 安装脚本（在模组 Linux 内以 root 执行）
# 用法：
#   adb push dist /data/local/tmp/fiboweb-install
#   adb shell sh /data/local/tmp/fiboweb-install/install.sh
# 可选环境变量：
#   FIBOWEB_ROOT   安装目录（默认自动选择可写分区）
#   FIBOWEB_PORT   监听端口，默认 auto（先 80，占用则 8080）
set -eu
PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin
export PATH

SRC=$(cd "$(dirname "$0")" && pwd)

LISTEN=${FIBOWEB_PORT:-auto}
case "$LISTEN" in
    auto|"") LISTEN=auto ;;
    *:*) ;;
    *) LISTEN="0.0.0.0:$LISTEN" ;;
esac

# ---------- 选择安装目录 ----------
# FM350（MTK T700/OpenWrt）的可写持久分区是 /data（yaffs2，mount_all 挂载）。
ROOT=${FIBOWEB_ROOT:-}
if [ -z "$ROOT" ]; then
    for d in /data /usrdata /mnt/data /home/root /userdata /opt; do
        if [ -d "$d" ] && [ -w "$d" ]; then
            ROOT="$d/fibocom-webui"
            break
        fi
    done
fi
[ -n "$ROOT" ] || { echo "错误：没有找到可写分区（可用 FIBOWEB_ROOT 指定）" >&2; exit 1; }
ROOT=$(printf '%s' "$ROOT" | sed 's:/*$::')

MACHINE=$(uname -m)
case "$MACHINE" in
    aarch64|arm64) BIN=fiboweb-arm64 ;;
    armv*|arm*)    BIN=fiboweb-arm ;;
    *) echo "错误：不支持的架构 $MACHINE" >&2; exit 1 ;;
esac

SRC_BIN="$SRC/bin/$BIN"
[ -f "$SRC_BIN" ] || { echo "错误：缺少 $SRC_BIN（请按模组架构推送对应二进制）" >&2; exit 1; }

echo "== 安装 Fibocom 内置 WebUI =="
echo "架构: $MACHINE   安装目录: $ROOT   监听: $LISTEN"

mkdir -p "$ROOT/bin" "$ROOT/etc" "$ROOT/var"

# 停止旧实例
if [ -x "$ROOT/bin/fibowebctl" ]; then
    "$ROOT/bin/fibowebctl" stop >/dev/null 2>&1 || true
fi

# ---------- 安装二进制 ----------
cp -f "$SRC_BIN" "$ROOT/bin/fiboweb"
chmod 0755 "$ROOT/bin/fiboweb"

# ---------- 安装站点 ----------
rm -rf "$ROOT/www.new"
cp -R "$SRC/www" "$ROOT/www.new"
rm -rf "$ROOT/www"
mv "$ROOT/www.new" "$ROOT/www"
find "$ROOT/www" -type d -exec chmod 0755 {} \; 2>/dev/null || true
find "$ROOT/www" -type f -exec chmod 0644 {} \; 2>/dev/null || true

printf '%s\n' "$LISTEN" > "$ROOT/etc/listen"

# ---------- 控制脚本 ----------
cat > "$ROOT/bin/fibowebctl" <<'CTL'
#!/bin/sh
ROOT=$(cd "$(dirname "$0")/.." && pwd)
BIN="$ROOT/bin/fiboweb"
PIDFILE="$ROOT/var/fiboweb.pid"
LISTEN=$(cat "$ROOT/etc/listen" 2>/dev/null || echo auto)

case "$1" in
  start)
    if "$0" running; then echo "already running"; exit 0; fi
    mkdir -p "$ROOT/var"
    "$BIN" -www "$ROOT/www" -state "$ROOT/etc" -listen "$LISTEN" >>"$ROOT/var/launch.log" 2>&1 &
    echo $! > "$PIDFILE"
    ;;
  stop)
    if [ -f "$PIDFILE" ]; then
      PID=$(cat "$PIDFILE" 2>/dev/null || true)
      [ -n "$PID" ] && kill "$PID" 2>/dev/null || true
      rm -f "$PIDFILE"
    fi
    pkill -f "$BIN" >/dev/null 2>&1 || true
    ;;
  restart)
    "$0" stop
    sleep 1
    "$0" start
    ;;
  running)
    if command -v pgrep >/dev/null 2>&1; then
      pgrep -f "$BIN" >/dev/null 2>&1 && exit 0
    elif command -v pidof >/dev/null 2>&1; then
      pidof fiboweb >/dev/null 2>&1 && exit 0
    fi
    exit 1
    ;;
  port)
    cat "$ROOT/etc/port" 2>/dev/null || echo ""
    ;;
  status)
    PORT=$(cat "$ROOT/etc/port" 2>/dev/null || true)
    if [ -n "$PORT" ]; then
      if command -v curl >/dev/null 2>&1; then
        curl -s "http://127.0.0.1:$PORT/fiboweb/status"
      elif command -v wget >/dev/null 2>&1; then
        wget -qO- "http://127.0.0.1:$PORT/fiboweb/status"
      fi
    fi
    ;;
  *)
    echo "usage: $0 start|stop|restart|status|running|port" >&2
    exit 1
    ;;
esac
CTL
chmod 0755 "$ROOT/bin/fibowebctl"

# ---------- 开机自启 ----------
SERVICE_MODE=none

if [ -d /run/systemd/system ] && [ -w /etc/systemd/system ] && command -v systemctl >/dev/null 2>&1; then
    cat > /etc/systemd/system/fiboweb.service <<UNIT
[Unit]
Description=Fibocom In-Module WebUI
After=network.target
Wants=network.target

[Service]
Type=simple
ExecStart=$ROOT/bin/fiboweb -www $ROOT/www -state $ROOT/etc -listen $LISTEN
Restart=always
RestartSec=3
Nice=-5

[Install]
WantedBy=multi-user.target
UNIT
    chmod 0644 /etc/systemd/system/fiboweb.service 2>/dev/null || true
    systemctl daemon-reload >/dev/null 2>&1 || true
    systemctl enable fiboweb.service >/dev/null 2>&1 || true
    systemctl restart fiboweb.service >/dev/null 2>&1 || true
    SERVICE_MODE=systemd
elif [ -f /etc/rc.common ] && [ -d /etc/init.d ] && [ -w /etc/init.d ]; then
    if [ -x /sbin/procd ]; then
        cat > /etc/init.d/fiboweb <<INIT
#!/bin/sh /etc/rc.common
START=99
USE_PROCD=1

start_service() {
    procd_open_instance
    procd_set_param command $ROOT/bin/fiboweb -www $ROOT/www -state $ROOT/etc -listen $LISTEN
    procd_set_param respawn 3600 5 0
    procd_close_instance
}
INIT
    else
        cat > /etc/init.d/fiboweb <<INIT
#!/bin/sh /etc/rc.common
START=99

start() { $ROOT/bin/fibowebctl start >/dev/null 2>&1; }
stop()  { $ROOT/bin/fibowebctl stop  >/dev/null 2>&1; }
restart(){ $ROOT/bin/fibowebctl restart >/dev/null 2>&1; }
INIT
    fi
    chmod 0755 /etc/init.d/fiboweb
    /etc/init.d/fiboweb enable >/dev/null 2>&1 || true
    /etc/init.d/fiboweb restart >/dev/null 2>&1 || true
    SERVICE_MODE=openwrt
else
    if [ -w /etc ]; then
        RCLOCAL=/etc/rc.local
        if [ ! -f "$RCLOCAL" ]; then
            printf '#!/bin/sh\nexit 0\n' > "$RCLOCAL"
            chmod 0755 "$RCLOCAL"
        fi
        sed -i '/# >>> fibocom-webui-inmodem >>>/,/# <<< fibocom-webui-inmodem <<</d' "$RCLOCAL" 2>/dev/null || true
        {
            echo '# >>> fibocom-webui-inmodem >>>'
            echo "( sleep 8; $ROOT/bin/fibowebctl start >/dev/null 2>&1 ) &"
            echo '# <<< fibocom-webui-inmodem <<<'
        } >> "$RCLOCAL"
        SERVICE_MODE=rclocal
    else
        echo "提示：/etc 为只读，无法安装开机自启；本次仍会启动服务，重启后需重新执行安装脚本。"
        SERVICE_MODE=none
    fi
    "$ROOT/bin/fibowebctl" start >/dev/null 2>&1 || true
fi

# systemd / procd 启动失败时回退到直接后台启动
sleep 2
if ! "$ROOT/bin/fibowebctl" running; then
    echo "服务未启动，回退到后台启动…"
    "$ROOT/bin/fibowebctl" start >/dev/null 2>&1 || true
fi

# ---------- 等待端口就绪 ----------
PORT=""
i=0
while [ $i -lt 10 ]; do
    PORT=$(cat "$ROOT/etc/port" 2>/dev/null || true)
    [ -n "$PORT" ] && break
    i=$((i + 1))
    sleep 1
done
[ -n "$PORT" ] || PORT=8080

# ---------- 防火墙：放行 WebUI 与内置 DHCP ----------
# FM350 没有路由/网桥/DHCP，WebUI 需要让 PC/CPE 无论拿到 192.168.x 还是
# 透传的运营商地址都能访问，因此按网卡放行，且保留私网来源规则。
LAN_IF=${FIBOWEB_LAN_IF:-}
if [ -z "$LAN_IF" ]; then
    for i in eth0 usb0 rndis0 br-lan; do
        [ -d "/sys/class/net/$i" ] && LAN_IF=$i && break
    done
fi
IPT=/usr/sbin/iptables
[ -x "$IPT" ] || IPT=/sbin/iptables
[ -x "$IPT" ] || IPT=$(command -v iptables 2>/dev/null || true)
if [ -n "${IPT:-}" ] && [ -x "$IPT" ]; then
    for p in "$PORT" 8080; do
        for src in 192.168.0.0/16 10.0.0.0/8 172.16.0.0/12 169.254.0.0/16; do
            "$IPT" -C INPUT -s "$src" -p tcp --dport "$p" -j ACCEPT >/dev/null 2>&1 || \
            "$IPT" -I INPUT 1 -s "$src" -p tcp --dport "$p" -j ACCEPT >/dev/null 2>&1 || true
        done
        "$IPT" -C INPUT -p tcp --dport "$p" -j ACCEPT >/dev/null 2>&1 || \
        "$IPT" -I INPUT 1 -p tcp --dport "$p" -j ACCEPT >/dev/null 2>&1 || true
    done
    if [ -n "$LAN_IF" ]; then
        "$IPT" -C INPUT -i "$LAN_IF" -p udp --dport 67 -j ACCEPT >/dev/null 2>&1 || \
        "$IPT" -I INPUT 1 -i "$LAN_IF" -p udp --dport 67 -j ACCEPT >/dev/null 2>&1 || true
    fi
fi
if [ -n "$LAN_IF" ]; then
    echo "LAN 网卡: $LAN_IF"
fi

# ---------- 结果 ----------
echo "启动方式: $SERVICE_MODE   监听端口: $PORT"
if command -v curl >/dev/null 2>&1; then
    if curl -s -o /dev/null "http://127.0.0.1:$PORT/"; then
        echo "本地自检: OK"
    else
        echo "本地自检: 失败（请查看 $ROOT/var/fiboweb.log）"
    fi
elif command -v wget >/dev/null 2>&1; then
    if wget -qO- "http://127.0.0.1:$PORT/" >/dev/null 2>&1; then
        echo "本地自检: OK"
    else
        echo "本地自检: 失败（请查看 $ROOT/var/fiboweb.log）"
    fi
fi

echo ""
echo "访问地址（路由模式 / 网桥模式获得 192.168.x.x 后均可直接打开）："
IPS=$(ip -4 addr show 2>/dev/null | awk '/inet /{sub(/\/.*/,"",$2); if($2!="127.0.0.1" && $2!="") print $2}')
if [ -z "$IPS" ]; then
    IPS=$(ifconfig 2>/dev/null | awk '/inet addr:/{sub("addr:","",$2); if($2!="127.0.0.1") print $2}')
fi
for ip in $IPS; do
    echo "  http://$ip:$PORT/"
done
echo ""
echo "AT 通道与状态: $ROOT/bin/fibowebctl status"
echo "日志文件:      $ROOT/var/fiboweb.log"
if command -v getenforce >/dev/null 2>&1; then
    MODE=$(getenforce 2>/dev/null || true)
    if [ "$MODE" = "Enforcing" ]; then
        echo ""
        echo "提示：当前 SELinux 为 Enforcing。若服务未监听端口，可先执行 setenforce 0 验证是否为策略拦截。"
    fi
fi
