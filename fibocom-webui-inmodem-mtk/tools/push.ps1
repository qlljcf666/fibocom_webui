# 通过 adb 将内置 WebUI 推送到模组并安装
# 用法：
#   powershell -ExecutionPolicy Bypass -File .\tools\push.ps1
#   powershell -ExecutionPolicy Bypass -File .\tools\push.ps1 -Serial <设备序列号> -Port 80
# 环境变量 ADB 可指定 adb.exe 完整路径。
param(
    [string]$Serial = "",
    [string]$Port = "auto"
)
$ErrorActionPreference = "Stop"

$Adb = if ($env:ADB) { $env:ADB } else { "adb" }
$Here = Split-Path -Parent $MyInvocation.MyCommand.Path
$Dist = Join-Path (Split-Path -Parent $Here) "dist"

if (-not (Get-Command $Adb -ErrorAction SilentlyContinue)) {
    throw "未找到 adb，请安装 platform-tools 或设置 ADB 环境变量。"
}
if (-not (Test-Path (Join-Path $Dist "install.sh"))) {
    throw "dist 不存在，请先运行 build.ps1。"
}

$adbArgs = @()
if ($Serial) { $adbArgs = @("-s", $Serial) }

Write-Host "[1/5] 检查设备"
& $Adb @adbArgs devices
& $Adb @adbArgs root 2>$null | Out-Null
Start-Sleep -Seconds 2
& $Adb @adbArgs wait-for-device

Write-Host "[2/5] 识别架构"
$machine = (& $Adb @adbArgs shell uname -m).Trim()
switch -Wildcard ($machine) {
    "aarch64*" { $binName = "fiboweb-arm64" }
    "arm64*"   { $binName = "fiboweb-arm64" }
    "armv*"    { $binName = "fiboweb-arm" }
    "arm*"     { $binName = "fiboweb-arm" }
    default { throw "不支持的架构: $machine" }
}
Write-Host "     架构=$machine 二进制=$binName"

$remote = "/data/local/tmp/fiboweb-install"
Write-Host "[3/5] 推送文件到 $remote"
& $Adb @adbArgs shell "rm -rf $remote; mkdir -p $remote/bin $remote/www"
& $Adb @adbArgs push (Join-Path $Dist "bin\$binName") "$remote/bin/$binName" | Out-Null
& $Adb @adbArgs push (Join-Path $Dist "install.sh") "$remote/install.sh" | Out-Null
& $Adb @adbArgs push (Join-Path $Dist "uninstall.sh") "$remote/uninstall.sh" | Out-Null

# 逐文件推送 www，避免不同 adb 版本对目录推送产生多一层目录
$www = Join-Path $Dist "www"
$dirs = Get-ChildItem $www -Recurse -Directory | ForEach-Object {
    $rel = $_.FullName.Substring($www.Length).Replace('\', '/')
    "mkdir -p '$remote/www$rel'"
}
if ($dirs) { & $Adb @adbArgs shell (($dirs -join "; ")) | Out-Null }
$files = Get-ChildItem $www -Recurse -File
foreach ($f in $files) {
    $rel = $f.FullName.Substring($www.Length).Replace('\', '/')
    & $Adb @adbArgs push $f.FullName "$remote/www$rel" | Out-Null
}

Write-Host "[4/5] 执行安装（FIBOWEB_PORT=$Port）"
$uid = (& $Adb @adbArgs shell id -u).Trim()
if ($uid -ne "0") {
    & $Adb @adbArgs shell "su -c `"FIBOWEB_PORT=$Port sh $remote/install.sh`""
} else {
    & $Adb @adbArgs shell "FIBOWEB_PORT=$Port sh $remote/install.sh"
}

Write-Host "[5/5] 状态检查"
& $Adb @adbArgs shell 'for d in /data /usrdata /mnt/data /home/root /userdata /opt; do if [ -x $d/fibocom-webui/bin/fiboweb ]; then echo "安装目录: $d/fibocom-webui"; echo "端口: $(cat $d/fibocom-webui/etc/port 2>/dev/null)"; fi; done'
& $Adb @adbArgs shell 'for d in /data /usrdata /mnt/data /home/root /userdata /opt; do if [ -x $d/fibocom-webui/bin/fiboweb ]; then $d/fibocom-webui/bin/fibowebctl status; echo; fi; done'
