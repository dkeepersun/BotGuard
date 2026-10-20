#!/bin/sh
# Idempotent demo seed: migrations, two apps (to show multi-app isolation),
# origins, actions and one secret pair per app. Credentials go to
# /shared/demo.env for the PHP site; secret keys are never printed.
set -eu

out=/shared/demo.env
botguard-ctl migrate up

field() { sed -n "s/^$1: //p"; }

# Login adapts to risk; register always shows the slider. Re-applied on every
# start so an existing demo database picks up mode changes.
set_modes() {
  botguard-ctl action set -app "$1" -action login -mode adaptive -puzzles 8 -difficulty 3000 >/dev/null
  botguard-ctl action set -app "$1" -action register -mode slider -puzzles 8 -difficulty 3000 >/dev/null
}

if [ -f "$out" ]; then
  . "$out"
  if botguard-ctl app show -app "$BG_APP_KEY" >/dev/null 2>&1; then
    set_modes "$BG_APP_KEY"
    set_modes "$BG_APP2_KEY"
    echo "演示数据已存在，已同步 action 模式"
    exit 0
  fi
fi

new_app() {
  name=$1
  app=$(botguard-ctl app create -name "$name" | field app_key)
  # The page may be opened as localhost or 127.0.0.1; origins are exact matches.
  botguard-ctl origin add -app "$app" -origin http://localhost:8080 >/dev/null
  botguard-ctl origin add -app "$app" -origin http://127.0.0.1:8080 >/dev/null
  set_modes "$app"
  echo "$app"
}

app1=$(new_app "演示商城")
pair1=$(botguard-ctl secret create -app "$app1")
app2=$(new_app "演示论坛")
pair2=$(botguard-ctl secret create -app "$app2")

umask 077
cat > "$out.tmp" <<ENV
BG_APP_KEY=$app1
BG_SECRET_ID=$(echo "$pair1" | field secret_id)
BG_SECRET_KEY=$(echo "$pair1" | field secret_key)
BG_APP2_KEY=$app2
BG_SECRET2_ID=$(echo "$pair2" | field secret_id)
BG_SECRET2_KEY=$(echo "$pair2" | field secret_key)
ENV
chmod 644 "$out.tmp"
mv "$out.tmp" "$out"
echo "已创建演示应用 $app1（演示商城）和 $app2（演示论坛）"
