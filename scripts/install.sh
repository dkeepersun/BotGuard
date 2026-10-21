#!/bin/sh
# BotGuard 一键安装：下载程序、安装 Redis、生成配置和密钥、注册开机自启的系统服务，
# 最后创建第一个管理员。支持 Linux（systemd，系统级）和 macOS（launchd，当前用户）。
#
#   curl -fsSL https://raw.githubusercontent.com/dkeepersun/BotGuard/main/scripts/install.sh | sh
#   curl -fsSL https://raw.githubusercontent.com/dkeepersun/BotGuard/main/scripts/install.sh | sh -s -- --method docker
#
# 重复执行是安全的：已经装过时改为升级，不会覆盖配置和数据。说明见 docs/install.md。
set -eu

REPO="dkeepersun/BotGuard"
RELEASE_BASE="${BOTGUARD_RELEASE_BASE:-https://github.com/$REPO/releases}"
RAW_BASE="${BOTGUARD_RAW_BASE:-https://raw.githubusercontent.com/$REPO}"
REPO_URL="${BOTGUARD_REPO_URL:-https://dkeepersun.github.io/BotGuard}"
GIT_URL="${BOTGUARD_GIT_URL:-https://github.com/$REPO.git}"

METHOD="${BOTGUARD_METHOD:-auto}"
VERSION="${BOTGUARD_VERSION:-latest}"
# 默认 Linux 为 /opt/botguard，macOS 为 ~/Library/Application Support/BotGuard（detect_env 里决定）。
INSTALL_DIR="${BOTGUARD_INSTALL_DIR:-}"
PUBLIC_ENDPOINT="${BOTGUARD_PUBLIC_ENDPOINT:-}"
LISTEN="${BOTGUARD_LISTEN:-}"
ADMIN_USER="${BOTGUARD_ADMIN_USER:-admin}"
RUN_USER=botguard
ETC_DIR=/etc/botguard
WITH_REDIS=1
START=1
YES=0

usage() {
  cat <<USAGE
BotGuard 一键安装

用法: install.sh [选项]
      curl -fsSL $RAW_BASE/main/scripts/install.sh | sh -s -- [选项]

安装方式（--method）:
  auto      默认。Debian/Ubuntu、RHEL/Fedora 等有软件源时装 deb/rpm 包，否则装发布包
  package   从 BotGuard 软件源安装 deb/rpm 包，之后用 apt / dnf 升级（仅 Linux）
  binary    下载 GitHub Release 发布包到 --dir，之后用 botguard upgrade 升级（macOS 默认）
  docker    用 Docker Compose 运行（含 Redis），文件放在 --dir
  source    git clone 源码到 --dir 并编译（需要 Go 和 git），之后用 botguard update 升级

选项:
  --method 方式            见上
  --version vX.Y.Z         安装指定版本，默认最新版
  --dir 目录               binary / source / docker 的安装目录，默认 Linux 为 /opt/botguard，
                           macOS 为 ~/Library/Application Support/BotGuard
  --public-endpoint 地址   gateway 的公开地址，例如 https://captcha.example.com
  --listen 地址            gateway 监听地址：0.0.0.0（直接对外）或 127.0.0.1（在反向代理后面或只在本机用）
  --admin 用户名           第一个管理员的用户名，默认 $ADMIN_USER
  --no-redis               不安装 Redis（已有 Redis 或用其他机器上的，记得改 Redis.Host）
  --no-start               只安装，不启动服务
  -y, --yes                不提问，全部用默认值
  -h, --help               显示帮助

同名环境变量也可以设置这些选项：BOTGUARD_METHOD、BOTGUARD_VERSION、BOTGUARD_INSTALL_DIR、
BOTGUARD_PUBLIC_ENDPOINT、BOTGUARD_LISTEN、BOTGUARD_ADMIN_USER。
USAGE
}

say() { printf '\033[1;32m==>\033[0m %s\n' "$*"; }
warn() { printf '\033[1;33m警告:\033[0m %s\n' "$*" >&2; }
die() {
  printf '\033[1;31m错误:\033[0m %s\n' "$*" >&2
  exit 1
}
has() { command -v "$1" >/dev/null 2>&1; }

