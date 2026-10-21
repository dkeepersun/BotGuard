#!/usr/bin/env bash
# BotGuard 服务管理：编译、启停、迁移、日志，以及安装 systemd 服务（Linux）或 launchd 服务（macOS）。
# 装了服务后，start/stop/restart/status/logs 都转给 systemctl/journalctl 或 launchctl，
# 不会和它们拉起的进程重复启动；没装时用 nohup 在后台运行，日志写 logs/。
# 要兼容 macOS 自带的 bash 3.2：不用关联数组、mapfile 等 bash 4 特性，也不用 GNU 专有参数。
# 同一个脚本既用于源码目录（build/rebuild/update），也随 GitHub Release 发布包分发（upgrade）。
# 说明见 docs/deploy.md。
set -euo pipefail

# 安装脚本会把 botguard 命令链接到这里，先解开链接再定位程序目录（旧版 macOS 的 readlink 没有 -f）。
resolve_path() {
  local p="$1" dir
  while [[ -L "$p" ]]; do
    dir="$(cd "$(dirname "$p")" && pwd)"
    p="$(readlink "$p")"
    [[ "$p" == /* ]] || p="$dir/$p"
  done
  echo "$p"
}
SCRIPT_PATH="$(resolve_path "${BASH_SOURCE[0]}")"
SCRIPT_DIR="$(cd "$(dirname "$SCRIPT_PATH")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
BIN_DIR="$ROOT_DIR/bin"
LOG_DIR="$ROOT_DIR/logs"
TEMPLATE_FILE="$ROOT_DIR/deploy/systemd/botguard.service.tmpl"
UNIT_DIR=/etc/systemd/system
RELEASE_BASE="${BOTGUARD_RELEASE_BASE:-https://github.com/dkeepersun/BotGuard/releases}"
OS="$(uname -s)"
# init 生成的配置和 SQLite 数据目录。Linux 是系统级安装；macOS 按当前用户安装，
# 都放在程序目录里，服务注册为 launchd 的 LaunchAgent（登录后自动运行）。
if [[ "$OS" == Darwin ]]; then
  ETC_DIR="$ROOT_DIR/etc"
  DATA_DIR="$ROOT_DIR/data"
else
  ETC_DIR=/etc/botguard
  DATA_DIR=/var/lib/botguard
fi
AGENT_DIR="$HOME/Library/LaunchAgents"
PLIST_TEMPLATE="$ROOT_DIR/deploy/launchd/botguard.plist.tmpl"

# 有 go.mod 是源码目录；发布包里只有编译好的二进制和 VERSION 文件。
if [[ -f "$ROOT_DIR/go.mod" ]]; then
  FROM_SOURCE=1
else
  FROM_SOURCE=0
fi

# gateway 先启动、后停止：admin-web 会检查 gateway 的 /readyz。
ALL_SERVICES="gateway admin-web"

# 用 init 生成的配置（Linux 的 /etc/botguard，macOS 的程序目录 etc/）；没有时是本机开发，
# 用仓库里的 configs/ 和 .env。
if [[ -z "${BOTGUARD_CONF_DIR:-}" ]]; then
  if [[ -f "$ETC_DIR/gateway.yaml" ]]; then
    BOTGUARD_CONF_DIR="$ETC_DIR"
  else
    BOTGUARD_CONF_DIR="$ROOT_DIR/configs"
  fi
fi
if [[ -z "${BOTGUARD_ENV_FILE:-}" ]]; then
  if [[ -e "$ETC_DIR/botguard.env" ]]; then
    BOTGUARD_ENV_FILE="$ETC_DIR/botguard.env"
  else
    BOTGUARD_ENV_FILE="$ROOT_DIR/.env"
  fi
fi

# macOS 按当前用户安装，不需要 sudo。
SUDO=""
if [[ "$OS" != Darwin && "$(id -u)" -ne 0 ]] && command -v sudo >/dev/null 2>&1; then
  SUDO="sudo"
fi

usage() {
  cat <<USAGE
用法: scripts/botguard.sh <命令> [服务]

服务: gateway | admin-web | all（默认 all）

命令:
  start [服务]          启动；二进制不存在时先编译
  stop [服务]           停止
  restart [服务]        重启，不重新编译
  rebuild [服务]        源码目录：编译 → 迁移库结构 → 重启
  update [服务]         源码目录：git pull --ff-only 后执行 rebuild
  build [服务]          源码目录：只编译到 bin/（同时编译 botguard-ctl）
  upgrade [版本]        发布包：下载 GitHub Release（默认最新版）→ 校验 → 替换 → 迁移 → 重启
  migrate               执行 botguard-ctl migrate up
  ctl <参数...>          加载环境变量后执行 botguard-ctl，例如 ctl admin create -username admin
                        装了 systemd 服务时以服务的运行用户执行（SQLite 文件属于它）
  status [服务]         运行状态、PID 和 /readyz
  logs <服务> [-n 行数] [-f]
  run <服务>            在前台运行（launchd 服务用它启动，也可用于调试）
  init [-y] [--user 用户] [--public-endpoint 地址] [--listen 地址]
                        生成配置：$ETC_DIR 下的配置和密钥、$DATA_DIR 数据目录；
                        Linux 上还会创建运行用户。已有的文件不覆盖，只改参数指定的项
  install-systemd [--user 用户] [--group 组] [--no-start]
                        Linux：安装 systemd 服务并设为开机自启，默认装完立即（重新）启动
  uninstall-systemd     停止、禁用并删除 systemd 服务（不删二进制、配置和日志）
  install-launchd [--no-start]
                        macOS：安装 launchd 服务（当前用户登录后自动运行），默认立即（重新）启动
  uninstall-launchd     停止并删除 launchd 服务（不删二进制、配置和日志）

当前配置:
  配置目录  $BOTGUARD_CONF_DIR
  环境变量  $BOTGUARD_ENV_FILE

环境变量:
  BOTGUARD_CONF_DIR       gateway.yaml / admin-web.yaml 所在目录
  BOTGUARD_ENV_FILE       密钥与 DSN 文件（KEY='value' 格式，不写 export）
  BOTGUARD_SKIP_MIGRATE=1 rebuild/update/upgrade 时跳过迁移
  BOTGUARD_RELEASE_BASE   upgrade 的下载地址，默认 $RELEASE_BASE
  GO_BUILD_FLAGS          额外的 go build 参数
USAGE
}

die() {
  echo "错误: $*" >&2
  exit 1
}

# ---------- 公共 ----------

parse_services() {
  case "${1:-all}" in
    all) echo "$ALL_SERVICES" ;;
    gateway | admin-web) echo "$1" ;;
    *) die "未知服务: $1（可选 gateway、admin-web、all）" ;;
  esac
}

reverse_words() {
  local out="" w
  for w in $1; do out="$w $out"; done
  echo "$out"
}

unit_name() {
  echo "botguard-$1"
}

uses_systemd() {
  command -v systemctl >/dev/null 2>&1 && [[ -f "$UNIT_DIR/$(unit_name "$1").service" ]]
}

# ---------- launchd（macOS） ----------

launchd_label() { echo "com.botguard.$1"; }
plist_file() { echo "$AGENT_DIR/$(launchd_label "$1").plist"; }
launchd_domain() { echo "gui/$(id -u)"; }
launchd_target() { echo "$(launchd_domain)/$(launchd_label "$1")"; }

uses_launchd() {
  [[ "$OS" == Darwin && -f "$(plist_file "$1")" ]]
}

launchd_loaded() {
  launchctl print "$(launchd_target "$1")" >/dev/null 2>&1
}

launchd_start() {
  if launchd_loaded "$1"; then
    launchctl kickstart "$(launchd_target "$1")"
  else
    launchctl bootstrap "$(launchd_domain)" "$(plist_file "$1")"
  fi
}

launchd_restart() {
  if launchd_loaded "$1"; then
    launchctl kickstart -k "$(launchd_target "$1")"
  else
    launchctl bootstrap "$(launchd_domain)" "$(plist_file "$1")"
  fi
}

launchd_stop() {
  if launchd_loaded "$1"; then launchctl bootout "$(launchd_target "$1")" 2>/dev/null || true; fi
}

# launchctl print 里的 "state = running" 和 "pid = 123"。
launchd_field() {
  launchctl print "$(launchd_target "$1")" 2>/dev/null | awk -v k="$2" '$1 == k && $2 == "=" { print $3; exit }'
}

conf_file() {
  echo "$BOTGUARD_CONF_DIR/$1.yaml"
}

# 从 yaml 顶层读 Host/Port，拼出本机能访问的地址。
service_url() {
  local file host port
  file="$(conf_file "$1")"
  [[ -f "$file" ]] || return 1
  host="$(awk '/^Host:/{print $2; exit}' "$file")"
  port="$(awk '/^Port:/{print $2; exit}' "$file")"
  [[ -n "$port" ]] || return 1
  case "$host" in "" | 0.0.0.0 | "::") host=127.0.0.1 ;; esac
  echo "http://$host:$port"
}

# 把环境变量文件导入当前 shell；文件只有 root 可读时借 sudo 读取。
load_env() {
  [[ -e "$BOTGUARD_ENV_FILE" ]] || die "环境变量文件不存在: $BOTGUARD_ENV_FILE"
  set -a
  if [[ -r "$BOTGUARD_ENV_FILE" ]]; then
    # shellcheck disable=SC1090
    . "$BOTGUARD_ENV_FILE"
  elif [[ -n "$SUDO" ]]; then
    # shellcheck disable=SC1090
    . <($SUDO cat "$BOTGUARD_ENV_FILE")
  else
    set +a
    die "没有权限读取 $BOTGUARD_ENV_FILE"
  fi
  set +a
}

# ---------- 编译 ----------

require_go() {
  if ! command -v go >/dev/null 2>&1 && [[ -x /usr/local/go/bin/go ]]; then
    PATH="/usr/local/go/bin:$PATH"
  fi
  command -v go >/dev/null 2>&1 || die "找不到 go 命令"
}

build_one() {
  local name="$1" tmp flags=()
  # 不是 git 仓库（或仓库属主不同）时 go build 取不到 VCS 信息会直接失败。
  if ! git -C "$ROOT_DIR" rev-parse --git-dir >/dev/null 2>&1; then
    flags+=(-buildvcs=false)
  fi
  tmp="$BIN_DIR/.$name.$$"
  echo "编译 $name ..."
  # 先写临时文件再改名，运行中的进程不受影响，编译失败也不会留下半个二进制。
  # shellcheck disable=SC2086
  if ! (cd "$ROOT_DIR" && go build -trimpath ${flags[@]+"${flags[@]}"} ${GO_BUILD_FLAGS:-} -o "$tmp" "./cmd/$name"); then
    rm -f "$tmp"
    die "编译 $name 失败，运行中的服务没有改动"
  fi
  chmod 0755 "$tmp"
  mv -f "$tmp" "$BIN_DIR/$name"
}

build() {
  local svc
  [[ "$FROM_SOURCE" -eq 1 ]] || die "这是发布包，没有源码，不能编译；升级用 scripts/botguard.sh upgrade [版本]"
  require_go
  mkdir -p "$BIN_DIR"
  for svc in $1; do build_one "$svc"; done
  build_one botguard-ctl
}

ensure_built() {
  local svc missing=""
  for svc in $1; do
    [[ -x "$BIN_DIR/$svc" ]] || missing="$missing $svc"
  done
  if [[ -n "$missing" ]]; then build "$missing"; fi
}

# systemd 服务的运行用户；没装服务时返回失败。
service_user() {
  local unit user
  unit="$UNIT_DIR/$(unit_name gateway).service"
  [[ -f "$unit" ]] || return 1
  user="$(awk -F= '/^User=/{print $2; exit}' "$unit")"
  [[ -n "$user" ]] || return 1
  echo "$user"
}

# botguard-ctl 和服务用同一个 SQLite 文件：以服务的运行用户执行，root 不会留下
# 服务读写不了的文件；工作目录和服务一致，相对路径的数据库指向同一个文件。
run_ctl() {
  local user vars
  cd "$ROOT_DIR"
  user="$(service_user 2>/dev/null || true)"
  if [[ -z "$user" || "$user" == "$(id -un)" ]]; then
    "$BIN_DIR/botguard-ctl" "$@"
  elif [[ "$(id -u)" -eq 0 ]]; then
    # runuser 不加 -l 时保留环境变量。
    runuser -u "$user" -- "$BIN_DIR/botguard-ctl" "$@"
  elif [[ -n "$SUDO" ]]; then
    vars="$(compgen -e | grep '^BOTGUARD_' | paste -sd, -)"
    $SUDO ${vars:+--preserve-env="$vars"} -u "$user" -- "$BIN_DIR/botguard-ctl" "$@"
  else
    die "服务以 $user 运行，需要 root 或 sudo 才能以它的身份执行 botguard-ctl"
  fi
}

migrate() {
  [[ -x "$BIN_DIR/botguard-ctl" ]] || build ""
  load_env
  echo "迁移库结构 ..."
  (run_ctl migrate up)
}

ctl() {
  [[ -x "$BIN_DIR/botguard-ctl" ]] || build ""
  load_env
  run_ctl "$@"
}

# ---------- 健康检查 ----------

wait_healthy() {
  local svc="$1" url
  command -v curl >/dev/null 2>&1 || return 0
  url="$(service_url "$svc")" || return 0
  for _ in $(seq 1 20); do
    if curl -fsS -m 2 "$url/healthz" >/dev/null 2>&1; then
      echo "$svc 已就绪: $url"
      return 0
    fi
    sleep 1
  done
  echo "警告: $svc 20 秒内没有响应 $url/healthz，查看日志: scripts/botguard.sh logs $svc -n 50" >&2
  return 1
}

readyz() {
  local url body
  command -v curl >/dev/null 2>&1 || return 0
  url="$(service_url "$1")" || return 0
  body="$(curl -s -m 2 "$url/readyz" 2>/dev/null || true)"
  echo "  /readyz  $url → ${body:-（无响应）}"
}

# ---------- 前台脚本方式（未安装 systemd 服务时） ----------

pid_file() { echo "$LOG_DIR/$1.pid"; }
log_file() { echo "$LOG_DIR/$1.log"; }

is_our_pid() {
  local pid="$1" svc="$2" args
  [[ "$pid" =~ ^[0-9]+$ ]] || return 1
  kill -0 "$pid" 2>/dev/null || return 1
  args="$(ps -p "$pid" -o args= 2>/dev/null || true)"
  [[ "$args" == "$BIN_DIR/$svc "* || "$args" == "$BIN_DIR/$svc" ]]
}

running_pid() {
  local svc="$1" pid
  if [[ -f "$(pid_file "$svc")" ]]; then
    pid="$(cat "$(pid_file "$svc")" 2>/dev/null || true)"
    if is_our_pid "$pid" "$svc"; then
      echo "$pid"
      return 0
    fi
    rm -f "$(pid_file "$svc")"
  fi
  for pid in $(pgrep -f "^$BIN_DIR/$svc( |$)" 2>/dev/null || true); do
    if is_our_pid "$pid" "$svc"; then
      echo "$pid" > "$(pid_file "$svc")"
      echo "$pid"
      return 0
    fi
  done
  return 1
}

manual_start() {
  local svc="$1" pid conf
  mkdir -p "$LOG_DIR"
  if pid="$(running_pid "$svc")"; then
    echo "$svc 已在运行 (PID $pid)"
    return 0
  fi
  conf="$(conf_file "$svc")"
  [[ -f "$conf" ]] || die "配置文件不存在: $conf"
  echo "启动 $svc ..."
  (
    load_env
    cd "$ROOT_DIR"
    if command -v setsid >/dev/null 2>&1; then
      setsid "$BIN_DIR/$svc" -f "$conf" >> "$(log_file "$svc")" 2>&1 < /dev/null &
    else
      nohup "$BIN_DIR/$svc" -f "$conf" >> "$(log_file "$svc")" 2>&1 < /dev/null &
    fi
    echo "$!" > "$(pid_file "$svc")"
  )
  sleep 1
  if ! pid="$(running_pid "$svc")"; then
    echo "错误: $svc 启动失败，最近日志:" >&2
    tail -n 20 "$(log_file "$svc")" >&2 || true
    return 1
  fi
  echo "$svc 已启动 (PID $pid)，日志 $(log_file "$svc")"
  wait_healthy "$svc" || true
}

manual_stop() {
  local svc="$1" pid
  if ! pid="$(running_pid "$svc")"; then
    echo "$svc 未运行"
    return 0
  fi
  echo "停止 $svc (PID $pid) ..."
  kill "$pid" 2>/dev/null || true
  for _ in $(seq 1 20); do
    if ! kill -0 "$pid" 2>/dev/null; then
      rm -f "$(pid_file "$svc")"
      echo "$svc 已停止"
      return 0
    fi
    sleep 1
  done
  echo "$svc 20 秒内没有退出，强制结束"
  kill -9 "$pid" 2>/dev/null || true
  rm -f "$(pid_file "$svc")"
}

# ---------- 对外命令 ----------

start() {
  local svc
  ensure_built "$1"
  for svc in $1; do
    if uses_systemd "$svc"; then
      $SUDO systemctl start "$(unit_name "$svc")"
      wait_healthy "$svc" || true
    elif uses_launchd "$svc"; then
      launchd_start "$svc"
      wait_healthy "$svc" || true
    else
      manual_start "$svc"
    fi
  done
}

stop() {
  local svc
  for svc in $(reverse_words "$1"); do
    if uses_systemd "$svc"; then
      $SUDO systemctl stop "$(unit_name "$svc")"
      echo "$svc 已停止"
    elif uses_launchd "$svc"; then
      launchd_stop "$svc"
      echo "$svc 已停止"
    else
      manual_stop "$svc"
    fi
  done
}

restart() {
  local svc
  ensure_built "$1"
  for svc in $1; do
    if uses_systemd "$svc"; then
      echo "重启 $svc ..."
      $SUDO systemctl restart "$(unit_name "$svc")"
      wait_healthy "$svc" || true
    elif uses_launchd "$svc"; then
      echo "重启 $svc ..."
      launchd_restart "$svc"
      wait_healthy "$svc" || true
    else
      manual_stop "$svc"
      manual_start "$svc"
    fi
  done
}

rebuild() {
  build "$1"
  if [[ "${BOTGUARD_SKIP_MIGRATE:-}" == 1 ]]; then
    echo "跳过迁移（BOTGUARD_SKIP_MIGRATE=1）"
  else
    # 先迁移再换 gateway：新版 gateway 在库结构落后时 /readyz 返回 schema_outdated。
    migrate
  fi
  restart "$1"
}

update() {
  [[ "$FROM_SOURCE" -eq 1 ]] || die "这是发布包，升级用 scripts/botguard.sh upgrade [版本]"
  echo "拉取代码 ..."
  git -C "$ROOT_DIR" pull --ff-only
  git -C "$ROOT_DIR" log -1 --format='当前版本 %h %s'
  rebuild "$1"
}

# 替换发布包里的程序和脚本；配置目录 configs/ 不动。
UPGRADE_FILES="bin/gateway bin/admin-web bin/botguard-ctl scripts/botguard.sh
  deploy/systemd/botguard.service.tmpl deploy/launchd/botguard.plist.tmpl deploy/botguard.env.example
  docs/deploy.md LICENSE README.md VERSION"

sha256_check() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum -c --quiet -
  else
    shasum -a 256 -c - >/dev/null
  fi
}

upgrade() {
  local want="${1:-latest}" os arch tag current name tmp f
  [[ "$FROM_SOURCE" -eq 0 ]] || die "这是源码目录，升级用 scripts/botguard.sh update（git pull 后重新编译）"
  if [[ -f "$ROOT_DIR/PACKAGED" ]]; then
    die "这是 deb/rpm 包安装的，用系统包管理器升级：sudo apt-get install --only-upgrade botguard 或 sudo dnf upgrade botguard"
  fi
  [[ -w "$ROOT_DIR" ]] || die "没有写 $ROOT_DIR 的权限，请用 sudo 执行"
  command -v curl >/dev/null 2>&1 || die "需要 curl"
  case "$OS" in
    Linux) os=linux ;;
    Darwin) os=darwin ;;
    *) die "发布包只提供 Linux 和 macOS 版本" ;;
  esac
  case "$(uname -m)" in
    x86_64 | amd64) arch=amd64 ;;
    aarch64 | arm64) arch=arm64 ;;
    *) die "发布包没有这个架构: $(uname -m)" ;;
  esac
  if [[ "$want" == latest ]]; then
    # releases/latest 重定向到 releases/tag/vX.Y.Z，不用调 GitHub API。
    tag="$(curl -fsSIL -o /dev/null -w '%{url_effective}' "$RELEASE_BASE/latest" 2>/dev/null || true)"
    tag="${tag##*/}"
    [[ "$tag" == v* ]] || die "查不到最新版本，请指定版本号，例如 upgrade v0.2.0"
  else
    tag="v${want#v}"
  fi
  current="$(cat "$ROOT_DIR/VERSION" 2>/dev/null || echo 未知)"
  if [[ "$tag" == "$current" ]]; then
    echo "已经是 $tag"
    return 0
  fi
  echo "升级 $current → $tag"

  name="botguard_${tag#v}_${os}_${arch}"
  # 临时目录放在同一个文件系统里，下面的 mv 才是原子改名。
  tmp="$(mktemp -d "$ROOT_DIR/.upgrade.XXXXXX")"
  # shellcheck disable=SC2064
  trap "rm -rf '$tmp'" EXIT
  curl -fL --progress-bar -o "$tmp/$name.tar.gz" "$RELEASE_BASE/download/$tag/$name.tar.gz" || die "下载失败: $RELEASE_BASE/download/$tag/$name.tar.gz"
  curl -fsSL -o "$tmp/checksums.txt" "$RELEASE_BASE/download/$tag/checksums.txt" || die "下载 checksums.txt 失败"
  (cd "$tmp" && grep " $name.tar.gz\$" checksums.txt | sha256_check) || die "校验和不符，已放弃升级，没有改动任何文件"
  tar -xzf "$tmp/$name.tar.gz" -C "$tmp"
  for f in $UPGRADE_FILES; do
    [[ -e "$tmp/$name/$f" ]] || die "发布包缺少 $f，已放弃升级"
  done
  # 逐个改名替换：运行中的服务和正在执行的本脚本仍持有旧文件，不受影响。
  for f in $UPGRADE_FILES; do
    mkdir -p "$(dirname "$ROOT_DIR/$f")"
    mv -f "$tmp/$name/$f" "$ROOT_DIR/$f"
  done
  echo "程序已替换为 $tag"

  if [[ "${BOTGUARD_SKIP_MIGRATE:-}" == 1 ]]; then
    echo "跳过迁移（BOTGUARD_SKIP_MIGRATE=1）"
  else
    migrate
  fi
  restart "$ALL_SERVICES"
}

status() {
  local svc pid state
  if [[ -x "$BIN_DIR/gateway" ]]; then
    echo "版本 $("$BIN_DIR/gateway" -version 2>/dev/null | cut -d' ' -f2-)"
  fi
  for svc in $1; do
    if uses_systemd "$svc"; then
      state="$(systemctl is-active "$(unit_name "$svc")" 2>/dev/null || true)"
      pid="$(systemctl show -p MainPID --value "$(unit_name "$svc")" 2>/dev/null || true)"
      echo "$svc  [systemd $(unit_name "$svc")]  $state  PID ${pid:-0}"
    elif uses_launchd "$svc"; then
      state="$(launchd_field "$svc" state)"
      pid="$(launchd_field "$svc" pid)"
      echo "$svc  [launchd $(launchd_label "$svc")]  ${state:-未加载}  PID ${pid:-0}"
    elif pid="$(running_pid "$svc")"; then
      echo "$svc  [脚本]  运行中  PID $pid"
    else
      echo "$svc  [脚本]  未运行"
    fi
    readyz "$svc"
  done
}

logs() {
  local svc="${1:-}" lines=200 follow=0
  case "$svc" in
    gateway | admin-web) shift ;;
    *) die "logs 需要指定服务: gateway 或 admin-web" ;;
  esac
  while [[ $# -gt 0 ]]; do
    case "$1" in
      -n | --lines)
        [[ $# -ge 2 && "$2" =~ ^[0-9]+$ ]] || die "行数无效"
        lines="$2"
        shift 2
        ;;
      -f | --follow)
        follow=1
        shift
        ;;
      *) die "logs 不认识的参数: $1" ;;
    esac
  done
  if uses_systemd "$svc"; then
    if [[ "$follow" -eq 1 ]]; then
      $SUDO journalctl -u "$(unit_name "$svc")" -n "$lines" -f
    else
      $SUDO journalctl -u "$(unit_name "$svc")" -n "$lines" --no-pager
    fi
  else
    # 标准输出（启动信息、崩溃）在 logs/<服务>.log；配置为 Mode: file 时，
    # 程序日志在 logs/<服务>/ 下，按大小切分。
    local files=() f
    for f in "$(log_file "$svc")" "$LOG_DIR/$svc/access.log" "$LOG_DIR/$svc/error.log" "$LOG_DIR/$svc/severe.log"; do
      if [[ -f "$f" ]]; then files+=("$f"); fi
    done
    [[ "${#files[@]}" -gt 0 ]] || die "还没有日志: $LOG_DIR/$svc*"
    if [[ "$follow" -eq 1 ]]; then
      tail -n "$lines" -F "${files[@]}"
    else
      tail -n "$lines" "${files[@]}"
    fi
  fi
}

