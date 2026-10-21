# 服务器部署与运维

gateway 和 admin-web 是两个常驻进程，统一用 `scripts/botguard.sh` 管理：启停、迁移、看日志、升级，以及一条命令装进 systemd。装了 systemd 服务后，脚本的 start/stop/restart/status/logs 会转给 `systemctl` / `journalctl`，不会另起一份进程抢端口；没装时脚本用 `setsid` 在后台运行，日志写到 `logs/<服务>.log`。

有两种部署方式，管理命令相同：

- **发布包（推荐）**：从 [GitHub Releases](https://github.com/dkeepersun/BotGuard/releases) 下载编译好的 Linux 包（amd64 / arm64），服务器上不需要 Go 和 git，用 `upgrade` 升级。
- **源码**：在服务器上 git clone 后编译，用 `update` / `rebuild` 升级。适合要跑未发布代码的测试服。

脚本根据目录里有没有 `go.mod` 判断是哪一种，不适用的命令会直接提示该用哪个。

## 目录约定

| 位置 | 内容 |
| --- | --- |
| 程序目录（例如 `/opt/botguard`） | 发布包解压的内容或 git 仓库；程序在 `bin/`：`gateway`、`admin-web`、`botguard-ctl` |
| `/etc/botguard/gateway.yaml`、`admin-web.yaml` | 服务配置，从 `configs/` 复制后按环境修改 |
| `/etc/botguard/botguard.env` | 密钥和 DSN，权限 0600，格式见 `deploy/botguard.env.example` |
| `/etc/systemd/system/botguard-gateway.service`、`botguard-admin-web.service` | 由 `deploy/systemd/botguard.service.tmpl` 渲染 |

脚本找配置的顺序：环境变量 `BOTGUARD_CONF_DIR` / `BOTGUARD_ENV_FILE` → `/etc/botguard/` 下存在对应文件就用它 → 仓库里的 `configs/` 和 `.env`（本机开发）。`scripts/botguard.sh help` 会打印当前用的是哪一份。

## 首次部署

需要 curl，以及可访问的 MySQL 和 Redis。用部署用户（不必是 root）执行，需要 root 的步骤脚本会自己调用 `sudo`。

第一步，放好程序，二选一：

```bash
# 发布包：把 0.2.0 换成要装的版本，arm64 机器把 amd64 换成 arm64
sudo install -d -o "$USER" /opt/botguard && cd /opt/botguard
curl -fLO https://github.com/dkeepersun/BotGuard/releases/download/v0.2.0/botguard_0.2.0_linux_amd64.tar.gz
curl -fLO https://github.com/dkeepersun/BotGuard/releases/download/v0.2.0/checksums.txt
sha256sum -c --ignore-missing checksums.txt
tar xzf botguard_0.2.0_linux_amd64.tar.gz --strip-components=1 && rm botguard_*.tar.gz checksums.txt

# 或者源码：需要 Go（版本见 README「本地运行」）和 git
git clone https://github.com/dkeepersun/BotGuard.git /opt/botguard && cd /opt/botguard
scripts/botguard.sh build
```

想确认下载的包确实由本仓库的发布工作流构建，可以在装了 gh 的机器上执行 `gh attestation verify botguard_0.2.0_linux_amd64.tar.gz -R dkeepersun/BotGuard`。

第二步，配置、建库、装服务（两种方式相同）：

```bash
sudo install -d -m 0755 /etc/botguard
sudo install -m 0644 configs/gateway.yaml configs/admin-web.yaml /etc/botguard/
sudo install -m 0600 deploy/botguard.env.example /etc/botguard/botguard.env
sudo vi /etc/botguard/botguard.env      # 填 DSN、challenge 密钥、KEK、公开地址
sudo vi /etc/botguard/gateway.yaml      # Redis 地址、TrustedProxies（前面有 nginx 时填 127.0.0.1/32）
sudo vi /etc/botguard/admin-web.yaml    # 同上；经 HTTPS 反代访问时 CookieSecure: true

scripts/botguard.sh migrate
scripts/botguard.sh ctl admin create -username admin   # 首个管理员，初始密码只显示一次

scripts/botguard.sh install-systemd      # 安装两个服务、设为开机自启并启动
```

`install-systemd` 的参数：

- `--user <用户>` / `--group <组>`：服务的运行身份，默认是执行脚本的用户（经 sudo 时取 `SUDO_USER`）。这个用户要能读程序目录下的 `bin/`；`botguard.env` 由 systemd 以 root 身份读取，可以保持 0600。
- `--no-start`：只安装和设为开机自启，不启动。

重复执行 `install-systemd` 是安全的：它会覆盖单元文件、`daemon-reload` 并重启服务，可以用来改运行用户或配置路径。安装前如果有脚本方式启动的进程，会先停掉。机器上已有手写的同名单元文件时也会被覆盖，ExecStart 改为指向当前程序目录的 `bin/`。

卸载：`scripts/botguard.sh uninstall-systemd`，只停止、禁用并删除两个单元文件，不动二进制、配置和日志。

## 日常运维

```bash
scripts/botguard.sh status              # 版本，两个服务的状态、PID 和 /readyz
scripts/botguard.sh restart             # 重启全部；restart gateway 只重启一个
scripts/botguard.sh stop / start
scripts/botguard.sh logs gateway -n 300 -f
scripts/botguard.sh ctl app show -app app_xxx   # 任意 botguard-ctl 命令，自动加载 botguard.env
```

升级（发布包）：

```bash
scripts/botguard.sh upgrade             # 升级到最新版
scripts/botguard.sh upgrade v0.2.1      # 升级到指定版本
```

`upgrade` 下载对应架构的包和 `checksums.txt`，校验不通过就放弃，不改任何文件；通过后替换 `bin/`、`scripts/botguard.sh`、单元模板和文档（不动 `configs/`），再迁移、重启。替换用改名完成，运行中的服务和正在执行的脚本都不受影响。新版本对配置有要求时会写在 Release 说明里，按说明修改 `/etc/botguard/` 下的文件。

升级（源码）：

```bash
scripts/botguard.sh update              # git pull --ff-only → 编译 → 迁移 → 重启
scripts/botguard.sh rebuild             # 代码已经就位（例如手动 checkout 了某个版本）时
scripts/botguard.sh rebuild admin-web   # 只换管理后台
```

`rebuild` 的顺序和 README「升级」一致：先编译（编译失败直接退出，运行中的服务不受影响）、再 `botguard-ctl migrate up`、最后重启。二进制先写临时文件再改名替换，不会出现半个文件。确定没有新迁移时可以设 `BOTGUARD_SKIP_MIGRATE=1` 跳过迁移（`upgrade` 同样适用）。

迁移只向前，不支持降级。出问题时先修复后发新版本，不要 `upgrade` 回旧版本。

每次 start/restart 后脚本会等待服务的 `/healthz` 最多 20 秒并报告结果；`status` 额外显示 `/readyz`，库结构落后时这里是 `schema_outdated`。

直接用 systemd 也可以，效果相同：

```bash
sudo systemctl restart botguard-gateway botguard-admin-web
sudo systemctl status botguard-gateway
journalctl -u botguard-gateway -f
```

服务崩溃后 systemd 在 5 秒后自动拉起；2 分钟内连续失败 5 次就停止重试（通常是配置或密钥错误），修好后 `scripts/botguard.sh restart` 即可。自动拉起不会重新编译。

## 前置 nginx

gateway 和 admin-web 建议都放在 nginx 后面，由 nginx 负责 TLS、慢速客户端和第一层限流。程序本身也有兜底：请求头必须在 5 秒内收完、整个请求 10 秒内收完、请求头不超过 64 KiB、空闲连接 120 秒后关闭；按 IP 的限流在 Redis 计数，已经超限的 IP 在本进程内直接拒绝，不再访问 Redis；库里不存在的 app_key 和 secret_id 每个进程每秒最多查 20 次 MySQL（每 10 秒从库里加载一次已有 key 的清单，新建的 key 最多 10 秒后可用，和应用缓存一致）。

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

## 注意

- `botguard.env` 里不要写 `export`：systemd 的 EnvironmentFile 不认，带 `export` 的行会被忽略，服务因缺少密钥起不来。`install-systemd` 发现这种行会拒绝安装并给出修正命令。
- 代码目录不是 git 仓库（例如 rsync 上传）时，脚本会自动加 `-buildvcs=false` 编译，管理后台「系统信息」里的程序版本显示为“开发构建”；`update` 需要 git 仓库。各程序都可以用 `-version`（botguard-ctl 用 `version`）查看版本。
- 单元文件开启了 `ProtectSystem=full`（`/usr`、`/etc` 只读）和 `NoNewPrivileges`，两个服务都不需要写本地文件。
