# 发布清单

服务端程序和各个 SDK 包分开发版，各用各的标签：

| 内容 | 渠道 | 标签 | 安装方式 |
|---|---|---|---|
| 服务端（gateway、admin-web、botguard-ctl） | GitHub Releases（tar.gz、deb、rpm）、APT/YUM 软件源、GHCR 镜像 | `vX.Y.Z` | 一键安装脚本，见 [install.md](install.md) |
| `@botguard/browser` | npm | `browser-vX.Y.Z` | `npm install @botguard/browser` |
| `botguard/php-sdk` | Packagist | `php-vX.Y.Z` | `composer require botguard/php-sdk` |
| `botguard/laravel` | Packagist | `laravel-vX.Y.Z` | `composer require botguard/laravel` |
| Go SDK 及 gin / go-zero 适配 | Go Modules（直接取本仓库） | `packages/go/vX.Y.Z`、`packages/go/gin/vX.Y.Z`、`packages/go/gozero/vX.Y.Z` | `go get github.com/dkeepersun/BotGuard/packages/go` |

服务端的发布步骤见文末「四、服务端发布」，Go SDK 见「五、Go SDK」。下面先是 npm 和 Composer 的包。

| 包 | 渠道 | 安装命令 |
|---|---|---|
| `@botguard/browser` | npm | `npm install @botguard/browser` |
| `botguard/php-sdk` | Packagist | `composer require botguard/php-sdk` |
| `botguard/laravel` | Packagist | `composer require botguard/laravel` |

另外，不想用 npm 的页面可以直接引用 gateway 托管的脚本，和易盾的 `load.min.js` 一样：`<script src="https://你的gateway/sdk/v1/load.js">`。

## 当前状态（2026-10-22）

许可证均为 MIT。

| 内容 | 最新版本 | 说明 |
|---|---|---|
| 服务端 | v0.2.1 | GitHub Releases、GHCR 镜像、APT/YUM 软件源都已发布。v0.2.1 与 v0.2.0 代码相同，只是 rpm 改为签名发布 |
| `@botguard/browser` | 0.2.0 | 0.1.0、0.2.0 都是在本机手动发布的，没有 provenance |
| `botguard/php-sdk`、`botguard/laravel` | 0.1.0 | 之后只改了文档，没有发新版本 |
| Go SDK（含 gin / go-zero 适配） | v0.1.0 | 三个模块的标签都在 `4b090c6` 上，proxy.golang.org 和 sum.golang.org 都已收录；在干净目录用默认设置 `go get` 并编译示例已验证 |

