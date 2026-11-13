# 将图形化管理工具打包为单文件 exe（可选）
# 需要：Python 3 + PyInstaller
# 产物：项目根目录 FibocomWebUI.exe
$ErrorActionPreference = "Stop"
$Here = Split-Path -Parent $MyInvocation.MyCommand.Path
$Root = Split-Path -Parent $Here

Set-Location $Root
$py = (Get-Command python -ErrorAction Stop).Source
& $py -m PyInstaller --noconfirm --onefile --noconsole `
    --name FibocomWebUI `
    --distpath $Root `
    --workpath (Join-Path $Here "build") `
    --specpath $Here `
    (Join-Path $Here "fiboweb_gui.py")
if ($LASTEXITCODE -ne 0) { throw "PyInstaller 打包失败" }

Write-Host "打包完成：$Root\FibocomWebUI.exe"
