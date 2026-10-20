# 发布清单（npm 与 Composer）

三个包的发布渠道：

| 包 | 渠道 | 安装命令（发布后） |
|---|---|---|
| `@botguard/browser` | npm | `npm install @botguard/browser` |
| `botguard/php-sdk` | Packagist | `composer require botguard/php-sdk` |
| `botguard/laravel` | Packagist | `composer require botguard/laravel` |

另外，不想用 npm 的页面可以直接引用 gateway 托管的脚本，和易盾的 `load.min.js` 一样：`<script src="https://你的gateway/sdk/v1/load.js">`。

## 一、只能由你完成的步骤

这些步骤涉及创建账号、登录和发布令牌，必须由你本人操作。

### 发布前先决定许可证

目前三个包标的都是“不开源”：npm 写 `UNLICENSED`，Composer 写 `proprietary`。包发布到公开仓库后，任何人都能下载源码；但在这两种许可下，别人在法律上无权使用。

- 想让外部开发者使用，需要换成开源许可（如 MIT 或 Apache-2.0），在仓库根目录加 `LICENSE`，并同步修改两个 `composer.json` 和 `package.json` 的 license 字段。
- 只给自己的项目用，可以保持现状，但可以考虑改用私有仓库（npm 私有包、Private Packagist），不发到公开仓库。

### npm

1. 注册或登录 npmjs.com，创建组织 **botguard**，这样才能使用 `@botguard/` 前缀。如果组织名已被占用，告诉我换哪个名字，我来改包名。
2. 在 npm 账号设置里生成 **Automation** 类型的访问令牌。
3. 在 GitHub 仓库 Settings → Secrets → Actions 中添加 `NPM_TOKEN`。
4. 发布：推送标签 `browser-v0.1.0`，`release-browser` 工作流会检查版本号、跑测试，然后带 provenance 发布到 npm。

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

## 三、发布后验证

发布完成后告诉我，我会在干净的临时目录中：

1. `npm install @botguard/browser`，检查装上的版本、文件和导入路径，再跑一遍浏览器流程；
2. 新建 Laravel 项目，执行 `composer require botguard/laravel`，确认会从 Packagist 拉取，并带上依赖的 `botguard/php-sdk`，然后跑通端到端流程；
3. 确认 `<script src="https://你的gateway/sdk/v1/load.js">` 在非同源页面可用。
