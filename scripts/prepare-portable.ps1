# prepare-portable.ps1
# ============================================================
# One-command portable test package builder for opencode2api.
# The portable build runs on the isolated 48200 slot and its own
# data dir, so it never touches the production install (40000 slot).
#
# Usage:
#   powershell -ExecutionPolicy Bypass -File scripts\prepare-portable.ps1
#   powershell ... -OutDir "D:\tmp\oc-beta"
#   powershell ... -SkipCoreBuild          # reuse existing Go core binary
#   powershell ... -SkipRustBuild          # reuse existing release shell exe
#
# Output layout (portable contract; Go core locates frontend via
# os.Executable() -> exe-adjacent bin\dist):
#   <OutDir>/
#   |-- opencode2api.exe     # Rust shell (embeds core + sing-box; releases bin/ on run)
#   |-- WebView2Loader.dll   # required by tauri runtime (NOT auto-copied for bare exe)
#   |-- portable.txt         # empty marker -> 48200 slot + isolated data dir
#   `-- bin/
#       `-- dist/            # latest frontend build (served by core)
#
# Isolation guarantees:
#   - admin:  http://127.0.0.1:48200   (gateway 48280 / instances 48300+)
#   - data:   %APPDATA%\opencode2api-manager-test (independent from production)
#   - remove: delete <OutDir> to uninstall; data dir may be removed manually.
#
# NOTE: PowerShell 5.1 reads UTF-8 files without BOM as ANSI; keep this
#       file ASCII-only (no non-ASCII comments) to stay parsing-safe.
#       $ErrorActionPreference is NOT set to Stop: external commands
#       (cargo/go) writing to stderr would then abort the script.
# ============================================================

param(
    [string]$OutDir = "",
    [switch]$SkipCoreBuild,
    [switch]$SkipRustBuild,
    [string]$Version = "v1.3.0"
)

$Root = Split-Path -Parent $PSScriptRoot   # repo root
Set-Location $Root

if (-not $OutDir) { $OutDir = Join-Path $Root "portable-out" }
$OutDir = [System.IO.Path]::GetFullPath($OutDir)

Write-Host "== opencode2api portable packager =="
Write-Host "  repo   : $Root"
Write-Host "  output : $OutDir"

# ---- 1. Build latest Go core into bin\opencode2api.exe (embedded by Rust shell) ----
if (-not $SkipCoreBuild) {
    Write-Host "[1/4] building Go core ..."
    $commit = "unknown"
    try { $commit = (git rev-parse --short HEAD).Trim() } catch { $commit = "unknown" }
    $date = Get-Date -Format "yyyy-MM-dd"
    go build -ldflags "-X main.version=$Version -X main.commit=$commit -X main.date=$date" -o bin\opencode2api.exe .
    if ($LASTEXITCODE -ne 0) { throw "Go core build FAILED" }
    Write-Host "      core ok: $Version @ $commit"
} else {
    Write-Host "[1/4] skip Go core build (reuse bin\opencode2api.exe)"
}
if (-not (Test-Path bin\opencode2api.exe)) { throw "bin\opencode2api.exe missing" }
if (-not (Test-Path bin\sing-box.exe))     { throw "bin\sing-box.exe missing (place sing-box prebuilt)" }

# ---- 2. Build Rust shell (release; embeds latest core + sing-box) ----
if (-not $SkipRustBuild) {
    Write-Host "[2/4] building Rust shell (cargo release) ..."
    Push-Location src-tauri
    cargo build --release
    if ($LASTEXITCODE -ne 0) { Pop-Location; throw "Rust shell build FAILED" }
    Pop-Location
} else {
    Write-Host "[2/4] skip Rust shell build (reuse target\release)"
}
$releaseExe = "src-tauri\target\release\opencode2api.exe"
if (-not (Test-Path $releaseExe)) { throw "release exe missing: $releaseExe" }

# ---- 3. Assemble portable folder ----
Write-Host "[3/4] assembling portable folder ..."
if (Test-Path $OutDir) { Remove-Item -Recurse -Force $OutDir }
New-Item -ItemType Directory -Force -Path (Join-Path $OutDir "bin") | Out-Null

Copy-Item $releaseExe (Join-Path $OutDir "opencode2api.exe") -Force

$dll = "src-tauri\target\release\WebView2Loader.dll"
if (Test-Path $dll) {
    Copy-Item $dll (Join-Path $OutDir "WebView2Loader.dll") -Force
} else {
    throw "WebView2Loader.dll missing (run: npm run tauri build once, or cargo build)"
}

New-Item -ItemType File -Path (Join-Path $OutDir "portable.txt") -Force | Out-Null

if (Test-Path (Join-Path $Root "dist\index.html")) {
    Copy-Item -Recurse -Force (Join-Path $Root "dist") (Join-Path $OutDir "bin\dist")
} else {
    throw "dist\index.html missing (run: npm run build)"
}

# ---- 4. Verify ----
Write-Host "[4/4] verifying ..."
$checks = @(
    "opencode2api.exe",
    "WebView2Loader.dll",
    "portable.txt",
    "bin\dist\index.html"
)
foreach ($c in $checks) {
    if (-not (Test-Path (Join-Path $OutDir $c))) { throw "verification FAILED: $c" }
}

Write-Host ""
Write-Host "== PORTABLE PACK READY =="
Write-Host "  dir     : $OutDir"
Write-Host "  admin   : http://127.0.0.1:48200"
Write-Host "  gateway : http://127.0.0.1:48280/v1 (starts when pool has running members)"
Write-Host "  data    : $env:APPDATA\opencode2api-manager-test (isolated)"
Write-Host "  start   : double-click $OutDir\opencode2api.exe"
Write-Host "  remove  : delete $OutDir (and data dir above if desired)"