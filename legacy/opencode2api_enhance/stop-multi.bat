@echo off
REM 停止所有 opencode2api 实例

echo 正在停止所有 opencode2api 实例...
taskkill /im opencode2api.exe /f >nul 2>&1

if %errorlevel%==0 (
    echo 所有实例已停止
) else (
    echo 未找到正在运行的 opencode2api 进程
)

pause