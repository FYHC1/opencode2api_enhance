<#
  Build the host exe and stage it as opencode2api.new.exe in the install dir.
  Run "YiJianGengXinBingChongQi.cmd" afterwards to swap it in and restart.

  NOTE: messages are ASCII on purpose - PowerShell 5.1 reads .ps1 as ANSI (GBK)
  unless the file has a BOM, so CJK text in a script breaks parsing.

  Usage:
    powershell -ExecutionPolicy Bypass -File .\scripts\build-and-stage-host.ps1
    powershell -ExecutionPolicy Bypass -File .\scripts\build-and-stage-host.ps1 -SkipFrontend
#>
[CmdletBinding()]
param(
  [switch]$SkipFrontend,
  [string]$InstallDir = 'D:\Program Files\opencode2api'
)

$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent $PSScriptRoot
Set-Location $root

function Step($msg) { Write-Host "`n==> $msg" -ForegroundColor Cyan }

if (-not $SkipFrontend) {
  Step 'Build frontend (tsc + vite)'
  npm run build
  if ($LASTEXITCODE -ne 0) { throw "frontend build failed (exit=$LASTEXITCODE)" }
}
else {
  Step 'Skip frontend build, reuse existing dist'
}

Step 'Build host exe (cargo release, x86_64-pc-windows-msvc)'
& cmd.exe /c "$root\build-win.bat"
if ($LASTEXITCODE -ne 0) { throw "host build failed (exit=$LASTEXITCODE)" }

$exe = Join-Path $root 'src-tauri\target-win-direct\x86_64-pc-windows-msvc\release\opencode2api.exe'
if (-not (Test-Path $exe)) { throw "build artifact not found: $exe" }

$stage = Join-Path $InstallDir 'opencode2api.new.exe'
Step "Stage to $stage"
Copy-Item $exe $stage -Force

Write-Host ''
Write-Host 'Build finished.' -ForegroundColor Green
Write-Host ("  artifact : {0}" -f $exe)
Write-Host ("  size     : {0:N1} MB" -f ((Get-Item $exe).Length / 1MB))
Write-Host ("  staged   : {0}" -f $stage)
Write-Host ''
Write-Host 'Next: close the host, then run the one-click update+restart cmd in the install dir.' -ForegroundColor Yellow