- npm：[@botguard/browser](https://www.npmjs.com/package/@botguard/browser)，组织 `botguard`（所有者 dksun）。Trusted Publisher 已配置（只允许暂存发布）。`release-browser` 工作流改为暂存发布：推标签后版本进入暂存区，要人工批准才公开（见下文 npm 第 4 步）。改完后还没有实际发过一个版本：main 上的版本号已改为 0.2.1，代码与 0.2.0 相同（README 多了不经过 npm 的安装方式），用来第一次验证暂存发布，以及把同一个 tgz 放进 GitHub Release 草稿。等 npm 当前的故障（Degraded Publish, Install, and View Package）恢复后，再推 `browser-v0.2.1` 并批准。
- Packagist：[botguard/php-sdk](https://packagist.org/packages/botguard/php-sdk)、[botguard/laravel](https://packagist.org/packages/botguard/laravel)。两个镜像仓库都已装好 Packagist 的 webhook，新标签会自动收录。
- 镜像仓库：[botguard-php-sdk](https://github.com/dkeepersun/botguard-php-sdk)、[botguard-laravel](https://github.com/dkeepersun/botguard-laravel)。已关闭 issues 和 wiki；部署密钥已配置，私钥只保存在主仓库的 Actions secrets 里。
- 软件源：<https://dkeepersun.github.io/BotGuard/>，签名密钥指纹 `BE49F7E6B91F729E10F3E24A0E7787FFB93E3231`，私钥在 Actions secret `REPO_GPG_PRIVATE_KEY` 里。GitHub Pages 已启用（Source：GitHub Actions）。
- 待办：
  - 签名密钥的 UID 是生成时没改的占位文字 `BotGuard Packages <你的邮箱>`，用 `gpg --quick-add-uid` / `--quick-revoke-uid` 改为真实邮箱，重新写入 secret，再手动运行 `repo` 更新 `gpg.key`（指纹不变，已签名的包不受影响）。

第一次发布时遇到的问题，下次可以提前避开：

- 推送 `.github/workflows/` 需要 gh 登录令牌带 `workflow` 权限：`gh auth refresh -h github.com -s workflow`。在终端里要按回车，否则 gh 不会去取新令牌。
- npm 发布要求账号开启 2FA，且要先在网站上建好 `botguard` 组织，否则会报 403 或 `Scope not found`。
- `npm login`、`npm publish` 要在真正的终端里运行。在 Claude Code 里用 `!` 运行时无法交互，`npm login` 会直接报错退出。
- 发布后 npm 会自动留下一个 `0.0.0-stage` 版本，描述为 “Temporary package placeholder for staged publishing”。它不影响安装：`latest` 指向正式版本，`^0.1.0` 也不会匹配预发布版本。
- 不要补推 `browser-v0.1.0`、`browser-v0.2.0` 标签：这两个版本已经发布，工作流会因为版本已存在而失败。下一个版本从 `browser-v0.2.1` 开始。

### 发版排错（2026-10-22 发 v0.2.0 和 Go SDK 时遇到）

| 现象 | 原因 | 处理 |
|---|---|---|
| `release-browser` 在 “Tag must match package.json version” 失败 | 标签打在了改版本号的 PR 合并之前的提交上 | 先合并改版本号的 PR，再在新的 main 上打标签。这一步失败时什么都没发布，可以删掉标签重新推：`git push origin :refs/tags/browser-vX.Y.Z` |
| 工作流 `npm publish` 报 404 `PUT .../@botguard%2fbrowser - Not found` | npm 没把请求认作 Trusted Publishing：包上没配置，或配置与工作流对不上 | 核对 Trusted Publisher：仓库 `dkeepersun` / `BotGuard`（注意大小写）、工作流文件名只写 `release-browser.yml`、Environment 留空 |
| 工作流 `npm publish` 报 403 `OIDC permission denied for this action` | 新建的 Trusted Publisher 默认只允许 `npm stage publish`，不允许直接 `npm publish`（`npm help trust` 里的 `--allow-publish` / `--allow-stage-publish`） | 工作流已改为 `npm stage publish`，推标签后人工批准。不要为此打开 “Allow npm publish”：npm 建议只开放暂存发布 |
| 本机 `npm publish` 报 404 | 没登录，或账号对 `@botguard` 没有写权限，或 registry 指向了镜像 | `npm whoami`、`npm access list collaborators @botguard/browser`、`npm config get registry` |
| 本机 `npm publish` 报 409 `Cannot publish over previously staged version` | 这个版本号已进过暂存区（staged publishing），暂存和正式版本共用版本号 | `npm stage list @botguard/browser` 查看，`npm stage approve <stage-id>` 批准（需要 npm ≥ 11.15.0 和 2FA）。暂存区为空时先看 npm 上是否其实已经发布了 |
| `repo` 工作流显示成功，但软件源没更新 | 读不到 `REPO_GPG_PRIVATE_KEY` 时整个构建被跳过，只留一条警告 | 不能只看结论，要看 “Build the repository” 等步骤是不是 skipped |
| 日志里 `REPO_GPG_PRIVATE_KEY:` 为空 | secret 不在 Actions 的 Repository secrets 里（例如放进了 `github-pages` 环境、Dependabot / Codespaces，或 Variables） | 存到 Settings → Secrets and variables → Actions → Repository secrets |
| `gpg: no valid OpenPGP data found` | secret 内容不是 armored 私钥（公钥、指纹、二进制或截断的内容） | 用管道写入，见下文“软件源和镜像的一次性设置”第 1 步 |
| `gpg: WARNING: nothing exported` 后 `gh secret set` 仍显示成功 | 本机没有这把密钥，secret 被写成了空值 | 先 `gpg --list-secret-keys` 确认密钥存在，再写入 |
| `deploy-pages` 报 `Failed to create deployment (status: 404)` | 仓库的 GitHub Pages 没有启用 | Settings → Pages → Source 选 GitHub Actions |
| `go get .../packages/go@v0.1.0` 报 `missing .../packages/go/go.mod at revision packages/go/v0.1.0` | 标签打在了合并 Go SDK 之前的提交上 | 先合并，再按提交号打标签（见「五、Go SDK」第 1 步）。这个版本从没成功下载过时，sum.golang.org 没有记录（lookup 返回 404），可以删掉标签重打 |
| 删掉重打后标签还是指向旧提交 | 本机的 `origin/main` 没有更新，`git tag ... origin/main` 又打在了旧提交上 | 用提交号打标签，先 `git cat-file -e <提交号>:packages/go/go.mod` 确认 |
| 标签已改对，proxy.golang.org / sum.golang.org 仍返回 404 `unknown revision` | 删标签到重推之间，代理来查过一次，缓存了“版本不存在” | 不要再删或重推，等缓存过期（这次不到 40 分钟）。急用时 `GOPRIVATE=github.com/dkeepersun/* go get ...` 直接从 GitHub 取 |
| 软件源可用后，`dnf install botguard` 失败 | 那个版本发布时还没有签名密钥，rpm 没签名，而软件源要求 `gpgcheck=1` | 配好密钥后发下一个补丁版本（这次就是 v0.2.1），`repo` 工作流会用签名的 rpm 重建软件源 |


## 一、只能由你完成的步骤

这些步骤涉及创建账号、登录和发布令牌，必须由你本人操作。

### 许可证

三个包都采用 MIT（2026-10-22 确定）。仓库根目录和三个包目录各有一份 `LICENSE`：镜像仓库只包含包目录，npm 也只打包包目录里的文件。

### npm

npm 已在 2025 年底撤销 classic（含 Automation）令牌；有写权限的 granular 令牌最长 90 天，默认要求 2FA。所以 CI 发布改用 **Trusted Publishing**（GitHub Actions 的 OIDC 临时凭证），仓库里不保存任何 npm 令牌。

1. 注册或登录 npmjs.com，开启 2FA，创建组织 **botguard**，这样才能使用 `@botguard/` 前缀。如果组织名已被占用，告诉我换哪个名字，我来改包名。
2. **第一次发布手动完成**，因为 Trusted Publishing 要在已存在的包上配置：在 `packages/browser` 下执行 `npm ci && npm test && npm publish --access public`，按提示输入 2FA 验证码。
3. 在 npmjs.com 打开包 `@botguard/browser` → Settings → Trusted Publisher，选择 GitHub Actions，填写仓库 `dkeepersun/BotGuard`、工作流文件 `release-browser.yml`，Environment 留空。权限只勾**暂存发布**（stage publish，新建配置的默认值），不要勾 “Allow npm publish”：这样即使 CI 被攻破，没有你的 2FA 也发不出包。之后可以在包的 Access 页面选择 “Require two-factor authentication and disallow tokens”，Trusted Publishing 不受影响。
4. 之后的版本：
   1. 改 `package.json` 的 version，PR 合并后在合并提交上推标签 `browser-vX.Y.Z`；
   2. `release-browser` 工作流检查版本号、跑测试，`npm pack` 打一次包，再做两件事：用 `npm stage publish --provenance` 把这个包放进 npm 暂存区（需要主仓库保持公开）；把同一个 `.tgz` 和它的 `.sha256` 放进 GitHub Release 草稿（`--latest=false`）。运行摘要里会写出 shasum 和下面两步的命令。这时版本**还没有公开**，`npm install` 装不到；
   3. 在本机批准（需要 npm ≥ 11.15.0 和 2FA）：

      ```bash
      npm stage list @botguard/browser
      npm stage view <stage-id>      # 核对版本号和 shasum，shasum 应与工作流日志里 npm notice 打印的一致
      npm stage approve <stage-id>
      ```

      也可以在 npmjs.com 上批准。发现问题就 `npm stage reject <stage-id>`。拒绝后这个版本号可以重新暂存，但标签已经推过，改了代码就发下一个版本号。

   4. npm 批准后，发布 GitHub Release 草稿：

      ```bash
      gh release edit browser-vX.Y.Z --repo dkeepersun/BotGuard --draft=false --latest=false
      ```

      **一定要带 `--latest=false`**：`scripts/install.sh` 和 `scripts/botguard.sh` 通过 `releases/latest` 找服务端版本，浏览器组件的 release 不能占用 “Latest”。拒绝了 npm 版本时，用 `gh release delete browser-vX.Y.Z --repo dkeepersun/BotGuard --yes` 删掉草稿。

      发布后，npm 出故障时也可以按地址安装同一个文件：`npm install https://github.com/dkeepersun/BotGuard/releases/download/browser-vX.Y.Z/botguard-browser-X.Y.Z.tgz`。

### Composer / Packagist

Packagist 只认仓库根目录的 `composer.json`，所以两个 PHP 包要先拆分到只读的镜像仓库。

1. 在 GitHub 新建两个**空的公开**仓库：`dkeepersun/botguard-php-sdk`、`dkeepersun/botguard-laravel`。
2. 为每个镜像仓库生成一对 SSH 密钥：
   - 公钥加到镜像仓库的 Deploy keys，勾选 Allow write access；
   - 私钥存到主仓库的 Actions secrets，名字分别是 `PHP_SDK_DEPLOY_KEY`、`LARAVEL_DEPLOY_KEY`。
3. 推送一次 main 分支，`split-php` 工作流会把 `packages/php`、`packages/laravel` 同步到两个镜像仓库。
4. 用 GitHub 账号登录 packagist.org，Submit 两个镜像仓库的地址。vendor 名 `botguard` 由第一个提交的人获得。
5. 在 Packagist 开启 GitHub 自动更新（用 GitHub 登录时通常会自动配置 webhook）。
6. 发布版本：推送标签 `php-v0.1.0` 和 `laravel-v0.1.0`，工作流会在镜像仓库上打 `v0.1.0` 标签，Packagist 随后收录。**先发 php-sdk，再发 laravel**，因为 laravel 包依赖 `botguard/php-sdk: ^0.1`。

**镜像仓库只读**：所有修改都在主仓库进行，不要直接改镜像仓库。

## 二、发布前验证（已完成，见 docs/validation.md）

- `npm pack` 打出的真实包装进全新项目，在浏览器中跑通。
- 在全新的 Laravel 项目中用 Composer 安装两个包（path 仓库，复制模式），跑通“页面验证 → 控制器核验”的完整流程，包括组件从 gateway 跨域加载。

## 三、发布后验证（2026-10-22 已完成，见 docs/validation.md）

1. 在干净目录里执行 `npm install @botguard/browser`：从 registry.npmjs.org 装上 0.1.0（MIT）。包里有 LICENSE、`src/` 和 `dist/botguard.legacy.js`。`exports` 指向 `src/botguard.js`，`main` / `module` 指向 legacy 构建，`@botguard/browser/legacy` 可以导入，在服务端 import 不报错。
2. 新建 Laravel 13 项目，执行 `composer require botguard/laravel`：lock 显示两个包都经 Packagist 解析，压缩包来自镜像仓库的 v0.1.0 标签。接上单独起的 gateway 后，`<x-botguard>` 从 gateway 加载组件，在浏览器中无感通过，控制器 `BotGuard::verify()` 返回通过。伪造的 token 返回 `TOKEN_INVALID`，`botguard:login` 规则判定失败。
3. 从 gateway 跨域加载 `load.js` 的方式，已在 yihui-h5 模拟中验证（`docs/validation.md`）。

## 四、服务端发布

推送 `vX.Y.Z` 标签，或手动运行 `publish` 工作流（它会创建标签，再调用 `release`）后，`release` 工作流（`.github/workflows/release.yml`）运行两个任务：

**release**：

1. `make check`；
2. `scripts/release.sh vX.Y.Z`：用 `CGO_ENABLED=0` 交叉编译 linux/amd64、linux/arm64、darwin/arm64、darwin/amd64 的静态二进制（darwin/arm64 由 Go 链接器自动做 ad-hoc 签名，Apple 芯片才能运行），版本号写进程序（`gateway -version`、`botguard-ctl version`、管理后台「系统信息」），每个平台打一个 tar.gz，Linux 平台再用 [nfpm](https://nfpm.goreleaser.com/) 打 deb 和 rpm（配置在 `deploy/nfpm/`）；配置了签名密钥时 rpm 带 GPG 签名；
3. 为每个文件生成构建来源证明（`actions/attest-build-provenance`），用户可以用 `gh attestation verify <文件> -R dkeepersun/BotGuard` 核对；
4. 创建 GitHub Release，附上 tar.gz、deb、rpm 和 `checksums.txt`，说明由 GitHub 自动生成（列出上一个 Release 以来合并的 PR；直接提交到 main 的改动不会逐条列出，需要手动补充）。带 `-` 的版本（如 `v0.3.0-rc.1`）标为预发布，`upgrade` 和安装脚本默认取的“最新版”不包括预发布。

**image**：构建 amd64 + arm64 镜像，推送到 `ghcr.io/dkeepersun/botguard`，标签为 `0.3.0`、`0.3`，正式版另加 `latest`。

release 成功后，`repo` 工作流（`.github/workflows/repo.yml`）用最新正式版的 deb / rpm 重建 APT / YUM 软件源，发布到 GitHub Pages（<https://dkeepersun.github.io/BotGuard/>）。软件源只保留最新版本：迁移只向前，旧版本没有安装的意义。也可以在 Actions 页面手动运行 `repo` 重建。

每个 tar.gz 解压后是一个可以直接部署的目录，deb / rpm 把同样的内容装到 `/opt/botguard`：

```
botguard_0.3.0_linux_amd64/
  bin/gateway  bin/admin-web  bin/botguard-ctl
  configs/gateway.yaml  configs/admin-web.yaml     # 配置样例，botguard init 据此生成 /etc/botguard
  deploy/systemd/botguard.service.tmpl  deploy/launchd/botguard.plist.tmpl  deploy/botguard.env.example
  scripts/botguard.sh  docs/deploy.md  LICENSE  README.md  VERSION
```

包里的文件清单在 `scripts/release.sh` 里写死，`deploy/` 下其他文件不会被带进去。一键安装脚本 `scripts/install.sh` 不在包里，用户直接从 main 分支下载。

### 软件源和镜像的一次性设置

1. **签名密钥**。生成一把专用于软件源的 GPG 密钥（不设口令最简单；设了口令就把它存进 `REPO_GPG_PASSPHRASE`）：

   ```bash
   # 把邮箱换成真实地址再运行：UID 会出现在用户导入的 gpg.key 里
   gpg --batch --passphrase '' --quick-gen-key "BotGuard Packages <you@example.com>" rsa4096 sign never
   # 第一行应为 -----BEGIN PGP PRIVATE KEY BLOCK-----；提示 nothing exported 说明本机没有这把密钥
   gpg --armor --export-secret-keys "BotGuard Packages" | head -1
   # 用管道写入，避免复制粘贴出错
   gpg --armor --export-secret-keys "BotGuard Packages" | gh secret set REPO_GPG_PRIVATE_KEY -R dkeepersun/BotGuard
   ```

   secret 必须在 Settings → Secrets and variables → Actions 的 **Repository secrets** 里。私钥另外备份到密码管理器或离线介质。私钥同时用于签名 rpm 包和软件源元数据；公钥由工作流导出为 `gpg.key` 发布。**这把密钥不要更换**：换了之后，已经添加软件源的服务器需要重新导入公钥才能升级（改 UID 不算更换，指纹不变）。**要在发第一个正式版之前配好**，否则那个版本的 rpm 没有签名，软件源一旦可用，dnf/yum 就装不上。
2. **GitHub Pages**。Settings → Pages → Build and deployment → Source 选择 **GitHub Actions**。没启用时 `repo` 工作流的 `deploy` 任务会报 `Failed to create deployment (status: 404)`。
3. **镜像可见性**。第一次推送后，在 GitHub 个人主页 → Packages → `botguard` → Package settings 中把可见性改为 Public，否则用户拉取镜像需要登录。

没有配置 `REPO_GPG_PRIVATE_KEY` 时，release 照常发布（rpm 不签名），`repo` 工作流会跳过并给出警告；一键安装脚本检测到软件源不可用时自动改用发布包安装。

### 发一个版本

1. 确认 main 上的 CI 通过（`install` 任务会在全新的 runner 上实际跑一遍安装脚本、deb 包和 Docker 部署）。新版本需要改配置或有不兼容变更时，先想好要写进 Release 说明的内容。
2. 本机可以先试打包：`scripts/release.sh v0.3.0`，产物在 `dist/`（已加入 `.gitignore`）。装了 nfpm 时同时生成 deb 和 rpm：`go install github.com/goreleaser/nfpm/v2/cmd/nfpm@v2.43.0`。
3. 打标签，两种方式效果相同，任选一种：
   - **在网页上**：Actions → **publish** → Run workflow，填版本号（例如 `v0.3.0`），分支默认 `main`。工作流会先检查版本号格式、标签是否已存在、这个提交的 CI 是否已通过，再创建标签并发布。也可以用 API 或 `gh workflow run publish.yml -f version=v0.3.0` 触发。
   - **在本机**：

     ```bash
     git fetch origin
     git tag -a v0.3.0 origin/main -m "BotGuard v0.3.0"
     git push origin v0.3.0
     ```

4. 工作流结束后，在 Release 页面检查附件和自动生成的说明，需要时手动补充升级注意事项（配置变化、迁移耗时等）。确认 `repo` 工作流已更新软件源：打开这次运行，看 “Build the repository” 步骤没有被跳过，日志里列出的是新版本的 deb / rpm。

第一次发版建议先推 `v0.3.0-rc.1` 这样的预发布标签，确认工作流、附件、镜像和 `upgrade` 都正常后再发正式版。标签一旦推送就不要移动或复用；发错了就发下一个版本号。

`vX.Y.Z` 标签同时也是 Go 模块版本，所以 `go install github.com/dkeepersun/BotGuard/cmd/gateway@v0.3.0` 也能装上对应版本（这样编译的程序版本号显示为模块版本）。

## 五、Go SDK

Go 模块不需要注册中心：`go get` 通过 Go 模块代理直接从本仓库按标签取代码，所以发布就是推标签。三个模块在同一个仓库的子目录里，标签要带目录前缀（[Go 多模块仓库的规则](https://go.dev/doc/modules/managing-source#multiple-module-source)）：

| 模块 | 目录 | 标签 |
|---|---|---|
| `github.com/dkeepersun/BotGuard/packages/go` | `packages/go` | `packages/go/vX.Y.Z` |
| `github.com/dkeepersun/BotGuard/packages/go/gin` | `packages/go/gin` | `packages/go/gin/vX.Y.Z` |
| `github.com/dkeepersun/BotGuard/packages/go/gozero` | `packages/go/gozero` | `packages/go/gozero/vX.Y.Z` |

适配包的 `go.mod` 里 `require` 的是核心包的**已发布版本**；`packages/go/go.work` 里的 `replace` 只在本仓库开发和 CI 时让它们用本目录的代码，用户 `go get` 时不会读到。所以：

1. **先发核心包**，确认改动已合并、main 上的 CI 通过后，**用提交号打标签**（本机的 `origin/main` 可能还没更新）：

   ```bash
   git fetch origin
   SHA=$(git rev-parse origin/main)          # 或者直接写 GitHub 上看到的合并提交号
   git cat-file -e "$SHA:packages/go/go.mod" && git log --oneline -1 "$SHA"   # 确认这个提交里有要发布的代码
   git tag -a packages/go/v0.1.0 "$SHA" -m "BotGuard Go SDK v0.1.0"
   git push origin packages/go/v0.1.0
   ```

2. **再发适配包**。适配包 require 的核心版本必须已经有标签，否则用户 `go get` 时会报 `unknown revision`：

   ```bash
   git tag -a packages/go/gin/v0.1.0 "$SHA" -m "BotGuard Go SDK gin adapter v0.1.0"
   git tag -a packages/go/gozero/v0.1.0 "$SHA" -m "BotGuard Go SDK go-zero adapter v0.1.0"
   git push origin packages/go/gin/v0.1.0 packages/go/gozero/v0.1.0
   git ls-remote --tags origin 'packages/go/*'   # 带 ^{} 的行应都是 $SHA
   ```

3. **发布后验证**：在一个干净目录里，不带 `go.work`，确认能从模块代理装上：

   ```bash
   mkdir /tmp/bg-go && cd /tmp/bg-go && go mod init example.com/try
   go get github.com/dkeepersun/BotGuard/packages/go@v0.1.0 \
          github.com/dkeepersun/BotGuard/packages/go/gin@v0.1.0 \
          github.com/dkeepersun/BotGuard/packages/go/gozero@v0.1.0
   ```

   也可以直接查代理和校验和数据库（路径里的大写字母写成 `!` 加小写）：

   ```bash
   curl https://proxy.golang.org/github.com/dkeepersun/!bot!guard/packages/go/@v/v0.1.0.info
   curl https://sum.golang.org/lookup/github.com/dkeepersun/!bot!guard/packages/go@v0.1.0
   ```

   `.info` 里的 `Hash` 应是打标签的提交。`go get` 时会顺带下载一次服务端模块 `github.com/dkeepersun/BotGuard`（约 2 MB），这是 Go 在确认这个包不属于上一级模块，不会写进用户的 `go.mod`。

   模块代理第一次被请求时才去抓取标签，<https://pkg.go.dev/github.com/dkeepersun/BotGuard/packages/go> 也要等有人请求过才会出现文档页。

以后升级核心包、并让适配包依赖新版本时：先发核心包新标签，再把适配包 `go.mod` 的 require 和 `go.work` 里 `replace` 的版本号一起改成新版本，合并后再给适配包打标签。只改适配包时不用动核心包。标签推送后不要移动或删除：模块代理和校验和数据库（sum.golang.org）会永久记住第一次看到的内容，同一个版本号换了代码，用户会遇到校验和不匹配。唯一的例外是这个版本从来没有成功下载过：上面的 lookup 返回 404 时，数据库里没有记录，删掉重打不会造成冲突，只是代理可能要过一阵才认新标签（见「发版排错」）。
