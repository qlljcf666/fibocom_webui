<#
WEBUI_V_02.02.10 (sdxlemur-sysfs.ubi) -> FM150AE / FM160 adb deployment script
Run in Windows PowerShell from the WEBUI_V_02.02.10_ubi directory:
    powershell -ExecutionPolicy Bypass -File .\adb_push_webui.ps1
Optional: set env var ADB to the full path of adb.exe:
    $env:ADB="C:\platform-tools\adb.exe"

Note: this bundle uses the Fibocom "webs" (GoAhead) server on ports 80/443.
If the target already runs lighttpd on those ports, stop/disable it first.
#>

$ErrorActionPreference = "Stop"
$Adb  = if ($env:ADB) { $env:ADB } else { "adb" }
$Here = Split-Path -Parent $MyInvocation.MyCommand.Path
$Root = Join-Path $Here "rootfs"

if (-not (Get-Command $Adb -ErrorAction SilentlyContinue)) {
    throw "adb not found. Install platform-tools or set the ADB environment variable."
}
if (-not (Test-Path $Root)) {
    throw "rootfs directory not found: $Root"
}

Write-Host "[1/7] Check device and request write access"
& $Adb devices
& $Adb root
Start-Sleep -Seconds 2
& $Adb remount
& $Adb shell 'mount -o remount,rw / 2>/dev/null; true'

Write-Host "[2/7] Backup existing WEBUI/server to /data"
& $Adb shell 'ts=$(date +%Y%m%d_%H%M%S); [ -d /webs ] && cp -a /webs /data/webs.bak.$ts; [ -f /usr/bin/webs ] && cp -a /usr/bin/webs /data/webs.bin.$ts; [ -f /usr/bin/libgo.so ] && cp -a /usr/bin/libgo.so /data/libgo.so.$ts; [ -f /usr/lib/libfibosocket.so.0.0.0 ] && cp -a /usr/lib/libfibosocket.so.0.0.0 /data/libfibosocket.so.0.0.0.$ts; true'

Write-Host "[3/7] Create target directories"
& $Adb shell 'mkdir -p /webs/web/css /webs/web/js /webs/web/json /webs/web/static/fonts /webs/web/static/img /usr/bin /usr/lib /etc/initscripts /lib/systemd/system /lib/systemd/system/multi-user.target.wants'

Write-Host "[4/7] Push payload files (restore original absolute paths)"
$files = Get-ChildItem -Path $Root -Recurse -File
$ok = 0
foreach ($f in $files) {
    $rel = $f.FullName.Substring($Root.Length).Replace('\', '/')
    & $Adb push $f.FullName $rel | Out-Null
    $ok++
}
Write-Host ("      pushed {0} files" -f $ok)

Write-Host "[5/7] Recreate symlinks"
& $Adb shell 'ln -sf libfibosocket.so.0.0.0 /usr/lib/libfibosocket.so.0'
& $Adb shell 'ln -sf /lib/systemd/system/webs.service /lib/systemd/system/multi-user.target.wants/webs.service'

Write-Host "[6/7] Restore permissions and owner"
& $Adb shell 'chmod -R 0755 /webs'
& $Adb shell 'chmod 0755 /usr/bin/webs /usr/bin/libgo.so /usr/lib/libfibosocket.so.0.0.0'
& $Adb shell 'chmod 0755 /etc/initscripts/start-webs /etc/initscripts/webs-deamon /etc/initscripts/webs-stop'
& $Adb shell 'chmod 0644 /lib/systemd/system/webs.service'
& $Adb shell 'chown -R root:root /webs /usr/bin/webs /usr/bin/libgo.so /usr/lib/libfibosocket.so.0.0.0 /etc/initscripts/start-webs /etc/initscripts/webs-deamon /etc/initscripts/webs-stop /lib/systemd/system/webs.service'

Write-Host "[7/7] Reload and restart webs"
& $Adb shell 'systemctl stop lighttpd 2>/dev/null; if command -v systemctl >/dev/null 2>&1; then systemctl daemon-reload; systemctl enable webs.service; systemctl restart webs.service; else /etc/initscripts/start-webs restart; fi'

Write-Host "Done. Verification:"
& $Adb shell 'echo -n "webui_version = "; cat /webs/web/webui_version; echo; echo -n "webs pid: "; pidof webs 2>/dev/null || ps | grep -i "[w]ebs" | head -3; echo "listen:"; netstat -lntp 2>/dev/null | grep -E ":80|:443"'