parse_args() {
  while [ $# -gt 0 ]; do
    case "$1" in
      --method | --version | --dir | --public-endpoint | --listen | --admin)
        [ $# -ge 2 ] && [ -n "$2" ] || die "$1 需要参数"
        case "$1" in
          --method) METHOD="$2" ;;
          --version) VERSION="$2" ;;
          --dir) INSTALL_DIR="$2" ;;
          --public-endpoint) PUBLIC_ENDPOINT="$2" ;;
          --listen) LISTEN="$2" ;;
          --admin) ADMIN_USER="$2" ;;
        esac
        shift 2
        ;;
      --no-redis) WITH_REDIS=0 && shift ;;
      --no-start) START=0 && shift ;;
      -y | --yes) YES=1 && shift ;;
      -h | --help) usage && exit 0 ;;
      *) die "不认识的参数: $1（--help 查看用法）" ;;
    esac
  done
  case "$METHOD" in auto | package | binary | docker | source) ;; *) die "未知安装方式: $METHOD" ;; esac
  case "$INSTALL_DIR" in "" | /*) ;; *) die "--dir 必须是绝对路径: $INSTALL_DIR" ;; esac
  case "$PUBLIC_ENDPOINT" in "" | http://* | https://*) ;; *) die "公开地址应以 http:// 或 https:// 开头: $PUBLIC_ENDPOINT" ;; esac
  if [ "$VERSION" != latest ]; then VERSION="v${VERSION#v}"; fi
}

# ---------- 环境 ----------

detect_env() {
  OS="$(uname -s)"
  case "$OS" in
    Linux) OSNAME=linux ;;
    Darwin) OSNAME=darwin ;;
    *) die "只支持 Linux 和 macOS；其他系统请用 Docker 部署，见 docs/install.md" ;;
  esac
  case "$(uname -m)" in
    x86_64 | amd64) ARCH=amd64 ;;
    aarch64 | arm64) ARCH=arm64 ;;
    *) die "暂不提供这个架构的程序: $(uname -m)" ;;
  esac
  has curl || die "需要 curl"
  SUDO=""
  SYSTEMD=0
  BREW=""
  if [ "$OS" = Darwin ]; then
    # macOS 按当前用户安装：程序、配置、数据都在一个目录里，服务注册为 LaunchAgent。
    [ "$(id -u)" -ne 0 ] || die "macOS 上请用普通用户执行（不要加 sudo），服务会以当前用户运行"
    [ -n "$INSTALL_DIR" ] || INSTALL_DIR="$HOME/Library/Application Support/BotGuard"
    ETC_DIR="$INSTALL_DIR/etc"
    for b in brew /opt/homebrew/bin/brew /usr/local/bin/brew; do
      if has "$b"; then
        BREW="$(command -v "$b")"
        break
      fi
    done
    # curl | sh 的环境里不一定有 Homebrew 的 PATH（redis-cli 在那里）。
    if [ -n "$BREW" ]; then PATH="$("$BREW" --prefix)/bin:$PATH"; fi
    PM=""
  else
    [ -n "$INSTALL_DIR" ] || INSTALL_DIR=/opt/botguard
    if [ "$(id -u)" -ne 0 ]; then
      has sudo || die "需要 root 权限：请用 root 运行，或先安装 sudo"
      SUDO=sudo
    fi
    detect_linux
  fi
  INTERACTIVE=0
  if [ "$YES" -eq 0 ] && (: </dev/tty) 2>/dev/null; then INTERACTIVE=1; fi
}

detect_linux() {
  PM=""
  for p in apt-get dnf yum apk zypper pacman; do
    if has "$p"; then
      PM="$p"
      break
    fi
  done
  if [ -d /run/systemd/system ] && has systemctl; then SYSTEMD=1; fi
}

# prompt 变量名 提示 默认值：有终端时提问，直接回车用默认值。
prompt() {
  answer=""
  if [ "$INTERACTIVE" -eq 1 ]; then
    printf '%s [%s]: ' "$2" "$3" >/dev/tty
    read -r answer </dev/tty || answer=""
  fi
  [ -n "$answer" ] || answer="$3"
  eval "$1=\$answer"
}

default_ip() {
  ip=""
  if has ip; then
    ip="$(ip -4 route get 1.1.1.1 2>/dev/null | awk '{for (i = 1; i < NF; i++) if ($i == "src") { print $(i + 1); exit }}')"
  fi
  [ -n "$ip" ] || ip="$(hostname -I 2>/dev/null | awk '{print $1}')"
  # macOS
  [ -n "$ip" ] || ip="$(ipconfig getifaddr en0 2>/dev/null || true)"
  echo "${ip:-127.0.0.1}"
}

# 首次安装时问两个问题：gateway 监听哪里、对外地址是什么。
ask_settings() {
  if [ -z "$LISTEN" ]; then
    if [ "$OS" = Darwin ]; then
      [ "$INTERACTIVE" -eq 0 ] || echo "gateway 监听地址：只在本机访问填 127.0.0.1；要让手机等局域网设备访问填 0.0.0.0。"
      prompt LISTEN "监听地址" 127.0.0.1
    else
      [ "$INTERACTIVE" -eq 0 ] || echo "gateway 监听地址：直接对外提供服务填 0.0.0.0；放在 nginx 等反向代理后面填 127.0.0.1。"
      prompt LISTEN "监听地址" 0.0.0.0
    fi
  fi
  if [ -z "$PUBLIC_ENDPOINT" ]; then
    case "$LISTEN" in
      127.* | localhost | ::1)
        if [ "$OS" = Darwin ]; then
          default="http://127.0.0.1:8090"
        else
          default="https://$(hostname -f 2>/dev/null || hostname)"
        fi
        ;;
      *) default="http://$(default_ip):8090" ;;
    esac
    [ "$INTERACTIVE" -eq 0 ] || echo "gateway 公开地址：浏览器和 SDK 用它访问 gateway，会写进接入代码，之后可以改。"
    prompt PUBLIC_ENDPOINT "公开地址" "$default"
  fi
  case "$PUBLIC_ENDPOINT" in http://* | https://*) ;; *) die "公开地址应以 http:// 或 https:// 开头: $PUBLIC_ENDPOINT" ;; esac
}

sha256_check() {
  if has sha256sum; then sha256sum -c --quiet -; else shasum -a 256 -c - >/dev/null; fi
}

resolve_tag() {
  if [ "$VERSION" != latest ]; then
    TAG="$VERSION"
    return
  fi
  # releases/latest 重定向到 releases/tag/vX.Y.Z，不用调 GitHub API。
  TAG="$(curl -fsSIL -o /dev/null -w '%{url_effective}' "$RELEASE_BASE/latest" 2>/dev/null || true)"
  TAG="${TAG##*/}"
  case "$TAG" in v*) ;; *) die "查不到最新版本，请用 --version 指定，或检查能否访问 $RELEASE_BASE" ;; esac
}

