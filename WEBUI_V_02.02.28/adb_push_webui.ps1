<#
WEBUI_V_02.02.28 -> FM150AE / FM160 adb deployment script
Run in Windows PowerShell from the WEBUI_V_02.02.28 directory:
    powershell -ExecutionPolicy Bypass -File .\adb_push_webui.ps1
Optional: set env var ADB to the full path of adb.exe:
    $env:ADB="C:\platform-tools\adb.exe"
The script pushes every payload file to its original absolute path, then
restores the permissions/owner recorded in the lighttpd package manifest.
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

Write-Host "[2/7] Backup existing WEBUI and config to /data"
& $Adb shell 'ts=$(date +%Y%m%d_%H%M%S); [ -d /webs/web ] && cp -a /webs/web /data/webs_web.bak.$ts; [ -f /data/lighttpd.conf ] && cp -a /data/lighttpd.conf /data/lighttpd.conf.bak.$ts; true'

Write-Host "[3/7] Create target directories"
& $Adb shell 'mkdir -p /webs/web /etc/initscripts /etc/lighttpd /etc/lighttpd.d /data /usr/sbin /lib/systemd/system /lib/systemd/system-preset'

Write-Host "[4/7] Push payload files (restore original absolute paths)"
$files = Get-ChildItem -Path $Root -Recurse -File
$ok = 0
foreach ($f in $files) {
    $rel = $f.FullName.Substring($Root.Length).Replace('\', '/')
    & $Adb push $f.FullName $rel | Out-Null
    $ok++
}
Write-Host ("      pushed {0} files" -f $ok)

Write-Host "[5/7] Restore permissions (same as package manifest)"
& $Adb shell 'chmod -R 0755 /webs/web'
& $Adb shell 'chmod 0755 /etc/initscripts/lighttpd /etc/backup_restore.sh /data/lighttpd.conf /data/firewall.fibocom /data/openssl.cnf /usr/sbin/lighttpd /usr/sbin/lighttpd-angel'
& $Adb shell 'chmod 0644 /data/backup.list /lib/systemd/system/lighttpd.service /lib/systemd/system-preset/98-lighttpd.preset'

Write-Host "[6/7] Restore owner root:root"
& $Adb shell 'chown -R root:root /webs/web /etc/initscripts/lighttpd /etc/backup_restore.sh /data/lighttpd.conf /data/firewall.fibocom /data/openssl.cnf /data/backup.list /usr/sbin/lighttpd /usr/sbin/lighttpd-angel /lib/systemd/system/lighttpd.service /lib/systemd/system-preset/98-lighttpd.preset'

Write-Host "[7/7] Reload and restart lighttpd"
& $Adb shell 'if command -v systemctl >/dev/null 2>&1; then systemctl daemon-reload; systemctl enable lighttpd.service; systemctl restart lighttpd.service; else /etc/initscripts/lighttpd restart; fi'

Write-Host "Done. Verification:"
& $Adb shell 'echo -n "webui_version = "; cat /webs/web/webui_version; echo; echo -n "lighttpd pid: "; pidof lighttpd 2>/dev/null || ps | grep -i "[l]ighttpd" | head -3'
