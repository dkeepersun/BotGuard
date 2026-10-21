#!/usr/bin/env bash
# 打服务端发布包：每个目标平台一个 tar.gz，外加 checksums.txt，输出到 dist/。
# .github/workflows/release.yml 在推送 vX.Y.Z 标签时调用它；本机也可以直接运行来检查打包结果。
# 说明见 docs/release.md。
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DIST_DIR="$ROOT_DIR/dist"
DEFAULT_TARGETS="linux/amd64 linux/arm64"
PROGRAMS="gateway admin-web botguard-ctl"

usage() {
  cat <<USAGE
用法: scripts/release.sh <版本> [平台...]

  版本   vX.Y.Z 或 vX.Y.Z-rc.1，与 git 标签一致
  平台   GOOS/GOARCH，默认 $DEFAULT_TARGETS

输出 dist/botguard_<版本>_<系统>_<架构>.tar.gz 和 dist/checksums.txt。
USAGE
}

die() {
  echo "错误: $*" >&2
  exit 1
}

[[ $# -ge 1 ]] || {
  usage
  exit 1
}
case "$1" in -h | --help | help)
  usage
  exit 0
  ;;
esac
VERSION="$1"
shift
[[ "$VERSION" =~ ^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.]+)?$ ]] || die "版本号格式应为 vX.Y.Z 或 vX.Y.Z-rc.1: $VERSION"
TARGETS="${*:-$DEFAULT_TARGETS}"

command -v go >/dev/null 2>&1 || die "找不到 go 命令"

sha256() {
  if command -v sha256sum >/dev/null 2>&1; then sha256sum "$@"; else shasum -a 256 "$@"; fi
}

# 包里的文件属主统一为 root，不带打包机器上的用户名。
pack() {
  if tar --version 2>/dev/null | grep -q GNU; then
    tar --owner=0 --group=0 --numeric-owner -czf "$@"
  else
    COPYFILE_DISABLE=1 tar --uid 0 --gid 0 --no-xattrs --no-mac-metadata -czf "$@"
  fi
}

rm -rf "$DIST_DIR"
mkdir -p "$DIST_DIR"
cd "$ROOT_DIR"

for target in $TARGETS; do
  [[ "$target" == */* ]] || die "平台格式应为 GOOS/GOARCH: $target"
  goos="${target%/*}"
  goarch="${target#*/}"
  name="botguard_${VERSION#v}_${goos}_${goarch}"
  stage="$DIST_DIR/$name"
  echo "==> $name"

  mkdir -p "$stage/bin" "$stage/configs" "$stage/deploy/systemd" "$stage/scripts" "$stage/docs"
  for prog in $PROGRAMS; do
    CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" go build -trimpath \
      -ldflags "-s -w -X github.com/dkeepersun/BotGuard/internal/version.Version=$VERSION" \
      -o "$stage/bin/$prog" "./cmd/$prog"
  done
  # 清单写死，deploy/ 下其他文件（例如某台服务器专用的脚本）不会被带进包里。
  cp configs/gateway.yaml configs/admin-web.yaml "$stage/configs/"
  cp deploy/systemd/botguard.service.tmpl "$stage/deploy/systemd/"
  cp deploy/botguard.env.example "$stage/deploy/"
  cp scripts/botguard.sh "$stage/scripts/"
  cp docs/deploy.md "$stage/docs/"
  cp LICENSE README.md "$stage/"
  echo "$VERSION" > "$stage/VERSION"
  chmod 0755 "$stage"/bin/* "$stage/scripts/botguard.sh"

  (cd "$DIST_DIR" && pack "$name.tar.gz" "$name")
  rm -rf "$stage"
done

(cd "$DIST_DIR" && sha256 ./*.tar.gz | sed 's| \./| |' > checksums.txt)
echo
cat "$DIST_DIR/checksums.txt"
