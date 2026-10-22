# 部署与运维

安装方法见 [install.md](install.md)。本文说明 Linux 服务器上安装后的目录、配置修改、日常运维、升级和反向代理，以及不用安装脚本的手动部署。macOS 本机安装的目录和区别见 [install.md 的 macOS 一节](install.md#macos)，管理命令相同。

## 目录与文件

除 Docker 外，各种安装方式的目录结构相同：

| 位置 | 内容 |
| --- | --- |
| `/opt/botguard/` | 程序：`bin/`（gateway、admin-web、botguard-ctl）、`scripts/botguard.sh`、配置样例 `configs/` |
| `/usr/local/bin/botguard`（deb/rpm 包为 `/usr/bin/botguard`） | 指向 `scripts/botguard.sh` 的链接，下文的 `botguard` 命令 |
| `/etc/botguard/gateway.yaml`、`admin-web.yaml` | 服务配置 |
| `/etc/botguard/botguard.env` | 密钥和数据库连接，权限 0600，格式见 `deploy/botguard.env.example` |
| `/var/lib/botguard/botguard.db` | SQLite 数据库，只有 `botguard` 用户可读写 |
| `/etc/systemd/system/botguard-gateway.service`、`botguard-admin-web.service` | systemd 服务，由 `deploy/systemd/botguard.service.tmpl` 生成 |
| journald | 日志：`botguard logs gateway` 或 `journalctl -u botguard-gateway` |

两个服务都以 `botguard` 系统用户运行。`botguard ctl` 也会切换到这个用户执行，因此用 root 运行命令不会产生服务读写不了的文件。

`botguard` 查找配置的顺序：环境变量 `BOTGUARD_CONF_DIR` / `BOTGUARD_ENV_FILE`，然后是 `/etc/botguard/`，最后是程序目录下的 `configs/` 和 `.env`（本机开发用）。`botguard help` 会显示当前使用的是哪一份。

## 修改配置

修改 `/etc/botguard/` 下的文件后执行 `sudo botguard restart` 生效。常见修改：

| 需求 | 做法 |
|---|---|
| 更换公开地址或监听地址 | `sudo botguard init --public-endpoint https://captcha.example.com --listen 127.0.0.1` |
| gateway 前面有 nginx | `gateway.yaml` 的 `TrustedProxies` 填 nginx 的地址，同机时填 `127.0.0.1/32`，见下文「前置 nginx」 |
| 使用其他机器上的 Redis | 两个 yaml 的 `Redis.Host` |
| Redis 设了密码（`requirepass`） | `botguard.env` 的 `BOTGUARD_REDIS_PASSWORD`。Docker 部署由安装脚本自动生成；本机安装用的是系统自带的 Redis，脚本不改它的配置，和其他程序或其他用户共用时建议自己设上 |
| 使用 MySQL | `botguard.env` 的 `BOTGUARD_DB_DRIVER='mysql'`、`BOTGUARD_DB_DSN='user:pass@tcp(host:3306)/botguard'`，再执行 `sudo botguard migrate` |
| 调整限流、验证难度、数据保留天数 | `gateway.yaml` 的 `RateLimit`、`Risk`，`admin-web.yaml` 的 `Cleanup`，每一项都有注释 |

`botguard init` 只会创建缺少的文件，已有的文件只修改参数里指定的项，密钥不会被重新生成。

**不要修改 `BOTGUARD_SECRET_KEK`。** 数据库里的应用密钥用它加密，改了之后已有的密钥对全部无法使用。确实需要更换时，按下文「更换 KEK」操作。

## 日常运维

```bash
botguard status                    # 版本、两个服务的状态和 /readyz
sudo botguard restart              # 重启全部；restart gateway 只重启一个
sudo botguard stop / start
botguard logs gateway -n 300 -f    # 查看日志
sudo botguard ctl app list         # 执行任意 botguard-ctl 命令，自动加载 botguard.env
sudo botguard ctl admin reset-password -username admin
```

也可以直接使用 systemd：

```bash
sudo systemctl restart botguard-gateway botguard-admin-web
journalctl -u botguard-gateway -f
```

服务崩溃后 systemd 会在 5 秒后自动拉起；2 分钟内连续失败 5 次就停止重试（通常是配置或密钥错误），修复后执行 `sudo botguard restart`。

`status` 中 `/readyz` 返回的状态：`ready` 表示正常；`store_unavailable` 表示数据库或 Redis 不可用；`schema_outdated` 表示库结构比程序旧（MySQL 部署忘记迁移）。

数据和日志不会无限增长：管理后台每小时清理过期的审计日志和已失效的密钥对，Redis 中的数据都会自动过期，日志由 journald 轮转。

### 备份

需要备份的只有两样：`/etc/botguard/`（尤其是 `botguard.env`）和数据库。SQLite 可以在线备份：

```bash
sudo -u botguard sqlite3 /var/lib/botguard/botguard.db ".backup '/var/lib/botguard/backup.db'"
```

没有 `sqlite3` 命令时，先 `sudo botguard stop`，再复制 `botguard.db` 文件。Redis 中只有限流计数、一次性 token 和统计等临时数据，不需要备份。

## 升级

| 安装方式 | 命令 |
|---|---|
| deb / rpm 包 | `sudo apt-get install --only-upgrade botguard` 或 `sudo dnf upgrade botguard` |
| 发布包 | `sudo botguard upgrade`（最新版）或 `sudo botguard upgrade v0.3.1` |
| 源码 | `sudo botguard update`（git pull、编译、迁移、重启） |
| Docker | `docker compose pull && docker compose up -d` |

升级会自动完成：校验下载文件、替换程序、迁移数据库、重启服务。配置和数据保持不变。新版本需要修改配置时，会写在 Release 说明里。

- `upgrade` 先校验 `checksums.txt`，不通过就放弃，不改任何文件；文件通过改名替换，运行中的服务不受影响。
- 迁移只向前，不支持降级。出问题时请等待修复版本，不要回退到旧版本。
- SQLite 部署在服务启动时自动迁移。MySQL 部署由 `upgrade` / `update` 先执行 `botguard-ctl migrate up`，再重启服务；库结构落后时 gateway 的 `/readyz` 返回 `schema_outdated`，不会接收流量。确定没有新迁移时，可以设 `BOTGUARD_SKIP_MIGRATE=1` 跳过。
- 多台 gateway 共用一个 MySQL 时，先迁移，再逐台替换 gateway。迁移完成到旧 gateway 下线之间，旧版本可能不认识新的配置值（例如旧版会把 `interactive` 模式当作无感处理），这段时间应尽量短。

### 更换 KEK

1. 生成新值：`echo "k1:$(openssl rand -base64 32)"`（`k1` 换成新的编号，例如 `k2`）。
2. 在 `botguard.env` 中把旧值移到 `BOTGUARD_SECRET_KEK_PREVIOUS`，新值写进 `BOTGUARD_SECRET_KEK`，然后 `sudo botguard restart`。
3. 在管理后台或用 `botguard ctl secret rotate` 轮换所有应用的密钥对。
4. 全部轮换完成后，清空 `BOTGUARD_SECRET_KEK_PREVIOUS` 并重启。

管理员的动态码（TOTP）密钥也用 KEK 加密。清空旧值之前，开启了动态码的管理员需要重新设置动态码，否则这些账号的动态码将无法验证。

## 运行监控

gateway 自己统计接口请求、错误码（限流 429、服务不可用 503 等）、图片题降级次数和题库余量，有两种看法：

- **管理后台「数据分析 → 运行状况」**：每台 gateway 每分钟往 Redis 写一条摘要（保留 24 小时），页面显示近 60 分钟的趋势、各实例的最近上报时间（超过 3 分钟没有上报标为失联）和异常记录。多台 gateway 共用一个 Redis 即可，不用额外部署。
- **Prometheus 指标**：`gateway.yaml` 的 `DevServer` 在 `127.0.0.1:6470/metrics` 导出 `botguard_gateway_requests_total`、`botguard_gateway_errors_total{code}`、`botguard_gateway_degraded_total{cause}`、`botguard_gateway_puzzle_pool_ready{kind}`，以及 go-zero 自带的 `http_server_requests_*` 和 Go 运行时指标。这个端口没有认证，只监听本机或内网地址；pprof 默认关闭。同一台机器跑多个 gateway 时给每个配不同端口。

**异常记录**：某一分钟出现 503、出现降级，或限流拒绝达到 `Monitor.RateLimitedPerMinute`（默认 300）时，记一条异常，显示在「运行状况」页，并写入 gateway 的错误日志（日志目录下的 `error.log`，级别 `alert`，内容以 `health anomaly` 开头）。目前只记录，不发送通知。

| 异常 | 常见原因 | 处理 |
| --- | --- | --- |
| 服务不可用（503） | Redis 或 MySQL 故障、连接数打满、慢查询 | 看「系统状态」的依赖检查和 gateway 日志 |
| 容量降级 | 某个应用超过 `RateLimit.InteractivePerAppPerMinute`，或图片生成满载且题库为空 | 确认是否被攻击；正常流量就调高配额、调大 `PuzzlePool` 或加机器 |
| 限流激增 | 单个 IP 刷接口，或 nginx 后面没配 `TrustedProxies`，所有用户共用一个 IP | 先检查 `TrustedProxies`；确认是攻击就在 nginx 或防火墙封 IP |

实例名默认是 `主机名:端口`，可以用 `Monitor.Instance` 指定。

## 前置 nginx

gateway 和 admin-web 建议都放在 nginx 后面，由 nginx 负责 TLS、慢速客户端和第一层限流。程序本身也有兜底：请求头必须在 5 秒内收完、整个请求 10 秒内收完、请求头不超过 64 KiB、空闲连接 120 秒后关闭；按 IP 的限流在 Redis 计数，已经超限的 IP 在本进程内直接拒绝，不再访问 Redis；库里不存在的 app_key 和 secret_id 每个进程每秒最多查 20 次数据库（每 10 秒从库里加载一次已有 key 的清单，新建的 key 最多 10 秒后可用，和应用缓存一致）。

必须同时配置 `gateway.yaml` 的 `TrustedProxies`（nginx 与 gateway 同机时填 `127.0.0.1/32`）。不配时所有用户的 IP 都是 nginx 的地址，共用一份限流额度，几个用户就会把所有人限住。

参考配置（gateway，按实际流量调整数值）：

```nginx
# http 块
limit_req_zone  $binary_remote_addr zone=bg_req:20m rate=20r/s;
limit_conn_zone $binary_remote_addr zone=bg_conn:20m;

upstream botguard_gateway {
    server 127.0.0.1:8090;
    keepalive 64;
}

# server 块
client_header_timeout 10s;
client_body_timeout   10s;
send_timeout          10s;
client_max_body_size  64k;     # gateway 最大请求体 32 KiB

location / {
    limit_req  zone=bg_req burst=40 nodelay;
    limit_conn bg_conn 20;
    limit_req_status  429;
    limit_conn_status 429;

    proxy_pass http://botguard_gateway;
    proxy_http_version 1.1;
    proxy_set_header Connection "";
    proxy_set_header Host $host;
    proxy_set_header X-Forwarded-For $remote_addr;   # 前面还有 CDN 时改用 $proxy_add_x_forwarded_for，并把 CDN 网段加进 TrustedProxies
    proxy_connect_timeout 2s;
    proxy_read_timeout    10s;
}
```

nginx 的 `limit_req` 只是粗粒度的第一道闸，按接口的精确额度仍由 gateway 的 `RateLimit` 决定，所以 nginx 的值要比 gateway 宽松，避免先于 gateway 拒绝正常用户。upstream 的 `keepalive_timeout`（默认 60 秒）要小于 gateway 的 120 秒空闲超时，否则可能偶发 502。

## 手动部署

不使用安装脚本时，按以下步骤部署（也适用于无法访问 GitHub 的服务器：在其他机器上下载发布包后拷贝过去）。需要 root 或 sudo。

```bash
# 1. 放好程序。把 0.3.0 换成要安装的版本，arm64 机器把 amd64 换成 arm64
sudo install -d /opt/botguard && cd /opt/botguard
sudo curl -fLO https://github.com/dkeepersun/BotGuard/releases/download/v0.3.0/botguard_0.3.0_linux_amd64.tar.gz
sudo curl -fLO https://github.com/dkeepersun/BotGuard/releases/download/v0.3.0/checksums.txt
sha256sum -c --ignore-missing checksums.txt
sudo tar xzf botguard_0.3.0_linux_amd64.tar.gz --strip-components=1 && sudo rm botguard_*.tar.gz checksums.txt
sudo ln -sf /opt/botguard/scripts/botguard.sh /usr/local/bin/botguard

# 2. 安装 Redis：Debian / Ubuntu 用 apt install redis-server，RHEL 系用 dnf install redis

# 3. 生成配置、创建 botguard 用户；不带 -y 时会询问监听地址和公开地址
sudo botguard init

# 4. 注册 systemd 服务并启动，创建第一个管理员
sudo botguard install-systemd --user botguard
sudo botguard ctl admin create -username admin
```

源码部署时，第 1 步改为 `sudo git clone https://github.com/dkeepersun/BotGuard.git /opt/botguard && sudo /opt/botguard/scripts/botguard.sh build`（需要 Go），之后用 `sudo botguard update` 升级。

想确认下载的包确实由本仓库的发布工作流构建，可以执行 `gh attestation verify botguard_0.3.0_linux_amd64.tar.gz -R dkeepersun/BotGuard`。

`install-systemd` 的参数：

- `--user <用户>` / `--group <组>`：服务的运行身份，默认是执行命令的用户（经 sudo 时取 `SUDO_USER`）。这个用户要能读程序目录下的 `bin/`，并能读写数据库文件；`botguard.env` 由 systemd 以 root 身份读取，保持 0600 即可。
- `--no-start`：只安装并设为开机自启，不启动。

重复执行 `install-systemd` 是安全的：会覆盖单元文件、`daemon-reload` 并重启服务，可以用来更换运行用户或配置路径。`sudo botguard uninstall-systemd` 停止、禁用并删除两个单元文件，不删除程序、配置和数据。

没有 systemd 的系统（例如 Alpine）上，`botguard start` / `stop` 以后台进程方式运行服务，日志写到程序目录的 `logs/`，不会开机自启。

## 注意

- `botguard.env` 里不要写 `export`：systemd 的 EnvironmentFile 不认，带 `export` 的行会被忽略，服务会因缺少密钥无法启动。`install-systemd` 发现这种行会拒绝安装，并给出修正命令。
- 单元文件开启了 `ProtectSystem=full`（`/usr`、`/etc` 只读）和 `NoNewPrivileges`。服务只写 `/var/lib/botguard`（systemd 的 `StateDirectory`，自动归运行用户所有）。
- 程序目录不是 git 仓库时（例如用 rsync 上传源码），编译时会自动加 `-buildvcs=false`，管理后台「系统信息」中的版本显示为“开发构建”。各程序都可以用 `-version`（botguard-ctl 用 `version`）查看版本。
