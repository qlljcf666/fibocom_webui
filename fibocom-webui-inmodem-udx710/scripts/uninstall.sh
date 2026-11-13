#!/bin/sh
# 卸载模组内置原厂 WEBUI
# 用法: adb shell sh /data/local/tmp/fiboweb-install/uninstall.sh
# 可选：FIBOWEB_ROOT 指定安装目录；KEEP_DATA=1 保留配置与日志
set -u

PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin
export PATH

ROOT=${FIBOWEB_ROOT:-}
if [ -z "$ROOT" ]; then
    for d in /mnt/data /home/root /usrdata /data /userdata /opt; do
        if [ -d "$d/fibocom-webui" ]; then
            ROOT="$d/fibocom-webui"
            break
        fi
    done
fi
ROOT=$(printf '%s' "${ROOT:-}" | sed 's:/*$::')

echo "== 卸载 Fibocom 内置 WebUI =="
[ -n "$ROOT" ] && echo "安装目录: $ROOT"

PORT=""
[ -n "$ROOT" ] && [ -f "$ROOT/etc/port" ] && PORT=$(cat "$ROOT/etc/port" 2>/dev/null || true)

if [ -d /run/systemd/system ] && command -v systemctl >/dev/null 2>&1; then
    systemctl stop fiboweb.service >/dev/null 2>&1 || true
    systemctl disable fiboweb.service >/dev/null 2>&1 || true
    rm -f /etc/systemd/system/fiboweb.service
    systemctl daemon-reload >/dev/null 2>&1 || true
elif [ -f /etc/init.d/fiboweb ]; then
    /etc/init.d/fiboweb stop >/dev/null 2>&1 || true
    /etc/init.d/fiboweb disable >/dev/null 2>&1 || true
    rm -f /etc/init.d/fiboweb
fi
# sysvinit：先摘掉 rc5.d 链接与脚本，再停止服务
for rcdir in /etc/rc5.d /etc/rc.d/rc5.d; do
    [ -e "$rcdir/S99fiboweb" ] && rm -f "$rcdir/S99fiboweb"
done
if [ -f /etc/init.d/S99fiboweb ]; then
    /etc/init.d/S99fiboweb stop >/dev/null 2>&1 || true
    rm -f /etc/init.d/S99fiboweb
fi

if [ -n "$ROOT" ] && [ -x "$ROOT/bin/fibowebctl" ]; then
    "$ROOT/bin/fibowebctl" stop >/dev/null 2>&1 || true
fi
pkill -f "bin/fiboweb" >/dev/null 2>&1 || true

if [ -f /etc/rc.local ]; then
    sed -i '/# >>> fibocom-webui-inmodem >>>/,/# <<< fibocom-webui-inmodem <<</d' /etc/rc.local 2>/dev/null || true
fi

if [ -n "$PORT" ]; then
    IPT=/usr/sbin/iptables
    [ -x "$IPT" ] || IPT=$(command -v iptables 2>/dev/null || true)
    if [ -n "$IPT" ]; then
        for src in 192.168.0.0/16 10.0.0.0/8 172.16.0.0/12 169.254.0.0/16; do
            "$IPT" -D INPUT -s "$src" -p tcp --dport "$PORT" -j ACCEPT >/dev/null 2>&1 || true
        done
    fi
fi

if [ -n "$ROOT" ] && [ -d "$ROOT" ]; then
    if [ "${KEEP_DATA:-0}" = "1" ]; then
        rm -rf "$ROOT/www" "$ROOT/bin"
        echo "已保留配置与日志: $ROOT/etc $ROOT/var"
    else
        rm -rf "$ROOT"
    fi
fi

echo "卸载完成。"
