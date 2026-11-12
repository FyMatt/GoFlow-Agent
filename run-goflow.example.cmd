@echo off
setlocal
chcp 65001 >nul

if "%GOFLOW_DATA_ROOT%"=="" set "GOFLOW_DATA_ROOT=%LOCALAPPDATA%\GoFlow\data"

if "%~1"=="" (
  set "WORKSPACE=%CD%\workspace"
) else (
  set "WORKSPACE=%~1"
)

echo Starting GoFlow Web Studio at http://127.0.0.1:8080/console
if "%GOFLOW_API_KEY%"=="" echo No GOFLOW_API_KEY set. You can configure providers from Web Studio settings after startup.
go run ./cmd/goflow --workspace "%WORKSPACE%" --http :8080
