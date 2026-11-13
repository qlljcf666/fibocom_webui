@echo off
title WEBUI 160WAU sdxlemur adb deploy
cd /d "%~dp0"
echo ============================================
echo  WEBUI 160WAU / sdxlemur  adb upload
echo ============================================
echo.
powershell.exe -NoProfile -ExecutionPolicy Bypass -File "%~dp0adb_push_webui.ps1"
set "RC=%ERRORLEVEL%"
echo.
if "%RC%"=="0" (
    echo [OK] Finished.
) else (
    echo [FAILED] Exit code %RC%. Check the messages above.
)
echo.
pause
