@echo off
rem ================================================================
rem  EVA Station - one-click launcher (Windows)
rem  Runs the ready-made server from the project folder, so the
rem  web files in web\ are used live: edit them and reload the page.
rem  PC:     http://localhost:8080
rem  Phones: the address shown below (same Wi-Fi)  + /app/
rem ================================================================
cd /d "%~dp0"
if not exist "dist\eva-station.exe" (
  echo dist\eva-station.exe not found. Build it with:  powershell -ExecutionPolicy Bypass -File tools\build.ps1
  pause
  exit /b 1
)
start "" "http://localhost:8080"
"dist\eva-station.exe" %*
pause
