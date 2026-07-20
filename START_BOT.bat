@echo off
chcp 65001 >nul
set LOG_DIR=logs
if not exist "%LOG_DIR%" mkdir "%LOG_DIR%"

:: دریافت تاریخ و زمان
for /f "tokens=2 delims==" %%I in ('wmic os get localdatetime /value') do set datetime=%%I
set DATE_TIME=%datetime:~0,4%-%datetime:~4,2%-%datetime:~6,2%_%datetime:~8,2%-%datetime:~10,2%-%datetime:~12,2%
set LOG_FILE=%LOG_DIR%\scraper-sync_%DATE_TIME%.log

echo ================================================
echo  🚀 Scraper-Sync Robot v3.0
echo ================================================
echo  Log file: %LOG_FILE%
echo.

:: اجرا با PowerShell برای استفاده از Tee-Object
powershell -NoProfile -Command "go run -mod=vendor cmd/app/main.go 2>&1 | Tee-Object -FilePath '%LOG_FILE%'"

echo.
echo ================================================
echo  ✅ Robot stopped.
echo ================================================
pause