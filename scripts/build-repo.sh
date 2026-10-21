#!/usr/bin/env bash
# 用 deb / rpm 包生成 APT 和 YUM 软件源（静态文件，发布到 GitHub Pages），并用 GPG 签名。
# .github/workflows/repo.yml 在每次正式发版后调用；说明见 docs/release.md「软件源」。
#
#   scripts/build-repo.sh <包目录> <输出目录>
#
# 需要 apt-ftparchive（apt-utils）、createrepo_c 和已导入私钥的 gpg。
# 环境变量: REPO_GPG_KEY_ID（默认取第一个私钥）、REPO_GPG_PASSPHRASE（私钥有口令时）、
#          REPO_URL（软件源地址，写进说明页和 .repo 文件）。
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
REPO_URL="${REPO_URL:-https://dkeepersun.github.io/BotGuard}"

die() {
  echo "错误: $*" >&2
  exit 1
}

[[ $# -eq 2 ]] || die "用法: scripts/build-repo.sh <包目录> <输出目录>"
SRC="$(cd "$1" && pwd)"
OUT="$2"
for c in apt-ftparchive createrepo_c gpg; do
  command -v "$c" >/dev/null 2>&1 || die "找不到 $c"
done
compgen -G "$SRC/*.deb" >/dev/null || die "$SRC 里没有 .deb"
compgen -G "$SRC/*.rpm" >/dev/null || die "$SRC 里没有 .rpm"

KEY_ID="${REPO_GPG_KEY_ID:-$(gpg --list-secret-keys --with-colons | awk -F: '/^sec/{print $5; exit}')}"
[[ -n "$KEY_ID" ]] || die "gpg 里没有私钥"
GPG=(gpg --batch --yes --local-user "$KEY_ID")
if [[ -n "${REPO_GPG_PASSPHRASE:-}" ]]; then
  GPG+=(--pinentry-mode loopback --passphrase "$REPO_GPG_PASSPHRASE")
fi

rm -rf "$OUT"
mkdir -p "$OUT/deb" "$OUT/rpm"
OUT="$(cd "$OUT" && pwd)"

# APT 平铺源：deb [signed-by=...] $REPO_URL/deb ./
cp "$SRC"/*.deb "$OUT/deb/"
(
  cd "$OUT/deb"
  apt-ftparchive packages . > Packages
  gzip -9kn Packages
  apt-ftparchive \
    -o APT::FTPArchive::Release::Origin=BotGuard \
    -o APT::FTPArchive::Release::Label=BotGuard \
    -o APT::FTPArchive::Release::Suite=stable \
    -o APT::FTPArchive::Release::Description="BotGuard packages" \
    release . > Release
  "${GPG[@]}" --clearsign -o InRelease Release
  "${GPG[@]}" --armor --detach-sign -o Release.gpg Release
)

# YUM / DNF 源：包本身由 nfpm 签名，这里再签名元数据（repo_gpgcheck）。
cp "$SRC"/*.rpm "$OUT/rpm/"
createrepo_c --quiet "$OUT/rpm"
"${GPG[@]}" --armor --detach-sign -o "$OUT/rpm/repodata/repomd.xml.asc" "$OUT/rpm/repodata/repomd.xml"

gpg --armor --export "$KEY_ID" > "$OUT/gpg.key"
cp "$ROOT_DIR/scripts/install.sh" "$OUT/install.sh"
cat > "$OUT/botguard.repo" <<REPO
[botguard]
name=BotGuard
baseurl=$REPO_URL/rpm
enabled=1
gpgcheck=1
repo_gpgcheck=1
gpgkey=$REPO_URL/gpg.key
REPO

version="$(dpkg-deb -f "$(ls "$OUT"/deb/*.deb | head -1)" Version 2>/dev/null || echo "")"
cat > "$OUT/index.html" <<HTML
<!doctype html>
<html lang="zh-CN">
<head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>BotGuard 软件源</title>
<style>body{font:15px/1.6 system-ui,sans-serif;max-width:760px;margin:40px auto;padding:0 16px;color:#222;background:#fff}pre{background:#f4f4f4;padding:12px;overflow-x:auto}@media (prefers-color-scheme:dark){body{color:#ddd;background:#111}pre{background:#222}}</style>
</head>
<body>
<h1>BotGuard 软件源</h1>
<p>当前版本 ${version:-未知}。项目主页：<a href="https://github.com/dkeepersun/BotGuard">github.com/dkeepersun/BotGuard</a></p>
<h2>一键安装</h2>
<pre>curl -fsSL $REPO_URL/install.sh | sh</pre>
<h2>Debian / Ubuntu</h2>
<pre>sudo install -d -m 0755 /etc/apt/keyrings
curl -fsSL $REPO_URL/gpg.key | sudo tee /etc/apt/keyrings/botguard.asc &gt;/dev/null
echo "deb [signed-by=/etc/apt/keyrings/botguard.asc] $REPO_URL/deb ./" | sudo tee /etc/apt/sources.list.d/botguard.list
sudo apt-get update &amp;&amp; sudo apt-get install botguard</pre>
<h2>RHEL / Rocky / Alma / Fedora</h2>
<pre>sudo curl -fsSL -o /etc/yum.repos.d/botguard.repo $REPO_URL/botguard.repo
sudo dnf install botguard</pre>
</body>
</html>
HTML

echo "软件源已生成: $OUT"
find "$OUT" -maxdepth 2 -type f | sort