pick_method() {
  if [ "$OS" = Darwin ]; then
    [ "$METHOD" != package ] || die "macOS 没有 deb/rpm 包，请用默认方式（发布包）或 --method docker"
  fi
  [ "$METHOD" = auto ] || return 0
  METHOD=binary
  if [ "$VERSION" = latest ] && [ "$SYSTEMD" -eq 1 ]; then
    case "$PM" in
      apt-get | dnf | yum)
        if curl -fsSI -m 10 "$REPO_URL/gpg.key" >/dev/null 2>&1; then METHOD=package; fi
        ;;
    esac
  fi
}

# ---------- Redis ----------

redis_ping() {
  for cli in redis-cli valkey-cli; do
    if has "$cli" && "$cli" -h 127.0.0.1 -p 6379 ping 2>/dev/null | grep -q PONG; then return 0; fi
  done
  return 1
}

enable_service() {
  for s in "$@"; do
    if [ "$SYSTEMD" -eq 1 ]; then
      if systemctl list-unit-files "$s.service" 2>/dev/null | grep -q "^$s\.service"; then
        $SUDO systemctl enable --now "$s" >/dev/null 2>&1 && return 0
      fi
    elif has rc-service; then
      if [ -e "/etc/init.d/$s" ]; then
        $SUDO rc-update add "$s" default >/dev/null 2>&1 || true
        $SUDO rc-service "$s" start && return 0
      fi
    elif has service; then
      $SUDO service "$s" start >/dev/null 2>&1 && return 0
    fi
  done
  return 1
}

