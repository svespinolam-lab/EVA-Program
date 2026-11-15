@echo off
setlocal
cd /d "%~dp0"
if "%EVA_OPERATOR_TOKEN%"=="" (
  echo Set EVA_OPERATOR_TOKEN before running this script to enable operator reports and mission controls.
  set /p EVA_OPERATOR_TOKEN=Operator token: 
)
go mod tidy
if errorlevel 1 exit /b 1
go run ./server
