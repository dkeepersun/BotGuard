# 服务器部署与运维

gateway 和 admin-web 是两个常驻进程，统一用 `scripts/botguard.sh` 管理：编译、启停、迁移、看日志，以及一条命令装进 systemd。装了 systemd 服务后，脚本的 start/stop/restart/status/logs 会转给 `systemctl` / `journalctl`，不会另起一份进程抢端口；没装时脚本用 `setsid` 在后台运行，日志写到 `logs/<服务>.log`。

## 目录约定

| 位置 | 内容 |
| --- | --- |
| 代码目录（例如 `/opt/botguard/src`） | git 仓库；编译产物在 `bin/`：`gateway`、`admin-web`、`botguard-ctl` |
| `/etc/botguard/gateway.yaml`、`admin-web.yaml` | 服务配置，从 `configs/` 复制后按环境修改 |
| `/etc/botguard/botguard.env` | 密钥和 DSN，权限 0600，格式见 `deploy/botguard.env.example` |
| `/etc/systemd/system/botguard-gateway.service`、`botguard-admin-web.service` | 由 `deploy/systemd/botguard.service.tmpl` 渲染 |

脚本找配置的顺序：环境变量 `BOTGUARD_CONF_DIR` / `BOTGUARD_ENV_FILE` → `/etc/botguard/` 下存在对应文件就用它 → 仓库里的 `configs/` 和 `.env`（本机开发）。`scripts/botguard.sh help` 会打印当前用的是哪一份。

## 首次部署

需要 Go（版本见 README「本地运行」）、git、curl，以及可访问的 MySQL 和 Redis。用部署用户（不必是 root）执行，需要 root 的步骤脚本会自己调用 `sudo`。

```bash
git clone <仓库地址> /opt/botguard/src
cd /opt/botguard/src

sudo install -d -m 0755 /etc/botguard
sudo install -m 0644 configs/gateway.yaml configs/admin-web.yaml /etc/botguard/
sudo install -m 0600 deploy/botguard.env.example /etc/botguard/botguard.env
sudo vi /etc/botguard/botguard.env      # 填 DSN、challenge 密钥、KEK、公开地址
sudo vi /etc/botguard/gateway.yaml      # Redis 地址、TrustedProxies（前面有 nginx 时填 127.0.0.1/32）
sudo vi /etc/botguard/admin-web.yaml    # 同上；经 HTTPS 反代访问时 CookieSecure: true

scripts/botguard.sh build
scripts/botguard.sh migrate
scripts/botguard.sh ctl admin create -username admin   # 首个管理员，初始密码只显示一次

scripts/botguard.sh install-systemd      # 安装两个服务、设为开机自启并启动
```

`install-systemd` 的参数：

- `--user <用户>` / `--group <组>`：服务的运行身份，默认是执行脚本的用户（经 sudo 时取 `SUDO_USER`）。这个用户要能读代码目录下的 `bin/`；`botguard.env` 由 systemd 以 root 身份读取，可以保持 0600。
- `--no-start`：只安装和设为开机自启，不启动。

重复执行 `install-systemd` 是安全的：它会覆盖单元文件、`daemon-reload` 并重启服务，可以用来改运行用户或配置路径。安装前如果有脚本方式启动的进程，会先停掉。机器上已有手写的同名单元文件时也会被覆盖，ExecStart 改为指向当前代码目录的 `bin/`。

卸载：`scripts/botguard.sh uninstall-systemd`，只停止、禁用并删除两个单元文件，不动二进制、配置和日志。

## 日常运维

```bash
scripts/botguard.sh status              # 两个服务的状态、PID 和 /readyz
scripts/botguard.sh restart             # 重启全部；restart gateway 只重启一个
scripts/botguard.sh stop / start
scripts/botguard.sh logs gateway -n 300 -f
scripts/botguard.sh ctl app show -app app_xxx   # 任意 botguard-ctl 命令，自动加载 botguard.env
```

发布新代码：

```bash
scripts/botguard.sh update              # git pull --ff-only → 编译 → 迁移 → 重启
scripts/botguard.sh rebuild             # 代码已经就位（例如手动 checkout 了某个版本）时
scripts/botguard.sh rebuild admin-web   # 只换管理后台
```

`rebuild` 的顺序和 README「升级」一致：先编译（编译失败直接退出，运行中的服务不受影响）、再 `botguard-ctl migrate up`、最后重启。二进制先写临时文件再改名替换，不会出现半个文件。确定没有新迁移时可以 `BOTGUARD_SKIP_MIGRATE=1 scripts/botguard.sh rebuild` 跳过迁移。

每次 start/restart 后脚本会等待服务的 `/healthz` 最多 20 秒并报告结果；`status` 额外显示 `/readyz`，库结构落后时这里是 `schema_outdated`。

直接用 systemd 也可以，效果相同：

```bash
sudo systemctl restart botguard-gateway botguard-admin-web
sudo systemctl status botguard-gateway
journalctl -u botguard-gateway -f
```

服务崩溃后 systemd 在 5 秒后自动拉起；2 分钟内连续失败 5 次就停止重试（通常是配置或密钥错误），修好后 `scripts/botguard.sh restart` 即可。自动拉起不会重新编译。

## 注意

- `botguard.env` 里不要写 `export`：systemd 的 EnvironmentFile 不认，带 `export` 的行会被忽略，服务因缺少密钥起不来。`install-systemd` 发现这种行会拒绝安装并给出修正命令。
- 代码目录不是 git 仓库（例如 rsync 上传）时，脚本会自动加 `-buildvcs=false` 编译，管理后台「系统信息」里的程序版本显示为 unknown；`update` 需要 git 仓库。
- 单元文件开启了 `ProtectSystem=full`（`/usr`、`/etc` 只读）和 `NoNewPrivileges`，两个服务都不需要写本地文件。
