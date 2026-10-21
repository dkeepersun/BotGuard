#!/bin/sh
# Forgot the demo console password? Puts "admin" back to the demo default
# (BG_DEMO_ADMIN_PASSWORD, 123456) and ends its sessions. Local demo only.
set -eu
cd "$(dirname "$0")"
pw=${BG_DEMO_ADMIN_PASSWORD:-123456}
docker compose run --rm -T -e BOTGUARD_ADMIN_PASSWORD="$pw" seed \
  botguard-ctl admin set-password -username admin -allow-weak >/dev/null 2>&1 || {
  echo "重置失败，请查看：docker compose -f $(pwd)/docker-compose.yml logs seed" >&2
  exit 1
}
echo "管理后台 admin 的密码已恢复为演示默认密码 $pw（动态码如已开启会保留）"
