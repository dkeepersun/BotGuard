# BotGuard

[![ci](https://github.com/dkeepersun/BotGuard/actions/workflows/ci.yml/badge.svg)](https://github.com/dkeepersun/BotGuard/actions/workflows/ci.yml)
[![release](https://img.shields.io/github/v/release/dkeepersun/BotGuard)](https://github.com/dkeepersun/BotGuard/releases)
[![license](https://img.shields.io/badge/license-MIT-blue)](LICENSE)

BotGuard 是一个可以自己部署的人机验证服务，用来防止机器人批量注册、撞库登录、刷接口。接入方式与网易易盾类似：前端放一个验证组件，后端调用接口核验结果。

- **多种验证方式**：无感验证（后台工作量证明），以及滑块拼图、旋转对齐、文字点选、图标点选等 8 种交互题型；可按风险自动选择
- **安全的接入协议**：一次性 token、HMAC 签名核验、密钥加密存储，支持多个应用和密钥轮换
- **管理后台**：应用与策略管理、统计报表、实时事件、审计日志、动态码登录
- **开箱即用**：内置 SQLite 数据库，唯一依赖是 Redis；一条命令完成安装
- **SDK**：浏览器组件、PHP、Laravel、Go（含 gin / go-zero 中间件）

## 快速开始

在 Linux 服务器或 Mac（x86_64 / arm64）上执行同一条命令：

```bash
curl -fsSL https://raw.githubusercontent.com/dkeepersun/BotGuard/main/scripts/install.sh | sh
```

脚本会自动安装 Redis、下载程序、生成配置和密钥、注册开机自启的服务，并创建管理员账号（初始密码只显示一次）。

| | Linux 服务器 | Mac（本机开发测试） |
|---|---|---|
| 运行方式 | systemd 服务，开机启动 | launchd 服务，登录后启动；不需要 sudo |
| gateway（验证接口） | `http://服务器IP:8090` | `http://127.0.0.1:8090` |
| 管理后台 | `http://127.0.0.1:8091`，只在本机监听；先执行 `ssh -L 8091:127.0.0.1:8091 用户@服务器`，再用浏览器打开 | `http://127.0.0.1:8091` |

想看一个带演示网站的完整环境？用 Docker 执行 `examples/demo/start.sh`，见 [examples/demo](examples/demo/README.md)。

## 安装方式

| 方式 | 命令 | 适合 |
|---|---|---|
| 一键脚本 | `curl -fsSL …/install.sh \| sh` | 大多数服务器，自动选择下面的一种方式 |
| apt / dnf 软件源 | `sudo apt install botguard` | 希望和系统软件一起升级 |
| 发布包 | `install.sh --method binary` | 任意 Linux 和 macOS（macOS 的默认方式） |
| Docker | `install.sh --method docker` | 已在使用 Docker 的环境 |
| 源码 | `install.sh --method source` | 开发和测试 |

软件源配置、安装选项、无人值守安装和卸载，见 **[安装文档](docs/install.md)**。

## 使用

**1. 创建应用。** 登录管理后台，进入「应用管理 → 新建应用」，添加网站域名，创建密钥对。「接入代码」页会给出填好参数的前后端代码。

**2. 在网页中加入验证组件。** 验证通过后，表单会带上 `botguard_token` 字段：

```html
<script type="module" src="https://captcha.example.com/sdk/v1/botguard.js"></script>
<form method="post" action="/login">
  <!-- 用户名、密码等字段 -->
  <botguard-widget endpoint="https://captcha.example.com" app-key="app_xxx" action="login"></botguard-widget>
</form>
```

**3. 在后端核验。** 以 PHP 为例：

```php
$client = new BotGuard\Client('https://captcha.example.com', $secretId, $secretKey, ['app_key' => 'app_xxx']);
$result = $client->verifyToken($_POST['botguard_token'] ?? null, 'login');
if (!$result->passed()) {
    // 验证未通过，拒绝请求
}
```

| SDK | 安装 | 文档 |
|---|---|---|
| 浏览器组件 | `npm install @botguard/browser`，或直接引用 gateway 上的脚本 | [packages/browser](packages/browser/README.md) |
| PHP | `composer require botguard/php-sdk` | [packages/php](packages/php/README.md) |
| Laravel | `composer require botguard/laravel` | [packages/laravel](packages/laravel/README.md) |
| Go（net/http、gin、go-zero） | `go get github.com/dkeepersun/BotGuard/packages/go` | [packages/go](packages/go/README.md) |

其他语言可以按 [API 文档](api/contract-v1.md) 直接调用 `/v1/verify`。

## 运维

安装后可以使用 `botguard` 命令管理服务：

```bash
botguard status                   # 查看状态
sudo botguard restart             # 重启
botguard logs gateway -f          # 查看日志
sudo botguard upgrade             # 升级到最新版（deb/rpm 安装请用 apt / dnf）
sudo botguard ctl help            # 命令行管理应用、密钥和管理员
```

配置文件在 `/etc/botguard/`，数据在 `/var/lib/botguard/`。生产环境建议在 gateway 前面加 HTTPS 反向代理。配置说明、升级、备份和 nginx 示例，见 **[部署与运维](docs/deploy.md)**。

## 文档

| 文档 | 内容 |
|---|---|
| [安装](docs/install.md) | 各种安装方式、选项、卸载、常见问题 |
| [部署与运维](docs/deploy.md) | 目录结构、修改配置、升级、备份、nginx |
| [管理后台](docs/admin.md) | 角色、功能和使用说明 |
| [验证题型](docs/challenge-types.md) | 题型、验证模式与风险策略 |
| [API](api/contract-v1.md) | 接口与签名协议 |
| [开发指南](docs/development.md) | 本地运行、数据库、测试 |
| [发布流程](docs/release.md) | 发布服务端、软件源、镜像和 SDK |

## 许可证

[MIT](LICENSE)
