@echo off
chcp 65001 >nul
cd /d "%~dp0"

echo ============================================
echo   Fibocom 模组内置 WebUI - 一键构建
echo ============================================
echo.

powershell -NoProfile -ExecutionPolicy Bypass -File "%~dp0build.ps1"
set "RC=%ERRORLEVEL%"

echo.
if "%RC%"=="0" (
    echo 构建完成，输出目录：%~dp0dist
) else (
    echo 构建失败，错误码：%RC%
)

echo.
pause
exit /b %RC%
