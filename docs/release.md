# 发布清单

服务端程序和三个 SDK 包分开发版，各用各的标签：

| 内容 | 渠道 | 标签 | 安装方式 |
|---|---|---|---|
| 服务端（gateway、admin-web、botguard-ctl） | GitHub Releases（tar.gz、deb、rpm）、APT/YUM 软件源、GHCR 镜像 | `vX.Y.Z` | 一键安装脚本，见 [install.md](install.md) |
| `@botguard/browser` | npm | `browser-vX.Y.Z` | `npm install @botguard/browser` |
| `botguard/php-sdk` | Packagist | `php-vX.Y.Z` | `composer require botguard/php-sdk` |
| `botguard/laravel` | Packagist | `laravel-vX.Y.Z` | `composer require botguard/laravel` |

服务端的发布步骤见文末「四、服务端发布」。下面先是三个 SDK 包。

| 包 | 渠道 | 安装命令 |
|---|---|---|
| `@botguard/browser` | npm | `npm install @botguard/browser` |
| `botguard/php-sdk` | Packagist | `composer require botguard/php-sdk` |
| `botguard/laravel` | Packagist | `composer require botguard/laravel` |

另外，不想用 npm 的页面可以直接引用 gateway 托管的脚本，和易盾的 `load.min.js` 一样：`<script src="https://你的gateway/sdk/v1/load.js">`。

## 当前状态（2026-10-22）

三个包都已发布 0.1.0，许可证 MIT：

- npm：[@botguard/browser](https://www.npmjs.com/package/@botguard/browser)，组织 `botguard`（所有者 dksun）。0.1.0 是在本机手动发布的。
- Packagist：[botguard/php-sdk](https://packagist.org/packages/botguard/php-sdk)、[botguard/laravel](https://packagist.org/packages/botguard/laravel)。两个镜像仓库都已装好 Packagist 的 webhook，新标签会自动收录。
- 镜像仓库：[botguard-php-sdk](https://github.com/dkeepersun/botguard-php-sdk)、[botguard-laravel](https://github.com/dkeepersun/botguard-laravel)。已关闭 issues 和 wiki；部署密钥已配置，私钥只保存在主仓库的 Actions secrets 里。
- 待办：在 npm 包设置里配置 Trusted Publisher（见下文 npm 第 3 步），之后的版本才能由工作流自动发布。

第一次发布时遇到的问题，下次可以提前避开：

- 推送 `.github/workflows/` 需要 gh 登录令牌带 `workflow` 权限：`gh auth refresh -h github.com -s workflow`。在终端里要按回车，否则 gh 不会去取新令牌。
- npm 发布要求账号开启 2FA，且要先在网站上建好 `botguard` 组织，否则会报 403 或 `Scope not found`。
- `npm login`、`npm publish` 要在真正的终端里运行。在 Claude Code 里用 `!` 运行时无法交互，`npm login` 会直接报错退出。
- 发布后 npm 会自动留下一个 `0.0.0-stage` 版本，描述为 “Temporary package placeholder for staged publishing”。它不影响安装：`latest` 指向正式版本，`^0.1.0` 也不会匹配预发布版本。
- 不要补推 `browser-v0.1.0` 标签：0.1.0 已经手动发布，工作流会因为版本已存在而失败。下一个版本从 `browser-v0.1.1` 开始。


## 一、只能由你完成的步骤

这些步骤涉及创建账号、登录和发布令牌，必须由你本人操作。

### 许可证

三个包都采用 MIT（2026-10-22 确定）。仓库根目录和三个包目录各有一份 `LICENSE`：镜像仓库只包含包目录，npm 也只打包包目录里的文件。

### npm

npm 已在 2025 年底撤销 classic（含 Automation）令牌；有写权限的 granular 令牌最长 90 天，默认要求 2FA。所以 CI 发布改用 **Trusted Publishing**（GitHub Actions 的 OIDC 临时凭证），仓库里不保存任何 npm 令牌。

1. 注册或登录 npmjs.com，开启 2FA，创建组织 **botguard**，这样才能使用 `@botguard/` 前缀。如果组织名已被占用，告诉我换哪个名字，我来改包名。
2. **第一次发布手动完成**，因为 Trusted Publishing 要在已存在的包上配置：在 `packages/browser` 下执行 `npm ci && npm test && npm publish --access public`，按提示输入 2FA 验证码。
3. 在 npmjs.com 打开包 `@botguard/browser` → Settings → Trusted Publisher，选择 GitHub Actions，填写仓库 `dkeepersun/BotGuard`、工作流文件 `release-browser.yml`。之后可以在同一页面把发布方式设为“只允许 Trusted Publishing”。
4. 之后的版本：修改 `package.json` 的 version，推送标签 `browser-vX.Y.Z`。`release-browser` 工作流会检查版本号、跑测试，然后带 provenance 发布（需要主仓库保持公开）。

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

推送 `vX.Y.Z` 标签后，`release` 工作流（`.github/workflows/release.yml`）运行两个任务：

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
   gpg --quick-gen-key "BotGuard Packages <你的邮箱>" rsa4096 sign never
   gpg --armor --export-secret-keys "BotGuard Packages" > botguard-signing.asc
   ```

   在仓库 Settings → Secrets and variables → Actions 中新建 secret `REPO_GPG_PRIVATE_KEY`，内容为 `botguard-signing.asc`，然后删除本地文件。私钥同时用于签名 rpm 包和软件源元数据；公钥由工作流导出为 `gpg.key` 发布。**这把密钥不要更换**：换了之后，已经添加软件源的服务器需要重新导入公钥才能升级。
2. **GitHub Pages**。Settings → Pages → Build and deployment → Source 选择 **GitHub Actions**。
3. **镜像可见性**。第一次推送后，在 GitHub 个人主页 → Packages → `botguard` → Package settings 中把可见性改为 Public，否则用户拉取镜像需要登录。

没有配置 `REPO_GPG_PRIVATE_KEY` 时，release 照常发布（rpm 不签名），`repo` 工作流会跳过并给出警告；一键安装脚本检测到软件源不可用时自动改用发布包安装。

### 发一个版本

1. 确认 main 上的 CI 通过（`install` 任务会在全新的 runner 上实际跑一遍安装脚本、deb 包和 Docker 部署）。新版本需要改配置或有不兼容变更时，先想好要写进 Release 说明的内容。
2. 本机可以先试打包：`scripts/release.sh v0.3.0`，产物在 `dist/`（已加入 `.gitignore`）。装了 nfpm 时同时生成 deb 和 rpm：`go install github.com/goreleaser/nfpm/v2/cmd/nfpm@v2.43.0`。
3. 打标签并推送：

   ```bash
   git tag -a v0.3.0 -m "BotGuard v0.3.0"
   git push origin v0.3.0
   ```

4. 工作流结束后，在 Release 页面检查附件和自动生成的说明，需要时手动补充升级注意事项（配置变化、迁移耗时等）。确认 `repo` 工作流已更新软件源。

第一次发版建议先推 `v0.3.0-rc.1` 这样的预发布标签，确认工作流、附件、镜像和 `upgrade` 都正常后再发正式版。标签一旦推送就不要移动或复用；发错了就发下一个版本号。

`vX.Y.Z` 标签同时也是 Go 模块版本，所以 `go install github.com/dkeepersun/BotGuard/cmd/gateway@v0.3.0` 也能装上对应版本（这样编译的程序版本号显示为模块版本）。
