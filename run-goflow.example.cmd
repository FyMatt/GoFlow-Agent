@echo off
setlocal

if "%GOFLOW_API_KEY%"=="" (
  echo Example usage:
  echo   set GOFLOW_API_KEY=your-deepseek-key
  echo   run-goflow.example.cmd
  exit /b 1
)

if "%GOFLOW_BASE_URL%"=="" set "GOFLOW_BASE_URL=https://api.deepseek.com/v1"
if "%GOFLOW_MODEL%"=="" set "GOFLOW_MODEL=deepseek-chat"
if "%GOFLOW_BACKUP_BASE_URL%"=="" set "GOFLOW_BACKUP_BASE_URL=%GOFLOW_BASE_URL%"
if "%GOFLOW_BACKUP_MODEL%"=="" set "GOFLOW_BACKUP_MODEL=%GOFLOW_MODEL%"
if "%GOFLOW_BACKUP_API_KEY%"=="" set "GOFLOW_BACKUP_API_KEY=%GOFLOW_API_KEY%"

go run ./cmd/goflow --workspace D:\Projects\test