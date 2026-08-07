#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"

cd "$ROOT"
echo "Building platform-independent frontend in WSL..."
npm run build

test -f "$ROOT/dist/index.html"

WIN_ROOT="$(wslpath -w "$ROOT")"
echo "Building Windows Rust executable in Windows..."
cmd.exe /d /c "call ${WIN_ROOT}\\build-win.bat"

EXE="$ROOT/src-tauri/target-win-direct/x86_64-pc-windows-msvc/release/opencode2api.exe"
test -f "$EXE"
echo "Windows executable: $EXE"
