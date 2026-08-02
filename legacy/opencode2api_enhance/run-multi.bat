@echo off
REM 多开启动脚本：启动 3 个 opencode2api 实例，分别使用不同代理节点
REM 端口: 8010 (node1), 8011 (node2), 8012 (node3)
REM 说明：请先确保各代理节点已在本地或远程可用

cd /d "%~dp0"

if not exist bin\opencode2api.exe (
    echo 构建二进制中，请稍候...
    go build -o bin\opencode2api.exe .
    if errorlevel 1 (
        echo 构建失败，请检查 Go 环境
        pause
        exit /b 1
    )
)

echo ==========================================
echo  启动多实例 opencode2api
echo ==========================================
echo.

REM 清理可能残留的旧进程
taskkill /im opencode2api.exe /f >nul 2>&1
timeout /t 1 /nobreak >nul

echo [1] 启动实例 1: 端口 8010, 代理 node1 (127.0.0.1:1080)
start "opencode2api-8010" /min cmd /c ".\bin\opencode2api.exe -port 8010 -config config-8010.json -password 123456"

echo [2] 启动实例 2: 端口 8011, 代理 node2 (127.0.0.1:1081)
start "opencode2api-8011" /min cmd /c ".\bin\opencode2api.exe -port 8011 -config config-8011.json -password 123456"

echo [3] 启动实例 3: 端口 8012, 代理 node3 (127.0.0.1:1082)
start "opencode2api-8012" /min cmd /c ".\bin\opencode2api.exe -port 8012 -config config-8012.json -password 123456"

echo.
echo 等待服务启动...
timeout /t 4 /nobreak >nul

echo.
echo 验证各实例健康状态:
for %%p in (8010 8011 8012) do (
    set "STATUS="
    powershell -command "$r = Invoke-RestMethod -Uri 'http://127.0.0.1:%%p/health' -UseBasicParsing -TimeoutSec 3 -ErrorAction SilentlyContinue; if ($r -eq 'OK') { 'OK' } else { 'FAIL' }" >nul 2>&1
    if "!ERRORLEVEL!"=="0" (
        echo   端口 %%p: OK
    ) else (
        echo   端口 %%p: 启动中或失败
    )
)

echo.
echo ==========================================
echo  全部实例已启动
echo  管理面板: http://127.0.0.1:8010/
echo  停止服务: 运行 stop-multi.bat
echo ==========================================
pause