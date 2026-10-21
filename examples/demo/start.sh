#!/bin/sh
# Start the BotGuard demo: generates throwaway secrets on first run, then
# builds and starts every service. Open http://localhost:8080 afterwards.
set -eu
cd "$(dirname "$0")"

if [ ! -f .env ]; then
  # MySQL sets the root password only when it initialises its volume, and the
  # old KEK is needed to open the stored secret pairs: new secrets cannot
  # reuse old data.
  if docker volume inspect botguard-demo_mysql-data >/dev/null 2>&1; then
    echo "错误：找不到 .env，但旧的演示数据卷还在，新生成的密码和密钥无法打开它。" >&2
    echo "请先清空旧数据：docker compose -f $(pwd)/docker-compose.yml down -v" >&2
    exit 1
  fi
  umask 077
  {
    echo "MYSQL_ROOT_PASSWORD=$(openssl rand -hex 16)"
    echo "BOTGUARD_CHALLENGE_KEY_K1=$(openssl rand -base64 32)"
    echo "BOTGUARD_SECRET_KEK=k1:$(openssl rand -base64 32)"
  } > .env
  echo "已生成 .env（仅用于本地演示的随机密钥）"
fi

if [ -z "${GOPROXY:-}" ] && command -v go >/dev/null 2>&1; then
  GOPROXY=$(go env GOPROXY)
  export GOPROXY
fi

docker compose up -d --build
echo
echo "演示页面： http://localhost:8080"
echo "管理后台： http://localhost:8091   账号 admin，初始密码 $(docker compose exec -T php cat /shared/admin-password 2>/dev/null || echo '见 docker compose logs seed')（首次登录需修改；改过后以新密码为准，忘记可运行 ./reset-admin.sh）"
echo "gateway：  http://localhost:8090/readyz"
echo "停止：     docker compose -f $(pwd)/docker-compose.yml down（加 -v 会清空演示数据）"