# 前台运行一个服务：加载环境变量后 exec，launchd 用它启动服务。
run_service() {
  local svc conf
  svc="$(parse_services "${1:-}")"
  [[ "$svc" != *" "* ]] || die "run 需要指定服务: gateway 或 admin-web"
  conf="$(conf_file "$svc")"
  [[ -f "$conf" ]] || die "配置文件不存在: $conf"
  [[ -x "$BIN_DIR/$svc" ]] || die "程序不存在: $BIN_DIR/$svc"
  load_env
  cd "$ROOT_DIR"
  exec "$BIN_DIR/$svc" -f "$conf"
}

# ---------- 服务器初始化 ----------

random_key() {
  head -c 32 /dev/urandom | base64 | tr -d '\n'
}

# 本机对外的 IPv4 地址，拼默认的公开地址用。
default_ip() {
  local ip=""
  if command -v ip >/dev/null 2>&1; then
    ip="$(ip -4 route get 1.1.1.1 2>/dev/null | awk '{for (i = 1; i < NF; i++) if ($i == "src") { print $(i + 1); exit }}')"
  fi
  [[ -n "$ip" ]] || ip="$(hostname -I 2>/dev/null | awk '{print $1}')"
  # macOS
  [[ -n "$ip" ]] || ip="$(ipconfig getifaddr en0 2>/dev/null || true)"
  echo "${ip:-127.0.0.1}"
}

