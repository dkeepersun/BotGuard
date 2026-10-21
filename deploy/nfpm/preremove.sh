#!/bin/sh
# deb 的 prerm（参数 remove / upgrade / ...）和 rpm 的 %preun（参数 0 卸载、1 升级）共用。
# 只在真正卸载时停止并删除 systemd 服务；升级时什么都不做（新包的安装后脚本会重启）。
# /etc/botguard 配置和 /var/lib/botguard 数据始终保留，彻底清除见 docs/install.md。
set -e

case "${1:-}" in
  remove | purge | 0) ;;
  *) exit 0 ;;
esac

if [ -d /run/systemd/system ] && [ -x /opt/botguard/scripts/botguard.sh ]; then
  /opt/botguard/scripts/botguard.sh uninstall-systemd || true
fi
exit 0