ensure_redis() {
  [ "$WITH_REDIS" -eq 1 ] || return 0
  if redis_ping; then
    say "Redis 已在运行"
    return 0
  fi
  say "安装 Redis"
  if [ "$OS" = Darwin ]; then
    [ -n "$BREW" ] || die "需要 Homebrew 来安装 Redis（https://brew.sh）；或自行安装 Redis 后加 --no-redis 重试"
    "$BREW" install redis
    "$BREW" services start redis || warn "Redis 没能自动启动，请执行: brew services start redis"
    wait_redis
    return 0
  fi
  case "$PM" in
    apt-get)
      $SUDO apt-get update -qq
      $SUDO env DEBIAN_FRONTEND=noninteractive apt-get install -y -qq redis-server >/dev/null
      ;;
    dnf | yum) $SUDO "$PM" install -y -q redis || $SUDO "$PM" install -y -q valkey ;;
    apk) $SUDO apk add -q redis ;;
    zypper) $SUDO zypper -n -q install redis ;;
    pacman) $SUDO pacman -S --noconfirm --needed valkey ;;
    *) die "不认识这台机器的包管理器，请手动安装 Redis（≥ 7.2）后加 --no-redis 重试" ;;
  esac
  enable_service redis-server redis valkey || warn "Redis 已安装但没能自动启动，请手动启动并设为开机自启"
  wait_redis
}

wait_redis() {
  i=0
  while [ $i -lt 10 ]; do
    if redis_ping; then
      say "Redis 已启动"
      return 0
    fi
    i=$((i + 1))
    sleep 1
  done
  warn "Redis 没有在 127.0.0.1:6379 响应；BotGuard 需要它才能启动"
}

# ---------- 原生安装（发布包、源码）的公共步骤 ----------

BG=""

# 把 botguard 命令链接到 PATH 里：Linux 用 /usr/local/bin；macOS 用 Homebrew 的 bin
# （当前用户可写，已在 PATH 里），没有 Homebrew 时用 ~/.local/bin。
link_command() {
  if [ "$OS" != Darwin ]; then
    $SUDO ln -sf "$INSTALL_DIR/scripts/botguard.sh" /usr/local/bin/botguard
    BG=/usr/local/bin/botguard
    return 0
  fi
  if [ -n "$BREW" ]; then
    dir="$("$BREW" --prefix)/bin"
  else
    dir="$HOME/.local/bin"
    mkdir -p "$dir"
  fi
  ln -sf "$INSTALL_DIR/scripts/botguard.sh" "$dir/botguard"
  BG="$dir/botguard"
  case ":$PATH:" in
    *":$dir:"*) ;;
    *) warn "$dir 不在 PATH 里，请把它加进 shell 配置，例如: echo 'export PATH=\"$dir:\$PATH\"' >> ~/.zshrc" ;;
  esac
}

setup_native() {
  link_command
  if [ "$OS" = Darwin ]; then
    "$BG" init -y --listen "$LISTEN" --public-endpoint "$PUBLIC_ENDPOINT"
    if [ "$START" -eq 1 ]; then
      "$BG" install-launchd
    else
      "$BG" install-launchd --no-start
    fi
    return 0
  fi
  $SUDO "$BG" init -y --user "$RUN_USER" --listen "$LISTEN" --public-endpoint "$PUBLIC_ENDPOINT"
  if [ "$SYSTEMD" -eq 1 ]; then
    if [ "$START" -eq 1 ]; then
      $SUDO "$BG" install-systemd --user "$RUN_USER"
    else
      $SUDO "$BG" install-systemd --user "$RUN_USER" --no-start
    fi
  else
    warn "这台机器没有 systemd，服务不会开机自启；用 botguard start / stop 管理"
    if [ "$START" -eq 1 ]; then $SUDO "$BG" start; fi
  fi
}

