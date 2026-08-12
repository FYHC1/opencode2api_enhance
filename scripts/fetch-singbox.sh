#!/usr/bin/env bash
set -euo pipefail

# 下载 sing-box 到 bin/（供 embed.rs 按平台内嵌；bin/ 不入库，构建前需先准备：
# Windows=sing-box.exe，Linux/macOS=sing-box 无扩展名）。
# opencode2api 本仓库用 Go 构建（build-release.sh 或 CI 内 go build），无需下载。
#
# 用法：
#   ./scripts/fetch-singbox.sh                     # 拉当前宿主平台（Linux/macOS/Windows 自动识别）
#   ./scripts/fetch-singbox.sh linux-amd64         # 显式指定目标
#   SINGBOX_VERSION=1.13.16 ./scripts/fetch-singbox.sh darwin-arm64
#   ./scripts/fetch-singbox.sh --all               # 常用六平台（linux/darwin/windows × amd64/arm64）

ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
BIN="$ROOT/bin"
SINGBOX_VERSION="${SINGBOX_VERSION:-1.13.16}"

host_target() {
  case "$(uname -s)/$(uname -m)" in
    Linux/x86_64) echo "linux-amd64" ;;
    Linux/aarch64 | Linux/arm64) echo "linux-arm64" ;;
    Darwin/arm64) echo "darwin-arm64" ;;
    Darwin/x86_64) echo "darwin-amd64" ;;
    MINGW* | MSYS* | CYGWIN*) echo "windows-amd64" ;;
    *) echo "" ;;
  esac
}

targets=("$@")
if [[ ${#targets[@]} -eq 0 ]]; then
  t="$(host_target)"
  if [[ -z "$t" ]]; then
    echo "无法识别宿主平台，请显式指定目标（如 linux-amd64）" >&2
    exit 1
  fi
  targets=("$t")
fi
if [[ "${targets[0]}" == "--all" ]]; then
  targets=(linux-amd64 linux-arm64 darwin-amd64 darwin-arm64 windows-amd64 windows-arm64)
fi

mkdir -p "$BIN"

for target in "${targets[@]}"; do
  target="${target/\//-}" # 兼容 linux/amd64 写法
  url="https://github.com/SagerNet/sing-box/releases/download/v${SINGBOX_VERSION}/sing-box-${SINGBOX_VERSION}-${target}.tar.gz"
  echo ">> fetch sing-box ${SINGBOX_VERSION} ${target}"
  tmp="$(mktemp -d)"
  curl -fsSL --retry 3 "$url" -o "$tmp/sb.tgz"
  tar -xzf "$tmp/sb.tgz" -C "$tmp"
  bin_name="sing-box"
  [[ "$target" == windows-* ]] && bin_name="sing-box.exe"
  src="$(find "$tmp" -maxdepth 2 -name sing-box -type f | head -n 1)"
  if [[ -z "$src" ]]; then
    echo "!! 未在包内找到 sing-box 可执行文件（$target）" >&2
    rm -rf "$tmp"
    continue
  fi
  cp "$src" "$BIN/$bin_name"
  chmod +x "$BIN/$bin_name" 2>/dev/null || true
  rm -rf "$tmp"
  echo ">> wrote $BIN/$bin_name"
done

echo "bin/ 当前 sing-box 产物："
ls -lh "$BIN"/sing-box* 2>/dev/null || echo "（无）"