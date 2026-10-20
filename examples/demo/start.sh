#!/bin/sh
# Start the BotGuard demo: generates throwaway secrets on first run, then
# builds and starts every service. Open http://localhost:8080 afterwards.
set -eu
cd "$(dirname "$0")"

if [ ! -f .env ]; then
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
echo "gateway：  http://localhost:8090/readyz"
echo "停止：     docker compose -f $(pwd)/docker-compose.yml down（加 -v 会清空演示数据）"