is_loopback() {
  case "$1" in 127.* | localhost | ::1) return 0 ;; *) return 1 ;; esac
}

# ask 变量名 提示 默认值：交互时读一行，直接回车用默认值；-y 或没有终端时用默认值。
ask() {
  local answer=""
  if [[ "$INIT_INTERACTIVE" -eq 1 ]]; then
    read -r -p "$2 [$3]: " answer || true
  fi
  printf -v "$1" '%s' "${answer:-$3}"
}

# 改 KEY='value' 文件里的一项，没有就追加；先写临时文件再改名。
set_env_value() {
  local file="$1" key="$2" value="$3" tmp
  [[ "$value" != *"'"* ]] || die "$key 的值不能包含单引号"
  tmp="$(mktemp "$file.XXXXXX")"
  if grep -q "^$key=" "$file"; then
    awk -v k="$key" -v line="$key='$value'" 'index($0, k "=") == 1 { print line; next } { print }' "$file" > "$tmp"
  else
    cat "$file" > "$tmp"
    echo "$key='$value'" >> "$tmp"
  fi
  chmod 0600 "$tmp"
  mv -f "$tmp" "$file"
}

create_system_user() {
  local nologin
  nologin="$(command -v nologin 2>/dev/null || echo /usr/sbin/nologin)"
  if command -v useradd >/dev/null 2>&1; then
    useradd --system --user-group --home-dir "$DATA_DIR" --no-create-home --shell "$nologin" "$1"
  elif command -v adduser >/dev/null 2>&1; then
    # BusyBox（Alpine）
    addgroup -S "$1"
    adduser -S -D -H -h "$DATA_DIR" -s "$nologin" -G "$1" "$1"
  else
    die "找不到 useradd 或 adduser，请先手动创建用户 $1"
  fi
  echo "已创建系统用户 $1"
}