create_admin() {
  # 只有表头一行说明还没有管理员。
  if [ "$($SUDO "$BG" ctl admin list 2>/dev/null | wc -l)" -gt 1 ]; then return 0; fi
  say "创建管理员 $ADMIN_USER（初始密码只显示这一次，请立即记下，首次登录后修改）"
  $SUDO "$BG" ctl admin create -username "$ADMIN_USER" || warn "创建管理员失败，稍后执行: sudo botguard ctl admin create -username $ADMIN_USER"
}

# ---------- 安装方式 ----------

install_binary() {
  if [ -f "$INSTALL_DIR/go.mod" ]; then
    die "$INSTALL_DIR 是源码安装，升级用 sudo botguard update"
  fi
  if [ -x "$INSTALL_DIR/scripts/botguard.sh" ] && [ -f "$INSTALL_DIR/VERSION" ]; then
    say "已安装 $(cat "$INSTALL_DIR/VERSION")，改为升级（配置和数据不变）"
    $SUDO "$INSTALL_DIR/scripts/botguard.sh" upgrade "$VERSION"
    UPGRADED=1
    return 0
  fi
  resolve_tag
  ask_settings
  ensure_redis
  name="botguard_${TAG#v}_${OSNAME}_${ARCH}"
  say "下载 BotGuard $TAG（$OSNAME/$ARCH）"
  tmp="$(mktemp -d)"
  trap 'rm -rf "$tmp"' EXIT
  curl -fL --progress-bar -o "$tmp/$name.tar.gz" "$RELEASE_BASE/download/$TAG/$name.tar.gz" || die "下载失败: $RELEASE_BASE/download/$TAG/$name.tar.gz"
  curl -fsSL -o "$tmp/checksums.txt" "$RELEASE_BASE/download/$TAG/checksums.txt" || die "下载 checksums.txt 失败"
  (cd "$tmp" && grep " $name.tar.gz\$" checksums.txt | sha256_check) || die "校验和不符，已放弃安装"
  tar -xzf "$tmp/$name.tar.gz" -C "$tmp"
  $SUDO install -d -m 0755 "$INSTALL_DIR"
  $SUDO cp -R "$tmp/$name/." "$INSTALL_DIR/"
  say "程序已安装到 $INSTALL_DIR"
  setup_native
}

install_source() {
  has git || die "源码安装需要 git"
  has go || [ -x /usr/local/go/bin/go ] || die "源码安装需要 Go（版本见 go.mod），https://go.dev/dl/"
  if [ -f "$INSTALL_DIR/go.mod" ]; then
    say "已有源码安装，改为更新（git pull、编译、重启）"
    $SUDO "$INSTALL_DIR/scripts/botguard.sh" update
    UPGRADED=1
    return 0
  fi
  [ ! -e "$INSTALL_DIR" ] || [ -z "$(ls -A "$INSTALL_DIR" 2>/dev/null)" ] || die "$INSTALL_DIR 已存在且不是源码目录，换一个 --dir"
  ask_settings
  ensure_redis
  say "克隆源码到 $INSTALL_DIR"
  if [ "$VERSION" = latest ]; then
    $SUDO git clone -q "$GIT_URL" "$INSTALL_DIR"
  else
    $SUDO git clone -q --branch "$VERSION" "$GIT_URL" "$INSTALL_DIR"
  fi
  $SUDO "$INSTALL_DIR/scripts/botguard.sh" build
  setup_native
}

