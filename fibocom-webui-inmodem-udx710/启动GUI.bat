@echo off
chcp 65001 >nul
cd /d "%~dp0"

if exist "FibocomWebUI.exe" (
    start "" "FibocomWebUI.exe"
    exit /b 0
)

where pythonw.exe >nul 2>nul
if %errorlevel%==0 (
    start "" pythonw.exe "gui\fiboweb_gui.py"
    exit /b 0
)

python "gui\fiboweb_gui.py"
if errorlevel 1 pause