# 从 configs/ 渲染 $ETC_DIR 下的配置：gateway 监听指定的地址；发行版自带的 Redis 常是 7.0，
# 不发 7.2 才有的 CLIENT SETINFO。Linux 上日志改为输出到标准输出（systemd 收进 journal）；
# macOS 保留按大小切分的日志文件（程序目录的 logs/）。其余保持样例的默认值。
render_conf() {
  local svc="$1" listen="$2" args=()
  if [[ "$OS" != Darwin ]]; then
    args+=(-e '/^Log:/,/^[^[:space:]]/ s/^\([[:space:]]*Mode:\) file/\1 console/')
  fi
  args+=(-e 's/^\([[:space:]]*DisableIdentity:\) false/\1 true/')
  if [[ "$svc" == gateway ]]; then
    args+=(-e "s|^Host:.*|Host: $listen|")
  fi
  sed "${args[@]}" "$ROOT_DIR/configs/$svc.yaml"
}

# 改 yaml 顶层的 Host（BSD sed 没有 GNU 的 sed -i 用法，用临时文件）。
set_yaml_host() {
  local file="$1" host="$2" tmp
  tmp="$(mktemp "$file.XXXXXX")"
  awk -v line="Host: $host" '/^Host:/ { print line; next } { print }' "$file" > "$tmp"
  chmod 0644 "$tmp"
  mv -f "$tmp" "$file"
}

