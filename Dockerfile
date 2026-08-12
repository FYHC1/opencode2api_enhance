# syntax=docker/dockerfile:1
# 管理器镜像：Go core（含前端 dist 静态托管）+ sing-box 出口子程序。
# 构建：docker build -t opencode2api-manager:latest .
# 运行：docker compose up -d --build（见 docker-compose.yml）

# ---------- 阶段 1：前端构建 ----------
FROM node:20-alpine AS frontend
WORKDIR /src
COPY package.json package-lock.json ./
RUN npm ci --no-audit --no-fund || npm install --no-audit --no-fund
COPY index.html vite.config.ts tsconfig.json tsconfig.app.json tsconfig.node.json ./
COPY src ./src
COPY public ./public
RUN npm run build

# ---------- 阶段 2：Go core + sing-box ----------
FROM golang:1.22-alpine AS build
WORKDIR /src
RUN apk add --no-cache wget tar
COPY go.mod ./
COPY core ./core
COPY vendors ./vendors
COPY *.go ./
COPY --from=frontend /src/dist ./dist
ARG SINGBOX_VERSION=1.13.16
RUN mkdir -p /out/bin && \
    go build -trimpath -ldflags "-s -w" -o /out/opencode2api . && \
    cp -r dist /out/dist && \
    wget -qO- --tries=5 --timeout=60 --waitretry=5 "https://github.com/SagerNet/sing-box/releases/download/v${SINGBOX_VERSION}/sing-box-${SINGBOX_VERSION}-linux-amd64.tar.gz" \
      | tar xz -C /tmp && \
    find /tmp -name sing-box -type f -exec cp {} /out/bin/sing-box \;

# ---------- 阶段 3：运行（精简） ----------
FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata && \
    addgroup -S app && adduser -S -G app -h /app app
WORKDIR /app
# 布局：管理二进制与 sing-box 出口子程序同放 /app/bin（关于页「二进制目录」可两者检出）；
# 前端 dist 放 /app/dist（cwd=/app 可被 frontendDistDir 找到）。
COPY --from=build /out/opencode2api /app/bin/opencode2api
COPY --from=build /out/bin /app/bin
COPY --from=build /out/dist /app/dist
ENV OPCODE2API_DATA_DIR=/data
EXPOSE 40000 18080
VOLUME ["/data"]
USER app
ENTRYPOINT ["/app/bin/opencode2api"]
CMD ["-port", "40000", "-listen", "0.0.0.0"]