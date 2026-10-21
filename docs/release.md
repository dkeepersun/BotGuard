# 发布清单（npm 与 Composer）

三个包的发布渠道：

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
