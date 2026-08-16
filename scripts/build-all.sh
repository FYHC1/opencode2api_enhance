#!/usr/bin/env bash
set -euo pipefail

# 一键构建各端分发目录：dist/<end>/（web / docker / win / linux / mac）。
# 每个目录自包含、可独立部署到对应平台，端与端之间无相互依赖。
#   - web/    ：headless Web 服务（Go core + 前端），复制到任意机器即可跑
#   - docker/ ：容器构建文件（Dockerfile + compose），服务器 docker compose up
#   - win/    ：Windows NSIS 安装包（本机 tauri build 或 CI 产物）
#   - linux/、mac/：安装包由 CI 产出（提交信息含「CI」触发 GitHub Actions），
#     本脚本仅生成占位说明，产物从 Actions artifacts 下载后放入对应目录。
#
# 用法：bash scripts/build-all.sh

ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

VERSION="$(git -C "$ROOT" describe --tags --always --dirty 2>/dev/null || echo dev)"
OUT="$ROOT/dist"
FRONTEND="$OUT/.frontend"

echo "==> 构建前端（各端复用）"
rm -rf "$FRONTEND"
mkdir -p "$FRONTEND"
npm run build
cp -r dist/index.html dist/assets "$FRONTEND/"

# ---------- web/ ----------
echo "==> dist/web（headless Web 服务）"
mkdir -p "$OUT/web/bin"
go build -trimpath -ldflags "-s -w" -o "$OUT/web/opencode2api" .
cp -r "$FRONTEND/." "$OUT/web/dist/"
cat > "$OUT/web/README.txt" <<EOF
opencode2api Web 端（headless）v${VERSION}
启动：./opencode2api -port 40000 -password "" -listen 0.0.0.0
浏览器访问：http://<本机IP>:40000
说明：前端为无登录页的七页 UI，默认无鉴权启动（-password ""，与桌面版一致）；
公网部署请前置反向代理（nginx + TLS / Basic Auth）或设置密码（需配套前端登录页，后续支持）。
数据目录：默认 <用户配置目录>/opencode2api-manager；可用环境变量 OPCODE2API_DATA_DIR 指定
依赖：无（Go 静态编译 + 内嵌前端）；sing-box 出口子程序自动释放到 bin/（需已提供或联网下载）
EOF

# ---------- docker/ ----------
echo "==> dist/docker（容器）"
mkdir -p "$OUT/docker"
cp Dockerfile docker-compose.yml .dockerignore "$OUT/docker/"

# ---------- win/ ----------
echo "==> dist/win（NSIS 安装包）"
mkdir -p "$OUT/win"
if ls src-tauri/target/release/bundle/nsis/*.exe >/dev/null 2>&1; then
  cp src-tauri/target/release/bundle/nsis/*.exe "$OUT/win/"
else
  cat > "$OUT/win/README.txt" <<EOF
未找到本地 NSIS 安装包。
获取方式：npm run tauri:build 本机构建，或从 GitHub Actions（提交信息含 CI 触发）artifacts 下载。
EOF
fi

# ---------- linux/ mac/（CI 产物占位） ----------
for end in linux mac; do
  echo "==> dist/$end（CI 产物）"
  mkdir -p "$OUT/$end"
  cat > "$OUT/$end/README.txt" <<EOF
$end 安装包由 CI 产出：提交信息含「CI」推送到 main → GitHub Actions 三平台矩阵构建，
产物在 Actions 页面 artifacts 下载后放入本目录。
- linux：.deb / .AppImage
- mac：.dmg
EOF
done

rm -rf "$FRONTEND"
echo ""
echo "==> 完成，产物清单："
find "$OUT" -maxdepth 2 -type f | sed "s|^$ROOT/||" | sort
echo ""
echo "各端部署说明见 docs/DEPLOYMENT-MATRIX.md"