install_package() {
  BG=/usr/bin/botguard
  [ "$SYSTEMD" -eq 1 ] || die "deb/rpm 包需要 systemd；这台机器请用 --method binary"
  first=1
  if [ -e "$ETC_DIR/botguard.env" ]; then first=0; fi
  if [ "$first" -eq 1 ]; then ask_settings; fi
  ensure_redis
  case "$PM" in
    apt-get)
      say "添加 BotGuard 软件源 $REPO_URL/deb"
      $SUDO install -d -m 0755 /etc/apt/keyrings
      curl -fsSL "$REPO_URL/gpg.key" | $SUDO tee /etc/apt/keyrings/botguard.asc >/dev/null
      echo "deb [signed-by=/etc/apt/keyrings/botguard.asc] $REPO_URL/deb ./" | $SUDO tee /etc/apt/sources.list.d/botguard.list >/dev/null
      $SUDO apt-get update -qq
      say "安装 botguard"
      $SUDO env DEBIAN_FRONTEND=noninteractive BOTGUARD_LISTEN="$LISTEN" BOTGUARD_PUBLIC_ENDPOINT="$PUBLIC_ENDPOINT" \
        BOTGUARD_NO_START="$((1 - START))" apt-get install -y botguard
      ;;
    dnf | yum)
      say "添加 BotGuard 软件源 $REPO_URL/rpm"
      $SUDO tee /etc/yum.repos.d/botguard.repo >/dev/null <<REPO
[botguard]
name=BotGuard
baseurl=$REPO_URL/rpm
enabled=1
gpgcheck=1
repo_gpgcheck=1
gpgkey=$REPO_URL/gpg.key
REPO
      say "安装 botguard"
      if [ "$first" -eq 1 ]; then
        $SUDO env BOTGUARD_LISTEN="$LISTEN" BOTGUARD_PUBLIC_ENDPOINT="$PUBLIC_ENDPOINT" \
          BOTGUARD_NO_START="$((1 - START))" "$PM" install -y botguard
      else
        $SUDO "$PM" install -y botguard && $SUDO "$PM" upgrade -y botguard
      fi
      ;;
    *) die "软件源只支持 apt 和 dnf/yum；这台机器请用 --method binary" ;;
  esac
  if [ "$first" -eq 0 ]; then UPGRADED=1; fi
}

