#!/usr/bin/env bash
# BotGuard 服务管理：编译、启停、迁移、日志，以及安装 systemd 服务。
# 装了 systemd 服务后，start/stop/restart/status/logs 都转给 systemctl/journalctl，
# 不会和 systemd 拉起的进程重复启动；没装时用 nohup 在后台运行，日志写 logs/。
# 说明见 docs/deploy.md。
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
BIN_DIR="$ROOT_DIR/bin"
LOG_DIR="$ROOT_DIR/logs"
TEMPLATE_FILE="$ROOT_DIR/deploy/systemd/botguard.service.tmpl"
UNIT_DIR=/etc/systemd/system

# gateway 先启动、后停止：admin-web 会检查 gateway 的 /readyz。
ALL_SERVICES="gateway admin-web"

# 服务器上的配置放 /etc/botguard，本机开发用仓库里的 configs/ 和 .env。
if [[ -z "${BOTGUARD_CONF_DIR:-}" ]]; then
  if [[ -f /etc/botguard/gateway.yaml ]]; then
    BOTGUARD_CONF_DIR=/etc/botguard
  else
    BOTGUARD_CONF_DIR="$ROOT_DIR/configs"
  fi
fi
if [[ -z "${BOTGUARD_ENV_FILE:-}" ]]; then
  if [[ -e /etc/botguard/botguard.env ]]; then
    BOTGUARD_ENV_FILE=/etc/botguard/botguard.env
  else
    BOTGUARD_ENV_FILE="$ROOT_DIR/.env"
  fi
fi

SUDO=""
if [[ "$(id -u)" -ne 0 ]] && command -v sudo >/dev/null 2>&1; then
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
  rebuild [服务]        编译 → 迁移库结构 → 重启（发布新代码用这个）
  update [服务]         git pull --ff-only 后执行 rebuild
  build [服务]          只编译到 bin/（同时编译 botguard-ctl）
  migrate               执行 botguard-ctl migrate up
  ctl <参数...>          加载环境变量后执行 botguard-ctl，例如 ctl admin create -username admin
  status [服务]         运行状态、PID 和 /readyz
  logs <服务> [-n 行数] [-f]
  install-systemd [--user 用户] [--group 组] [--no-start]
                        安装 systemd 服务并设为开机自启，默认装完立即（重新）启动
  uninstall-systemd     停止、禁用并删除 systemd 服务（不删二进制、配置和日志）

当前配置:
  配置目录  $BOTGUARD_CONF_DIR
  环境变量  $BOTGUARD_ENV_FILE

环境变量:
  BOTGUARD_CONF_DIR       gateway.yaml / admin-web.yaml 所在目录
  BOTGUARD_ENV_FILE       密钥与 DSN 文件（KEY='value' 格式，不写 export）
  BOTGUARD_SKIP_MIGRATE=1 rebuild/update 时跳过迁移
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

migrate() {
  [[ -x "$BIN_DIR/botguard-ctl" ]] || build ""
  load_env
  echo "迁移库结构 ..."
  "$BIN_DIR/botguard-ctl" migrate up
}

ctl() {
  [[ -x "$BIN_DIR/botguard-ctl" ]] || build ""
  load_env
  exec "$BIN_DIR/botguard-ctl" "$@"
}

# ---------- 健康检查 ----------

wait_healthy() {
  local svc="$1" url i
  command -v curl >/dev/null 2>&1 || return 0
  url="$(service_url "$svc")" || return 0
  for i in $(seq 1 20); do
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
  local svc="$1" pid i
  if ! pid="$(running_pid "$svc")"; then
    echo "$svc 未运行"
    return 0
  fi
  echo "停止 $svc (PID $pid) ..."
  kill "$pid" 2>/dev/null || true
  for i in $(seq 1 20); do
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
  echo "拉取代码 ..."
  git -C "$ROOT_DIR" pull --ff-only
  git -C "$ROOT_DIR" log -1 --format='当前版本 %h %s'
  rebuild "$1"
}

status() {
  local svc pid state
  for svc in $1; do
    if uses_systemd "$svc"; then
      state="$(systemctl is-active "$(unit_name "$svc")" 2>/dev/null || true)"
      pid="$(systemctl show -p MainPID --value "$(unit_name "$svc")" 2>/dev/null || true)"
      echo "$svc  [systemd $(unit_name "$svc")]  $state  PID ${pid:-0}"
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
    [[ -f "$(log_file "$svc")" ]] || die "还没有日志: $(log_file "$svc")"
    if [[ "$follow" -eq 1 ]]; then
      tail -n "$lines" -f "$(log_file "$svc")"
    else
      tail -n "$lines" "$(log_file "$svc")"
    fi
  fi
}

# ---------- systemd ----------

require_systemd() {
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
    if ! uses_systemd "$svc"; then manual_stop "$svc"; fi
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

main() {
  local cmd="${1:-help}"
  [[ $# -gt 0 ]] && shift
  case "$cmd" in
    start | stop | restart | rebuild | update | build | status)
      [[ $# -le 1 ]] || die "$cmd 只接受一个服务参数"
      "$cmd" "$(parse_services "${1:-all}")"
      ;;
    migrate) migrate ;;
    ctl) ctl "$@" ;;
    logs) logs "$@" ;;
    install-systemd) install_systemd "$@" ;;
    uninstall-systemd) uninstall_systemd ;;
    help | -h | --help) usage ;;
    *)
      usage
      die "未知命令: $cmd"
      ;;
  esac
}

main "$@"
