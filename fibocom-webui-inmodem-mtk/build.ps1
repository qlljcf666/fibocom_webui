# 构建 Fibocom 模组内置 WebUI：交叉编译 ARMv7 / ARM64 静态二进制并组装 dist
# 用法：powershell -ExecutionPolicy Bypass -File .\build.ps1
$ErrorActionPreference = "Stop"
$Here = Split-Path -Parent $MyInvocation.MyCommand.Path

$Go = (Get-Command go -ErrorAction Stop).Source
# 部分环境的 GOROOT 指向旧版 SDK，这里强制使用 PATH 中 go 对应的 GOROOT
$env:GOROOT = Split-Path (Split-Path $Go -Parent) -Parent
Write-Host "使用 Go: $Go (GOROOT=$env:GOROOT)"

$Bin = Join-Path $Here "dist\bin"
New-Item -ItemType Directory -Force -Path $Bin | Out-Null

Push-Location (Join-Path $Here "backend")
try {
    $env:CGO_ENABLED = "0"
    $env:GOOS = "linux"

    Write-Host "编译 ARMv7 (GOARM=6) ..."
    $env:GOARCH = "arm"
    $env:GOARM = "6"
    & go build -trimpath -ldflags "-s -w" -o (Join-Path $Bin "fiboweb-arm") .
    if ($LASTEXITCODE -ne 0) { throw "ARMv7 编译失败" }

    Write-Host "编译 ARM64 ..."
    $env:GOARCH = "arm64"
    Remove-Item Env:GOARM -ErrorAction SilentlyContinue
    & go build -trimpath -ldflags "-s -w" -o (Join-Path $Bin "fiboweb-arm64") .
    if ($LASTEXITCODE -ne 0) { throw "ARM64 编译失败" }
}
finally {
    Pop-Location
    Remove-Item Env:CGO_ENABLED,Env:GOOS,Env:GOARCH,Env:GOARM -ErrorAction SilentlyContinue
}

Write-Host "复制站点与安装脚本 ..."
Remove-Item (Join-Path $Here "dist\www") -Recurse -Force -ErrorAction SilentlyContinue
Copy-Item (Join-Path $Here "www") (Join-Path $Here "dist\www") -Recurse
Copy-Item (Join-Path $Here "scripts\install.sh") (Join-Path $Here "dist\install.sh") -Force
Copy-Item (Join-Path $Here "scripts\uninstall.sh") (Join-Path $Here "dist\uninstall.sh") -Force

Get-ChildItem $Bin | Format-Table Name, Length
Write-Host "构建完成：$Here\dist"
