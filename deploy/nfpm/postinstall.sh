#!/bin/sh
# deb 的 postinst（参数 configure [旧版本]）和 rpm 的 %post（参数 1 安装、2 升级）共用。
# 首次安装：生成 /etc/botguard 配置和密钥、创建 botguard 用户、注册并启动 systemd 服务。
# 升级：迁移库结构，重新渲染单元文件并重启。配置和数据不动。
# 环境变量（scripts/install.sh 会传入）：BOTGUARD_LISTEN、BOTGUARD_PUBLIC_ENDPOINT、BOTGUARD_NO_START=1。
set -e

BG=/opt/botguard/scripts/botguard.sh
UNIT=/etc/systemd/system/botguard-gateway.service

case "${1:-}" in
  configure) if [ -n "${2:-}" ]; then action=upgrade; else action=install; fi ;;
  1) action=install ;;
  2) action=upgrade ;;
  *) exit 0 ;;
esac

has_systemd() { [ -d /run/systemd/system ] && command -v systemctl >/dev/null 2>&1; }

if [ "$action" = install ]; then
  set -- init -y
  if [ -n "${BOTGUARD_LISTEN:-}" ]; then set -- "$@" --listen "$BOTGUARD_LISTEN"; fi
  if [ -n "${BOTGUARD_PUBLIC_ENDPOINT:-}" ]; then set -- "$@" --public-endpoint "$BOTGUARD_PUBLIC_ENDPOINT"; fi
  "$BG" "$@"
  if has_systemd; then
    if [ "${BOTGUARD_NO_START:-0}" = 1 ]; then
      "$BG" install-systemd --user botguard --no-start
    else
      # 启动失败（例如 Redis 还没装好）不让整个包安装失败，修好后 botguard restart 即可。
      "$BG" install-systemd --user botguard || echo "BotGuard 服务没有启动成功，查看日志: botguard logs gateway -n 50" >&2
    fi
  else
    echo "没有检测到运行中的 systemd，跳过服务注册；之后可执行: botguard install-systemd --user botguard"
  fi
  cat <<'EOF'

BotGuard 已安装。
  创建第一个管理员: sudo botguard ctl admin create -username admin
  查看状态:         botguard status
  配置文件:         /etc/botguard/（修改后 sudo botguard restart）
EOF
  exit 0
fi

# 升级：沿用原来的运行用户。
if has_systemd && [ -f "$UNIT" ]; then
  user="$(awk -F= '/^User=/{print $2; exit}' "$UNIT")"
  "$BG" migrate || echo "迁移失败，修复后执行: sudo botguard migrate && sudo botguard restart" >&2
  "$BG" install-systemd --user "${user:-botguard}" || echo "BotGuard 服务没有重启成功，查看日志: botguard logs gateway -n 50" >&2
fi
exit 0
