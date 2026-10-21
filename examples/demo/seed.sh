#!/bin/sh
# Idempotent demo seed: migrations, two apps (to show multi-app isolation),
# origins, actions and one secret pair per app. Credentials go to
# /shared/demo.env for the PHP site; secret keys are never printed.
set -eu

out=/shared/demo.env
botguard-ctl migrate up

field() { sed -n "s/^$1: //p"; }

# One action per challenge style: login adapts to risk across every kind;
# register, post, pay and comment always show the slider, text clicks, shape
# clicks and the rotate disc.
# Re-applied on every start so an existing demo database picks up changes.
set_modes() {
  botguard-ctl action set -app "$1" -action login -mode adaptive -kinds slider,rotate,jigsaw,click_text,click_order,click_icon,click_shape,spatial -puzzles 8 -difficulty 3000 >/dev/null
  botguard-ctl action set -app "$1" -action register -mode interactive -kinds slider -puzzles 8 -difficulty 3000 >/dev/null
  botguard-ctl action set -app "$1" -action post -mode interactive -kinds click_text -puzzles 8 -difficulty 3000 >/dev/null
  botguard-ctl action set -app "$1" -action pay -mode interactive -kinds click_shape -puzzles 8 -difficulty 3000 >/dev/null
  botguard-ctl action set -app "$1" -action comment -mode interactive -kinds rotate -puzzles 8 -difficulty 3000 >/dev/null
  botguard-ctl action set -app "$1" -action share -mode interactive -kinds jigsaw -puzzles 8 -difficulty 3000 >/dev/null
  botguard-ctl action set -app "$1" -action checkin -mode interactive -kinds click_order -puzzles 8 -difficulty 3000 >/dev/null
  botguard-ctl action set -app "$1" -action coupon -mode interactive -kinds click_icon -puzzles 8 -difficulty 3000 >/dev/null
  botguard-ctl action set -app "$1" -action vote -mode interactive -kinds spatial -puzzles 8 -difficulty 3000 >/dev/null
}

# The console's first owner. For this local demo its password is a fixed,
# weak default (BG_DEMO_ADMIN_PASSWORD, 123456) so testing is easy; never do
# this for a console reachable by others.
if ! botguard-ctl admin list 2>/dev/null | grep -q '^admin '; then
  botguard-ctl admin create -username admin -name 演示管理员 -role owner >/dev/null
  BOTGUARD_ADMIN_PASSWORD=${BG_DEMO_ADMIN_PASSWORD:-123456} botguard-ctl admin set-password -username admin -allow-weak >/dev/null 2>&1
  echo "已创建管理后台账号 admin（演示默认密码）"
fi

# lan.sh: also allow the HTTPS origin a phone uses on the local network.
allow_extra_origin() {
  if [ -n "${BG_EXTRA_ORIGIN:-}" ]; then
    botguard-ctl origin add -app "$1" -origin "$BG_EXTRA_ORIGIN" >/dev/null 2>&1 || true
  fi
}

if [ -f "$out" ]; then
  . "$out"
  # Recreate only when the app is really gone (e.g. the database was reset);
  # any other failure must stop here instead of creating duplicate apps.
  if err=$(botguard-ctl app show -app "$BG_APP_KEY" 2>&1 >/dev/null); then
    set_modes "$BG_APP_KEY"
    set_modes "$BG_APP2_KEY"
    allow_extra_origin "$BG_APP_KEY"
    allow_extra_origin "$BG_APP2_KEY"
    echo "演示数据已存在，已同步 action 模式"
    exit 0
  fi
  case "$err" in
    *未找到*) echo "演示应用已不在数据库中，重新创建" ;;
    *) echo "$err" >&2; exit 1 ;;
  esac
fi

new_app() {
  name=$1
  # A pipeline hides the ctl exit status from set -e; check the result.
  app=$(botguard-ctl app create -name "$name" | field app_key)
  if [ -z "$app" ]; then
    echo "创建应用 $name 失败" >&2
    exit 1
  fi
  # The page may be opened as localhost or 127.0.0.1; origins are exact matches.
  botguard-ctl origin add -app "$app" -origin http://localhost:8080 >/dev/null
  botguard-ctl origin add -app "$app" -origin http://127.0.0.1:8080 >/dev/null
  set_modes "$app"
  allow_extra_origin "$app"
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
