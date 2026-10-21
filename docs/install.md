# 安装

BotGuard 服务端由两个常驻服务组成：**gateway**（对外提供验证接口）和 **admin-web**（管理后台）。另有命令行工具 **botguard-ctl**。数据库默认用内嵌的 SQLite，唯一的外部依赖是 Redis，安装脚本会自动装好。

## 系统要求

- Linux x86_64 或 arm64，推荐 Debian 11+、Ubuntu 20.04+、RHEL / Rocky / Alma 8+
- 1 核 CPU、512 MB 内存起步
- Redis（安装脚本自动安装发行版自带的版本）
- root 权限或 sudo

## 一键安装（推荐）

```bash
curl -fsSL https://raw.githubusercontent.com/dkeepersun/BotGuard/main/scripts/install.sh | sh
```

脚本会依次完成：

1. 安装 Redis（本机已有就跳过）；
2. 下载程序并校验 SHA-256；
3. 创建系统用户 `botguard`，生成配置和随机密钥（`/etc/botguard/`）；
4. 注册 systemd 服务 `botguard-gateway`、`botguard-admin-web`，设为开机自启并启动；
5. 创建管理员 `admin`，**初始密码只显示这一次**。

安装过程中会问两个问题，直接回车使用默认值：

| 问题 | 默认值 | 说明 |
|---|---|---|
| 监听地址 | `0.0.0.0` | gateway 直接对外提供服务填 `0.0.0.0`；放在 nginx 等反向代理后面填 `127.0.0.1` |
| 公开地址 | `http://服务器IP:8090` | 浏览器和 SDK 访问 gateway 的地址，会写进接入代码。用了 HTTPS 反向代理就填 `https://你的域名` |

重复执行同一条命令是安全的：已经安装时会改为升级，配置和数据保持不变。

### 安装选项

在命令末尾加 `-s -- 选项`：

```bash
curl -fsSL https://raw.githubusercontent.com/dkeepersun/BotGuard/main/scripts/install.sh | sh -s -- --method docker
```

| 选项 | 说明 |
|---|---|
| `--method 方式` | `auto`（默认）、`package`、`binary`、`docker`、`source`，见下文 |
| `--version vX.Y.Z` | 安装指定版本，默认最新版 |
| `--public-endpoint 地址` | gateway 公开地址，例如 `https://captcha.example.com` |
| `--listen 地址` | gateway 监听地址，`0.0.0.0` 或 `127.0.0.1` |
| `--dir 目录` | binary / source / docker 的安装目录，默认 `/opt/botguard` |
| `--admin 用户名` | 第一个管理员的用户名，默认 `admin` |
| `--no-redis` | 不安装 Redis（使用其他机器上的 Redis 时） |
| `--no-start` | 只安装，不启动 |
| `-y` | 不提问，全部使用默认值或参数给定的值 |

无人值守安装示例：

```bash
curl -fsSL https://raw.githubusercontent.com/dkeepersun/BotGuard/main/scripts/install.sh \
  | sh -s -- -y --listen 127.0.0.1 --public-endpoint https://captcha.example.com
```

## 安装方式

`--method auto` 会自动选择：Debian / Ubuntu / RHEL 系且能访问 BotGuard 软件源时用 `package`，否则用 `binary`。

| 方式 | 适合 | 升级命令 |
|---|---|---|
| `package`：deb / rpm 包 | 有 apt 或 dnf 的服务器，和系统软件一起管理 | `sudo apt upgrade` / `sudo dnf upgrade` |
| `binary`：发布包 | 任意 Linux，包括没有软件源的环境 | `sudo botguard upgrade` |
| `docker`：Docker Compose | 已在用 Docker 的环境；手动部署时也能在 macOS、Windows 上运行 | 重新执行安装命令 |
| `source`：源码编译 | 需要运行未发布代码的测试环境 | `sudo botguard update` |

除 Docker 外，其余方式安装后的目录结构和管理命令都相同，见 [deploy.md](deploy.md)。

### 软件源（apt / dnf）

不用安装脚本，也可以直接添加软件源。

Debian / Ubuntu：

```bash
sudo install -d -m 0755 /etc/apt/keyrings
curl -fsSL https://dkeepersun.github.io/BotGuard/gpg.key | sudo tee /etc/apt/keyrings/botguard.asc >/dev/null
echo "deb [signed-by=/etc/apt/keyrings/botguard.asc] https://dkeepersun.github.io/BotGuard/deb ./" \
  | sudo tee /etc/apt/sources.list.d/botguard.list
sudo apt-get update && sudo apt-get install botguard
sudo botguard ctl admin create -username admin
```

RHEL / Rocky / Alma / Fedora：

```bash
sudo curl -fsSL -o /etc/yum.repos.d/botguard.repo https://dkeepersun.github.io/BotGuard/botguard.repo
sudo dnf install botguard
sudo botguard ctl admin create -username admin
```

