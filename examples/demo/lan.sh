#!/bin/sh
# H5 testing on a phone: serves the demo and the console over HTTPS on this
# machine's local network address. Run ./start.sh once first.
set -eu
cd "$(dirname "$0")"
[ -f .env ] || { echo "请先运行 ./start.sh 初始化演示环境" >&2; exit 1; }
LAN_HOST=${LAN_HOST:-$(ipconfig getifaddr en0 2>/dev/null || ipconfig getifaddr en1 2>/dev/null || hostname -I 2>/dev/null | awk '{print $1}')}
[ -n "$LAN_HOST" ] || { echo "没有找到局域网地址，请用 LAN_HOST=192.168.x.x ./lan.sh 指定" >&2; exit 1; }
export LAN_HOST
if [ -z "${GOPROXY:-}" ] && command -v go >/dev/null 2>&1; then
  GOPROXY=$(go env GOPROXY)
  export GOPROXY
fi
docker compose -f docker-compose.yml -f docker-compose.lan.yml up -d --build
echo
echo "手机和电脑连同一个 Wi-Fi，在手机浏览器打开："
echo "  演示站（H5）： https://$LAN_HOST:8443"
echo "  管理后台：     https://$LAN_HOST:8444"
echo "证书是本机自签的：手机提示“连接不是私密连接”时，点“显示详细信息 → 访问此网站”（Android：高级 → 继续）。"
echo "关闭局域网访问：docker compose -f $(pwd)/docker-compose.yml -f $(pwd)/docker-compose.lan.yml stop https"
