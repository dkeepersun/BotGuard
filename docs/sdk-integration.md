# SDK、仓库与接入方案

## 决策：一个开发仓库，独立发布

不需要一开始建多个独立开发项目。协议、服务与 SDK 的变更放在一个 PR 中，统一 fixtures 和兼容测试；发布版本独立。拆仓只有在团队权限、独立交付节奏或对外开源边界确有需要时再做。

| 包 | 职责 | 发布渠道 | 提议名字（未注册） |
|---|---|---|---|
| browser | Web Component、挑战请求、Worker 求解、token 回调、reset/destroy | npm | `@botguard/browser` |
| go | 服务端 HTTP verify、context/超时、四类类型化结果 | Go Modules | `github.com/dkeepersun/BotGuard/packages/go` |
| php | 无 Laravel 依赖的 verify 核心；PSR-18/17 传输抽象 | Composer | `botguard/php-sdk` |
| laravel | 配置、服务容器、Facade、Rule/中间件适配 PHP 核心 | Composer | `botguard/laravel` |

浏览器包不能携带服务端 secret。npm 是 JavaScript 生态分发方式，不能代替 Go Modules 或 Composer。若将来需要 Node.js 服务端 SDK，可新增 `packages/node`，与浏览器 package 分开，避免 secret/API 误用；当前不增加。

## 包布局和版本

- 根目录 Go module 仅服务端；`packages/go/go.mod` 独立，无 go-zero/Redis/MySQL 依赖，只用标准库 HTTP。共享 DTO 通过契约生成/fixtures 对齐，不 import 服务端 internal。
- Go 子模块标签用 `packages/go/v0.1.0`，v2 需要符合 Go major module path 规则。CI 分别测试根和子 module，不依赖本地 replace 隐藏发布错误。
- npm 用根 private workspace + 子包 `package.json`，TypeScript 输出 ESM、声明文件、Worker 资源和可选独立浏览器构建；`files` 明确白名单，包测试检查密钥/服务端代码未打包。
- PHP core 建议 PHP >=8.2 为初始候选；Laravel 支持矩阵在实施时按用户项目锁定并用 CI 验证，不未经测试宣称兼容所有版本。
- Laravel adapter 单独 composer.json，依赖已发布 core 范围；本地使用 Composer `path` repositories 联调。
- Composer 正式分发采用自动 subtree split 到只读发布镜像 `botguard-php-sdk` 和 `botguard-laravel`，使 composer.json 位于镜像仓库根目录；接入 Packagist 或私有 Composer 仓库。开发只改 monorepo，镜像禁手工修改。
- 另一选择是受控 Composer dist 仓库，归档根包含 composer.json；对当前规模，镜像更直观。不把 monorepo 子目录 URL 当成 Packagist 原生子包注册方案。
- 每个包独立 SemVer；API `/v1` 加字段保持向后兼容，删除/改语义走新版本。记录每个包兼容的服务端范围，发布前测试当前和上一个兼容服务版本。

