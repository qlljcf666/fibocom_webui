@echo off
setlocal
cd /d "%~dp0"

echo ==========================================
echo  WEBUI_V_02.02.28  adb deployment
echo ==========================================
echo.

powershell -NoProfile -ExecutionPolicy Bypass -File "%~dp0adb_push_webui.ps1"
set "RC=%ERRORLEVEL%"

echo.
if not "%RC%"=="0" (
    echo [FAILED] exit code %RC%
) else (
    echo [OK] finished
)
echo.
pause