init_server() {
  local orig=("$@") run_user=botguard yes=0 endpoint="" listen="" port group env_file svc conf gateway_host tmp
  while [[ $# -gt 0 ]]; do
    case "$1" in
      -y | --yes)
        yes=1
        shift
        ;;
      --user | --public-endpoint | --listen)
        [[ $# -ge 2 && -n "$2" ]] || die "$1 需要参数"
        case "$1" in
          --user) run_user="$2" ;;
          --public-endpoint) endpoint="$2" ;;
          --listen) listen="$2" ;;
        esac
        shift 2
        ;;
      *) die "init 不认识的参数: $1" ;;
    esac
  done
  if [[ "$OS" == Darwin ]]; then
    # macOS 按当前用户安装，服务以当前用户运行。
    [[ "$(id -u)" -ne 0 ]] || die "macOS 上请用普通用户执行（不要 sudo），服务会以当前用户运行"
    run_user="$(id -un)"
  elif [[ "$(id -u)" -ne 0 ]]; then
    [[ -n "$SUDO" ]] || die "init 需要 root"
    exec $SUDO "$SCRIPT_PATH" init ${orig[@]+"${orig[@]}"}
  fi
  [[ -z "$listen" || "$listen" =~ ^[0-9A-Za-z.:_-]+$ ]] || die "监听地址无效: $listen"
  [[ -z "$endpoint" || "$endpoint" =~ ^https?://[^[:space:]\']+$ ]] || die "公开地址应以 http:// 或 https:// 开头: $endpoint"
  INIT_INTERACTIVE=0
  if [[ "$yes" -eq 0 && -t 0 ]]; then INIT_INTERACTIVE=1; fi
  env_file="$ETC_DIR/botguard.env"
  port="$(awk '/^Port:/{print $2; exit}' "$ROOT_DIR/configs/gateway.yaml")"

  if [[ ! -f "$ETC_DIR/gateway.yaml" && -z "$listen" ]]; then
    if [[ "$OS" == Darwin ]]; then
      [[ "$INIT_INTERACTIVE" -eq 0 ]] || echo "gateway 监听地址：只在本机访问填 127.0.0.1；要让手机等局域网设备访问填 0.0.0.0。"
      ask listen "监听地址" 127.0.0.1
    else
      [[ "$INIT_INTERACTIVE" -eq 0 ]] || echo "gateway 监听地址：直接对外提供服务填 0.0.0.0；放在 nginx 等反向代理后面填 127.0.0.1。"
      ask listen "监听地址" 0.0.0.0
    fi
  fi
  if [[ ! -e "$env_file" && -z "$endpoint" ]]; then
    if is_loopback "${listen:-127.0.0.1}"; then
      if [[ "$OS" == Darwin ]]; then
        endpoint="http://127.0.0.1:$port"
      else
        endpoint="https://$(hostname -f 2>/dev/null || hostname)"
      fi
    else
      endpoint="http://$(default_ip):$port"
    fi
    [[ "$INIT_INTERACTIVE" -eq 0 ]] || echo "gateway 公开地址：浏览器和 SDK 用它访问 gateway，会写进管理后台给出的接入代码，之后可以改。"
    ask endpoint "公开地址" "$endpoint"
    [[ "$endpoint" =~ ^https?://[^[:space:]\']+$ ]] || die "公开地址应以 http:// 或 https:// 开头: $endpoint"
  fi

  install -d -m 0755 "$ETC_DIR"
  # 数据库里有加密的应用密钥和密码哈希，只有运行用户能进。
  if [[ "$OS" == Darwin ]]; then
    install -d -m 0700 "$DATA_DIR"
  else
    if ! id "$run_user" >/dev/null 2>&1; then create_system_user "$run_user"; fi
    group="$(id -gn "$run_user")"
    install -d -m 0750 -o "$run_user" -g "$group" "$DATA_DIR"
  fi

  for svc in $ALL_SERVICES; do
    conf="$ETC_DIR/$svc.yaml"
    if [[ ! -f "$conf" ]]; then
      tmp="$(mktemp "$conf.XXXXXX")"
      render_conf "$svc" "${listen:-0.0.0.0}" > "$tmp"
      chmod 0644 "$tmp"
      mv -f "$tmp" "$conf"
      echo "已生成 $conf"
    elif [[ "$svc" == gateway && -n "$listen" ]]; then
      set_yaml_host "$conf" "$listen"
      echo "已更新 $conf：Host: $listen"
    fi
  done

  if [[ ! -e "$env_file" ]]; then
    gateway_host="${listen:-0.0.0.0}"
    case "$gateway_host" in 0.0.0.0 | "::") gateway_host=127.0.0.1 ;; esac
    tmp="$(mktemp "$env_file.XXXXXX")"
    cat > "$tmp" <<ENV
# 由 botguard init 生成于 $(date '+%Y-%m-%d %H:%M:%S')。各项说明见 $ROOT_DIR/deploy/botguard.env.example。
# 格式 KEY='value'，不要写 export：systemd 的 EnvironmentFile 不认。
# 两个密钥生成后不要改：KEK 改了已有的应用密钥对就解不开了，轮换方法见 docs/deploy.md「更换 KEK」。

BOTGUARD_DB_DRIVER='sqlite'
BOTGUARD_DB_DSN='$DATA_DIR/botguard.db'

BOTGUARD_CHALLENGE_KEY_K1='$(random_key)'
BOTGUARD_SECRET_KEK='k1:$(random_key)'
BOTGUARD_SECRET_KEK_PREVIOUS=''

BOTGUARD_PUBLIC_ENDPOINT='$endpoint'
BOTGUARD_GATEWAY_URL='http://$gateway_host:$port'
ENV
    chmod 0600 "$tmp"
    mv -f "$tmp" "$env_file"
    echo "已生成 $env_file（随机密钥，SQLite 数据库 $DATA_DIR/botguard.db）"
  elif [[ -n "$endpoint" ]]; then
    set_env_value "$env_file" BOTGUARD_PUBLIC_ENDPOINT "$endpoint"
    echo "已更新 $env_file：BOTGUARD_PUBLIC_ENDPOINT='$endpoint'"
  else
    echo "$env_file 已存在，保持不变"
  fi
  echo "运行用户 $run_user，数据目录 $DATA_DIR"
}

# ---------- systemd ----------

require_systemd() {
  [[ "$OS" != Darwin ]] || die "macOS 上用 install-launchd / uninstall-launchd"
  command -v systemctl >/dev/null 2>&1 || die "找不到 systemctl，这台机器不是 systemd 系统"
  [[ "$(id -u)" -eq 0 || -n "$SUDO" ]] || die "需要 root 或 sudo"
}

escape_sed() {
  printf '%s' "$1" | sed -e 's/[\/&|]/\\&/g'
}

install_systemd() {
  local run_user="${SUDO_USER:-$(id -un)}" run_group="" start_now=1 svc conf tmp unit
  while [[ $# -gt 0 ]]; do
    case "$1" in
      --user)
        [[ $# -ge 2 ]] || die "--user 需要参数"
        run_user="$2"
        shift 2
        ;;
      --group)
        [[ $# -ge 2 ]] || die "--group 需要参数"
        run_group="$2"
        shift 2
        ;;
      --no-start)
        start_now=0
        shift
        ;;
      *) die "install-systemd 不认识的参数: $1" ;;
    esac
  done
  require_systemd
  id "$run_user" >/dev/null 2>&1 || die "用户不存在: $run_user"
  [[ -n "$run_group" ]] || run_group="$(id -gn "$run_user")"
  [[ -f "$TEMPLATE_FILE" ]] || die "找不到模板: $TEMPLATE_FILE"
  [[ "$BOTGUARD_ENV_FILE" == /* && "$BOTGUARD_CONF_DIR" == /* ]] || die "BOTGUARD_ENV_FILE 和 BOTGUARD_CONF_DIR 必须是绝对路径"
  [[ -e "$BOTGUARD_ENV_FILE" ]] || die "环境变量文件不存在: $BOTGUARD_ENV_FILE（参考 deploy/botguard.env.example）"
  # systemd 的 EnvironmentFile 不认 export，带 export 的行会被忽略，服务因缺密钥起不来。
  if $SUDO grep -qE '^[[:space:]]*export[[:space:]]' "$BOTGUARD_ENV_FILE"; then
    die "$BOTGUARD_ENV_FILE 里有 export 开头的行，systemd 不识别。去掉: sudo sed -i 's/^[[:space:]]*export[[:space:]]*//' $BOTGUARD_ENV_FILE"
  fi
  for svc in $ALL_SERVICES; do
    conf="$(conf_file "$svc")"
    [[ -f "$conf" ]] || die "配置文件不存在: $conf"
  done
  ensure_built "$ALL_SERVICES"

  tmp="$(mktemp)"
  # shellcheck disable=SC2064
  trap "rm -f '$tmp'" EXIT
  for svc in $ALL_SERVICES; do
    unit="$(unit_name "$svc")"
    # 脚本方式启动的进程先停掉，否则和 systemd 抢端口。
    if ! uses_systemd "$svc" && running_pid "$svc" >/dev/null; then manual_stop "$svc"; fi
    sed \
      -e "s|__PROJECT_DIR__|$(escape_sed "$ROOT_DIR")|g" \
      -e "s|__RUN_USER__|$(escape_sed "$run_user")|g" \
      -e "s|__RUN_GROUP__|$(escape_sed "$run_group")|g" \
      -e "s|__ENV_FILE__|$(escape_sed "$BOTGUARD_ENV_FILE")|g" \
      -e "s|__CONF_DIR__|$(escape_sed "$BOTGUARD_CONF_DIR")|g" \
      -e "s|__SERVICE__|$svc|g" \
      "$TEMPLATE_FILE" > "$tmp"
    $SUDO install -m 0644 "$tmp" "$UNIT_DIR/$unit.service"
    echo "已安装 $UNIT_DIR/$unit.service"
  done
  $SUDO systemctl daemon-reload
  for svc in $ALL_SERVICES; do
    $SUDO systemctl enable "$(unit_name "$svc")" >/dev/null 2>&1
  done
  echo "代码目录 $ROOT_DIR，运行用户 $run_user:$run_group"
  echo "配置目录 $BOTGUARD_CONF_DIR，环境变量 $BOTGUARD_ENV_FILE"
  echo "已设为开机自启"
  if [[ "$start_now" -eq 1 ]]; then
    restart "$ALL_SERVICES"
    status "$ALL_SERVICES"
  fi
}

uninstall_systemd() {
  local svc unit
  require_systemd
  for svc in $(reverse_words "$ALL_SERVICES"); do
    unit="$(unit_name "$svc")"
    if [[ ! -f "$UNIT_DIR/$unit.service" ]]; then
      echo "$unit 没有安装"
      continue
    fi
    $SUDO systemctl disable --now "$unit" >/dev/null 2>&1 || true
    $SUDO rm -f "$UNIT_DIR/$unit.service"
    $SUDO systemctl reset-failed "$unit" >/dev/null 2>&1 || true
    echo "已停止并删除 $unit"
  done
  $SUDO systemctl daemon-reload
}

# ---------- launchd ----------

# 路径写进 XML：转义 &、<、>，再转义 sed 替换串里的特殊字符。
# 不用 ${v//&/...}：bash 5.2 起替换串里的 & 表示匹配到的文本。
escape_plist() {
  escape_sed "$(printf '%s' "$1" | sed -e 's/&/\&amp;/g' -e 's/</\&lt;/g' -e 's/>/\&gt;/g')"
}

install_launchd() {
  local start_now=1 svc conf plist tmp
  while [[ $# -gt 0 ]]; do
    case "$1" in
      --no-start)
        start_now=0
        shift
        ;;
      *) die "install-launchd 不认识的参数: $1" ;;
    esac
  done
  [[ "$OS" == Darwin ]] || die "install-launchd 只用于 macOS；Linux 用 install-systemd"
  [[ "$(id -u)" -ne 0 ]] || die "请用普通用户执行（不要 sudo），服务会以当前用户运行"
  [[ -f "$PLIST_TEMPLATE" ]] || die "找不到模板: $PLIST_TEMPLATE"
  [[ -e "$BOTGUARD_ENV_FILE" ]] || die "环境变量文件不存在: $BOTGUARD_ENV_FILE（先执行 botguard init）"
  for svc in $ALL_SERVICES; do
    conf="$(conf_file "$svc")"
    [[ -f "$conf" ]] || die "配置文件不存在: $conf"
  done
  ensure_built "$ALL_SERVICES"
  mkdir -p "$AGENT_DIR" "$LOG_DIR"
  for svc in $ALL_SERVICES; do
    plist="$(plist_file "$svc")"
    # 脚本方式启动的进程先停掉，否则和 launchd 抢端口；已加载的旧服务先卸下，新的 plist 才会生效。
    if uses_launchd "$svc"; then
      launchd_stop "$svc"
    elif running_pid "$svc" >/dev/null; then
      manual_stop "$svc"
    fi
    tmp="$(mktemp "$plist.XXXXXX")"
    sed \
      -e "s|__PROJECT_DIR__|$(escape_plist "$ROOT_DIR")|g" \
      -e "s|__SERVICE__|$svc|g" \
      "$PLIST_TEMPLATE" > "$tmp"
    chmod 0644 "$tmp"
    mv -f "$tmp" "$plist"
    echo "已安装 $plist"
  done
  echo "程序目录 $ROOT_DIR，以当前用户 $(id -un) 运行，登录后自动启动"
  echo "配置目录 $BOTGUARD_CONF_DIR，环境变量 $BOTGUARD_ENV_FILE，日志 $LOG_DIR"
  if [[ "$start_now" -eq 1 ]]; then
    for svc in $ALL_SERVICES; do
      launchd_start "$svc"
      wait_healthy "$svc" || true
    done
    status "$ALL_SERVICES"
  fi
}

uninstall_launchd() {
  local svc plist
  [[ "$OS" == Darwin ]] || die "uninstall-launchd 只用于 macOS"
  for svc in $(reverse_words "$ALL_SERVICES"); do
    plist="$(plist_file "$svc")"
    if [[ ! -f "$plist" ]]; then
      echo "$(launchd_label "$svc") 没有安装"
      continue
    fi
    launchd_stop "$svc"
    rm -f "$plist"
    echo "已停止并删除 $(launchd_label "$svc")"
  done
}

main() {
  local cmd="${1:-help}"
  [[ $# -gt 0 ]] && shift
  case "$cmd" in
    start | stop | restart | rebuild | update | build | status)
      [[ $# -le 1 ]] || die "$cmd 只接受一个服务参数"
      "$cmd" "$(parse_services "${1:-all}")"
      ;;
    migrate) migrate ;;
    upgrade)
      [[ $# -le 1 ]] || die "upgrade 只接受一个版本参数"
      upgrade "${1:-latest}"
      ;;
    ctl) ctl "$@" ;;
    logs) logs "$@" ;;
    init) init_server "$@" ;;
    run)
      [[ $# -eq 1 ]] || die "run 需要指定服务: gateway 或 admin-web"
      run_service "$1"
      ;;
    install-systemd) install_systemd "$@" ;;
    install-launchd) install_launchd "$@" ;;
    uninstall-launchd) uninstall_launchd ;;
    uninstall-systemd) uninstall_systemd ;;
    help | -h | --help) usage ;;
    *)
      usage
      die "未知命令: $cmd"
      ;;
  esac
}

main "$@"
