#!/bin/sh
# opencode2api postinst：创建专用系统用户/数据目录，注册并启动 systemd 服务。

set -e

case "$1" in
  configure)
    # 1) 专用系统用户（不可登录；home 指向数据目录）
    if ! id -u opencode2api >/dev/null 2>&1; then
      useradd --system --home /var/lib/opencode2api --shell /usr/sbin/nologin \
        --comment "opencode2api manager service user" opencode2api
    fi

    # 2) 数据目录与配置目录（/etc/opencode2api 由 deb 的 files 装入 manager.env）
    mkdir -p /var/lib/opencode2api /etc/opencode2api
    chown -R opencode2api:opencode2api /var/lib/opencode2api
    # /etc/opencode2api/manager.env 由用户维护：opencode2api 只需读（不需要写权限）
    chown -R root:opencode2api /etc/opencode2api

    # 3) 注册并启动服务（start 容错：首次启动前用户未改 CHANGE_ME/端口也允许失败，
    #    不因端口冲突导致 dpkg 安装失败回滚）
    if command -v systemctl >/dev/null 2>&1 && [ -d /run/systemd/system ]; then
      systemctl daemon-reload
      systemctl enable opencode2api-manager-deb >/dev/null 2>&1 || true
      systemctl start opencode2api-manager-deb >/dev/null 2>&1 || true
      echo "opencode2api: 服务已注册并尝试启动（systemctl status opencode2api-manager-deb 查看状态）"
    fi

    # 4) 首次安装提示（默认密码/端口可改 /etc/opencode2api/manager.env）
    if [ -f /etc/opencode2api/manager.env ] && grep -q CHANGE_ME /etc/opencode2api/manager.env; then
      echo "opencode2api: 请修改 /etc/opencode2api/manager.env 中的 MANAGER_PASSWORD（默认密码 CHANGE_ME）"
      echo "opencode2api: 改后执行: sudo systemctl daemon-reload && sudo systemctl restart opencode2api-manager-deb"
    fi
    ;;
esac

exit 0