install_docker() {
  has docker || die "需要 Docker：https://docs.docker.com/engine/install/"
  # 目录要 root 才能写时，文件（含 0600 的 .env）属于 root，docker compose 也要用 root 执行才读得到。
  FSUDO=""
  if ! mkdir -p "$INSTALL_DIR" 2>/dev/null || [ ! -w "$INSTALL_DIR" ]; then FSUDO="$SUDO"; fi
  DOCKER=docker
  if [ -n "$FSUDO" ] || ! docker info >/dev/null 2>&1; then DOCKER="$SUDO docker"; fi
  $DOCKER compose version >/dev/null 2>&1 || die "需要 Docker Compose 插件（docker compose）"
  resolve_tag
  ref="$TAG"
  compose="$INSTALL_DIR/compose.yml"
  envf="$INSTALL_DIR/.env"
  $FSUDO install -d -m 0755 "$INSTALL_DIR"
  say "下载 compose.yml（$ref）"
  curl -fsSL "$RAW_BASE/$ref/deploy/docker/compose.yml" | $FSUDO tee "$compose" >/dev/null || die "下载 compose.yml 失败"
  if $FSUDO test -e "$envf"; then
    say "$envf 已存在，更新镜像版本为 ${TAG#v}"
    # 不用 sed -i（BSD 和 GNU 写法不同）；cat 写回保留原文件的 0600 权限。
    $FSUDO sh -c "umask 077 && sed 's|^BOTGUARD_VERSION=.*|BOTGUARD_VERSION=${TAG#v}|' \"\$1\" > \"\$1.tmp\" && cat \"\$1.tmp\" > \"\$1\" && rm -f \"\$1.tmp\"" sh "$envf"
    UPGRADED=1
  else
    ask_settings
    key() { head -c 32 /dev/urandom | base64 | tr -d '\n'; }
    $FSUDO sh -c 'umask 077 && cat > "$1"' sh "$envf" <<ENV
# 由 install.sh 生成。两个密钥生成后不要改：KEK 改了已有的应用密钥对就解不开了。
BOTGUARD_VERSION=${TAG#v}
BOTGUARD_LISTEN=$LISTEN
BOTGUARD_CHALLENGE_KEY_K1=$(key)
BOTGUARD_SECRET_KEK=k1:$(key)
BOTGUARD_SECRET_KEK_PREVIOUS=
BOTGUARD_PUBLIC_ENDPOINT=$PUBLIC_ENDPOINT
ENV
    say "已生成 $envf（随机密钥）"
  fi
  if [ "$START" -eq 0 ]; then
    $DOCKER compose -f "$compose" --env-file "$envf" pull
    return 0
  fi
  say "启动容器"
  $DOCKER compose -f "$compose" --env-file "$envf" up -d --wait
  if [ "$($DOCKER compose -f "$compose" --env-file "$envf" exec -T admin-web botguard-ctl admin list 2>/dev/null | wc -l)" -le 1 ]; then
    say "创建管理员 $ADMIN_USER（初始密码只显示这一次，请立即记下，首次登录后修改）"
    $DOCKER compose -f "$compose" --env-file "$envf" exec -T admin-web botguard-ctl admin create -username "$ADMIN_USER" ||
      warn "创建管理员失败，稍后执行: docker compose -f $compose exec admin-web botguard-ctl admin create -username $ADMIN_USER"
  fi
}

# ---------- 收尾 ----------

summary() {
  echo
  if [ "${UPGRADED:-0}" -eq 1 ]; then
    say "BotGuard 已升级完成"
  else
    say "BotGuard 安装完成（方式: $METHOD）"
  fi
  if [ "$METHOD" = docker ]; then
    cat <<EOF
  文件目录   $INSTALL_DIR（compose.yml、.env）
  查看状态   cd "$INSTALL_DIR" && docker compose ps
  查看日志   docker compose logs -f gateway
  升级       重新执行本安装命令，或 cd "$INSTALL_DIR" && docker compose pull && docker compose up -d
EOF
  elif [ "$OS" = Darwin ]; then
    cat <<EOF
  程序目录   $INSTALL_DIR
  配置目录   $ETC_DIR（gateway.yaml、admin-web.yaml、botguard.env）
  数据和日志 程序目录下的 data/、logs/
  查看状态   botguard status
  查看日志   botguard logs gateway -f
  启停       botguard stop / start / restart（登录后自动启动）
EOF
    case "$METHOD" in
      source) echo "  升级       botguard update" ;;
      *) echo "  升级       botguard upgrade" ;;
    esac
  else
    cat <<EOF
  配置目录   $ETC_DIR（gateway.yaml、admin-web.yaml、botguard.env）
  数据目录   /var/lib/botguard（SQLite）
  查看状态   botguard status
  查看日志   botguard logs gateway -f
EOF
    case "$METHOD" in
      package) echo "  升级       sudo apt-get install --only-upgrade botguard 或 sudo dnf upgrade botguard" ;;
      source) echo "  升级       sudo botguard update" ;;
      *) echo "  升级       sudo botguard upgrade" ;;
    esac
  fi
  if [ -n "$PUBLIC_ENDPOINT" ]; then
    echo "  gateway    $PUBLIC_ENDPOINT"
  fi
  if [ "$OS" = Darwin ]; then
    cat <<EOF
  管理后台   http://127.0.0.1:8091

下一步：登录管理后台 →「应用管理 → 新建应用」，按「接入代码」把验证组件加到你的网站。
EOF
  else
    cat <<EOF
  管理后台   http://127.0.0.1:8091（只在本机监听；远程访问先建 SSH 隧道：
             ssh -L 8091:127.0.0.1:8091 用户@服务器，再在本地浏览器打开上面的地址）

下一步：登录管理后台 →「应用管理 → 新建应用」，按「接入代码」把验证组件加到你的网站。
生产环境建议在 gateway 前面加 HTTPS 反向代理，见 docs/deploy.md。
EOF
  fi
}

main() {
  parse_args "$@"
  detect_env
  pick_method
  UPGRADED=0
  case "$METHOD" in
    package) install_package ;;
    binary) install_binary ;;
    source) install_source ;;
    docker) install_docker ;;
  esac
  if [ "$METHOD" != docker ] && [ "$UPGRADED" -eq 0 ]; then create_admin; fi
  summary
}

# 整个脚本下载完才会执行到这里，curl | sh 中途断开不会只跑一半。
main "$@"
