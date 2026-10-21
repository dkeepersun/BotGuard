#!/bin/sh
# Forgot the demo console password? Generates a new one for "admin", ends
# its sessions and prints it (the console asks to change it at next login).
set -eu
cd "$(dirname "$0")"
pw=$(docker compose run --rm -T seed sh -c \
  'botguard-ctl admin reset-password -username admin | sed -n "s/^password: //p" | tee /shared/admin-password')
if [ -z "$pw" ]; then
  echo "重置失败，请查看：docker compose -f $(pwd)/docker-compose.yml logs seed" >&2
  exit 1
fi
echo "管理后台 http://localhost:8091  账号 admin  新的临时密码 $pw（登录后需修改）"