以上发布规则依据官方资料：[Go 多模块源码管理](https://go.dev/doc/modules/managing-source)、[npm workspaces](https://docs.npmjs.com/cli/v8/using-npm/workspaces/)、[npm publish](https://docs.npmjs.com/cli/commands/npm-publish/)、[Composer repositories](https://getcomposer.org/doc/05-repositories.md)。

## 首批接入约定

浏览器组件和 PHP SDK 已实现（未发布），可运行示例见 `examples/demo`；Go SDK 与 Laravel 适配仍是提案。

浏览器（`packages/browser`，已实现）：业务页引入 `<botguard-widget>` 并配置 endpoint/app-key/action；组件拿到 token 后交给业务表单的 `botguard_token` 字段。token 到期、业务拒绝、页面 reset 后清理。Worker 不能冻结主线程；超时可取消。不能只隐藏提交按钮作为防护。

服务端 SDK 统一结果分类（判定规则以 `api/contract-v1.md` 的“verify 结果语义”为准，各语言共用同一组 fixtures）：

| 分类 | 判定 | 调用方处理 |
|---|---|---|
| Passed | HTTP 200 且 `success: true`，且响应中的 app_key/action 与请求一致 | 继续本地业务幂等事务 |
| Rejected | HTTP 200 且 `success: false`；携带 reason，未知 reason 也归入此类 | 不执行副作用，提示终端用户重新验证 |
| CallerError | 400/401/403/409；携带 code | 不执行副作用、不重试，记录并告警（代码或配置问题） |
| Unknown | 429、5xx、网络错误、超时，以及响应畸形：非 JSON、缺 `success`、`success: true` 但 app_key/action 与请求不符 | 不执行副作用；服务端可能已消费 token，只能用原 request_id 重试或提示稍后再试 |

- 只有 Passed 允许继续业务。SDK 不把 Rejected 表示成异常，也不把 CallerError/Unknown 表示成普通拒绝，避免业务把配置故障当成“用户没过验证”静默吞掉。
- 响应畸形属于 Unknown 的一个子类型（ProtocolError），SDK 应单独记录以便排查，但重试语义与 Unknown 相同。
- 429 的 `Retry-After` 透传给调用方。

Go（待实现）：`client.Verify(ctx, VerifyRequest{AppKey, Token, Action, RequestID})` 返回 `(Result, error)`：Passed 与 Rejected 通过 `Result` 返回，`error == nil`；CallerError 与 Unknown 通过可用 `errors.As` 区分的类型化 error 返回。创建 client 时注入 HTTPS endpoint、secret_id/secret_key、超时；每次请求（含重试）都重新签名；固定超时上限、不跟随重定向、限制响应体。除非调用方明确指定原 request_id，不自动重试。

PHP（`packages/php`，已实现）：`Client::verify(VerifyRequest): VerifyResult`，按 BG1-HMAC-SHA256 签名，传输可注入，可选按原 request_id 重新签名重试。Passed 与 Rejected 通过 `VerifyResult` 返回；CallerError 与 Unknown 抛出不同的异常类型（ProtocolError 为 Unknown 异常的子类）。secret_key 由业务服务器配置注入，不进入 Blade/JavaScript/HTML。

Laravel：ServiceProvider 注入 PHP client，发布配置，支持 config cache；只在配置读取 env。默认推荐 controller/service 显式验证，便于控制消费时机。若提供 Validation Rule，只在普通字段验证全部成功后调用，且每个请求最多一次；不要让通用多次校验提前消耗 token。`request_id` 与业务幂等键由服务端建立稳定映射。Validation Rule 只把 Rejected 转为表单验证错误；CallerError 与 Unknown 不得伪装成验证错误，应抛出异常交给应用的错误处理（如返回 503）。

未来发布后安装命令：
```sh
npm install @botguard/browser
# Go 模块使用实际已发布版本：
go get github.com/dkeepersun/BotGuard/packages/go@v0.1.0
composer require botguard/php-sdk
# Laravel 项目安装适配包，传递引入 core：
composer require botguard/laravel
```
这些名字/版本目前未发布，不能作为现在可运行的指令。

## 发布流水线计划

1. 服务端和 SDK 共用签名用例 `api/fixtures/hmac-signature-v1.json`、PoW 用例 `api/fixtures/pow-sha256-target-v1.json`，以及 Passed / Rejected（含未知 reason）/ CallerError / Unknown（含畸形响应）四类 fixtures；CI 不启动或访问用户现有业务库。
2. 执行根 Go 测试、Go SDK 独立测试、browser build/test、PHP 单测、Laravel 兼容矩阵。
3. npm pack 检查归档；Go 从干净消费者 module 安装 tag；Composer 从镜像 tag 或 dist 归档安装；示例项目冒烟。
4. 生成 changelog 与兼容表，再发布所选包。发布账户、仓库创建、推送和 npm/Packagist 发布需后续明确执行指令，本次未操作。
