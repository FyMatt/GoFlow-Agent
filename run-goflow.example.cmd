@echo off
setlocal
chcp 65001 >nul

if "%GOFLOW_BASE_URL%"=="" set "GOFLOW_BASE_URL=https://api.deepseek.com/v1"
if "%GOFLOW_MODEL%"=="" set "GOFLOW_MODEL=deepseek-chat"
if "%GOFLOW_BACKUP_BASE_URL%"=="" set "GOFLOW_BACKUP_BASE_URL=%GOFLOW_BASE_URL%"
if "%GOFLOW_BACKUP_MODEL%"=="" set "GOFLOW_BACKUP_MODEL=%GOFLOW_MODEL%"
if "%GOFLOW_BACKUP_API_KEY%"=="" set "GOFLOW_BACKUP_API_KEY=%GOFLOW_API_KEY%"

if "%~1"=="" (
  set "WORKSPACE=%CD%\workspace"
) else (
  set "WORKSPACE=%~1"
)

echo Starting GoFlow Web Studio at http://127.0.0.1:8080/console
if "%GOFLOW_API_KEY%"=="" echo No GOFLOW_API_KEY set. You can configure providers from Web Studio settings after startup.
go run ./cmd/goflow --workspace "%WORKSPACE%" --http :8080
