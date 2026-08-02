#!/usr/bin/env bash
# 组装便携 zip：release exe（内嵌子程序）+ 使用说明
# 用法: bash scripts/make-portable.sh [version]
set -euo pipefail
cd "$(dirname "$0")/.."
VERSION="${1:-0.1.0}"
RELEASE="src-tauri/target/release/opencode2api.exe"
[ -f "$RELEASE" ] || { echo "未找到 $RELEASE，请先执行: npm run tauri:build -- --no-bundle"; exit 1; }
mkdir -p dist
STAGE="dist/portable"
rm -rf "$STAGE" 2>/dev/null || true
mkdir -p "$STAGE"
cp "$RELEASE" "$STAGE/opencode2api-manager.exe"
cp portable/README.txt "$STAGE/README.txt"
# 子程序已内嵌于 exe，运行时自动释放；zip 只需主程序 + 说明
(cd "$STAGE" && zip -r "../opencode2api-manager-${VERSION}-portable.zip" . -x ".*" >/dev/null)
rm -rf "$STAGE"
echo "完成: dist/opencode2api-manager-${VERSION}-portable.zip"