deb 包把 `redis-server` 列为推荐依赖，apt 会一并安装；rpm 包推荐 `redis`，dnf 默认也会安装。发行版没有 `redis` 包时（例如改用 valkey 的新版本），请先自行安装 Redis 或 valkey；一键安装脚本会自动处理。

也可以从 [GitHub Releases](https://github.com/dkeepersun/BotGuard/releases) 下载 `.deb` / `.rpm` 文件直接安装：`sudo apt install ./botguard_*.deb`。

### Docker

```bash
curl -fsSL https://raw.githubusercontent.com/dkeepersun/BotGuard/main/scripts/install.sh | sh -s -- --method docker
```

脚本在 `/opt/botguard` 下生成 `compose.yml` 和 `.env`（随机密钥），启动 Redis、gateway、admin-web 三个容器，并创建管理员。不想用脚本时，可以手动部署：

```bash
mkdir botguard && cd botguard
curl -fsSLO https://raw.githubusercontent.com/dkeepersun/BotGuard/main/deploy/docker/compose.yml
cat > .env <<EOF
BOTGUARD_CHALLENGE_KEY_K1=$(openssl rand -base64 32)
BOTGUARD_SECRET_KEK=k1:$(openssl rand -base64 32)
BOTGUARD_PUBLIC_ENDPOINT=http://localhost:8090
EOF
chmod 600 .env
docker compose up -d
docker compose exec admin-web botguard-ctl admin create -username admin
```

镜像为 `ghcr.io/dkeepersun/botguard`，同时包含 gateway、admin-web 和 botguard-ctl。数据保存在卷 `botguard-data`（SQLite）和 `redis-data` 中。`.env` 里可以设置的其他变量：

| 变量 | 默认值 | 说明 |
|---|---|---|
| `BOTGUARD_VERSION` | `latest` | 镜像版本，例如 `0.3.0` |
| `BOTGUARD_LISTEN` | `0.0.0.0` | gateway 端口绑定的宿主机地址 |
| `BOTGUARD_PORT` | `8090` | gateway 对外端口 |
| `BOTGUARD_ADMIN_PORT` | `8091` | 管理后台端口（只绑定 127.0.0.1） |

升级：`docker compose pull && docker compose up -d`。

### 源码

需要 Go（版本见 `go.mod`）和 git：

```bash
curl -fsSL https://raw.githubusercontent.com/dkeepersun/BotGuard/main/scripts/install.sh | sh -s -- --method source
```

等同于 `git clone` 到 `/opt/botguard` 后执行 `scripts/botguard.sh build`、`init`、`install-systemd`。

## 安装之后

1. **登录管理后台**。管理后台只监听本机 `127.0.0.1:8091`。在自己电脑上建一条 SSH 隧道，再用浏览器打开 <http://127.0.0.1:8091>：

   ```bash
   ssh -L 8091:127.0.0.1:8091 用户@服务器
   ```

   首次登录要修改初始密码。忘记密码时执行 `sudo botguard ctl admin reset-password -username admin`。

2. **配置 HTTPS**（生产环境）。网站是 HTTPS 时，浏览器只能访问 HTTPS 的 gateway。参考 [deploy.md 的 nginx 配置](deploy.md#前置-nginx)，然后更新公开地址：

   ```bash
   sudo botguard init --listen 127.0.0.1 --public-endpoint https://captcha.example.com
   sudo botguard restart
   ```

3. **接入网站**：见 README「使用」一节。

## 卸载

| 方式 | 命令 |
|---|---|
| package | `sudo apt remove botguard` 或 `sudo dnf remove botguard` |
| binary / source | `sudo botguard uninstall-systemd && sudo rm -rf /opt/botguard /usr/local/bin/botguard` |
| docker | `cd /opt/botguard && docker compose down`（加 `-v` 同时删除数据卷） |

卸载不会删除配置和数据。确认不再需要后，手动删除：

```bash
sudo rm -rf /etc/botguard /var/lib/botguard
sudo userdel botguard
```

`/etc/botguard/botguard.env` 里的 KEK 用来解密数据库中的应用密钥。只保留数据库而丢失这个文件，已有的密钥对将无法使用。

## 常见问题

**服务启动失败。** 先看日志：`botguard logs gateway -n 50`。最常见的原因是 Redis 没有运行（`systemctl status redis-server` 或 `redis`）。

**无法访问 GitHub。** 发布包、脚本和软件源都托管在 GitHub。可以在能访问的机器上下载发布包，拷贝到服务器后手动部署，见 [deploy.md](deploy.md#手动部署)。

**要用 MySQL。** 把 `/etc/botguard/botguard.env` 中的 `BOTGUARD_DB_DRIVER` 改为 `mysql`，`BOTGUARD_DB_DSN` 改为 MySQL 连接串，执行 `sudo botguard migrate && sudo botguard restart`。见 [development.md](development.md#数据库sqlite-或-mysql)。
