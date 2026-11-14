# Posthaste

Posthaste 是面向内部业务系统的自托管消息／告警推送中心：通过统一 HTTP API、Shoutrrr Generic Webhook 或 Go SDK 提交消息，再按 caller 目标组推送到一个或多个平台目标。

**当前状态：仅完成架构与工程规范文档。** 服务端、管理后台和 Go SDK 尚未实现；没有可运行命令、已发布 SDK、数据库或部署实例。下文描述首阶段开发目标。

## 文档导航

| 文档 | 读者与内容 |
| --- | --- |
| [系统设计](DESIGN.md) | 后端与架构维护者：领域边界、目录、数据、消息链路、生命周期、配置和扩展 |
| [工程规范](AGENTS.md) | 所有开发者和代理：分层、安全、校验、修改与验证约束 |
| [HTTP API](docs/API.md) | 接入方和后台开发者：请求/响应、caller、配置管理与 Beszel 示例 |
| [Go SDK](docs/GO_SDK.md) | Go 服务接入方和 SDK 维护者：公开接口、示例、测试与发布 |

## 首阶段能力

- 内部单租户、单实例运行，一个 Go 服务与同源管理后台。
- 统一 `POST /api/v1/notify`，接收 title/message，支持 text 和 Markdown。
- caller 选择目标组；缺省或未知 caller 使用默认组，命中空组则明确拒绝。
- 飞书自定义机器人、Telegram Bot 两种平台，每组可绑定多个目标。
- 后台维护目标、目标组和默认组，以单管理员口令登录；目标测试复用正常异步链路。
- 独立 Go SDK，方便其他 Go 服务发送消息，首阶段与 HTTP v1 同步交付。

投递为**内存尽力发送**。202 只表示进入当前进程队列，不代表送达；不提供消息历史、自动重试、幂等去重、重启恢复或结果查询。messageId 只用于日志关联。客户端超时后重新提交可能重复推送。

## 架构概要

```text
业务系统 / Beszel / Go SDK
  → HTTP 适配器
  → notification 接收用例 → PostgreSQL 配置
  → eventbus 内存队列
  → 固定订阅者 → notification 投递用例
  → Feishu Sender / Telegram Sender
```

后端使用 allingo + fx + DDD，划分 notification、auth 两个域，每域按 application/domain/infra 分层。PostgreSQL 只保存目标和目标组配置，平台凭证加密保存；不装配 Redis、消息中间件、对象存储或定时任务。

后台采用 React、TypeScript、Vite、Tailwind CSS 和 pnpm，构建产物嵌入 Go。Go SDK 计划位于 `sdk/go` 独立模块，仅依赖标准库，不将 allingo 或数据库依赖带入调用方项目。

## 消息接入示例

服务实现并部署后，HTTP 调用形状如下：

```http
POST /api/v1/notify?caller=beszel HTTP/1.1
Authorization: Bearer <入口 token>
Content-Type: application/json

{
  "title": "磁盘空间告警",
  "message": "**web-01** 磁盘使用率超过阈值。",
  "format": "markdown"
}
```

Beszel 的通知 URL 计划使用：

```text
generic://notify.example.com/api/v1/notify?template=json&caller=beszel&@Authorization=Bearer%20YOUR_TOKEN
```

其中 caller 只是服务端配置的路由键，不是身份凭证；省略 format 时按纯文本发送。完整响应、安全状态与集成说明见 [HTTP API](docs/API.md)。Go SDK 的目标使用方式见 [Go SDK](docs/GO_SDK.md)，当前尚无可安装版本。

## 配置与运行前提

未来部署需要 PostgreSQL、HTTPS 反向代理，以及到飞书／Telegram 官方 API 的网络连接。默认单实例，后台与 API 同源。关键配置如下，均尚未由代码读取：

| 配置 | 用途 |
| --- | --- |
| `DB_DSN` | allingo PostgreSQL 连接串 |
| `POSTHASTE_PUBLIC_URL` | 后台公开 HTTPS origin，用于同源校验 |
| `POSTHASTE_ADMIN_PASSWORD` | 管理员口令 |
| `POSTHASTE_INGRESS_TOKEN` | 独立消息入口 Bearer Token |
| `POSTHASTE_ENCRYPTION_KEY` | 标准 Base64 编码的 32 字节平台凭证加密密钥 |
| `RUN_MODE=release` | 生产 allingo 模式，避免调试监听 |

无默认管理员口令、入口 Token 或主密钥。完整环境变量、队列参数和超时约定见 [系统设计](DESIGN.md)。修改主密钥需要显式处理已有凭证，不会自动迁移。

## 后续开发与验证

开发顺序为：基础装配与配置 → 配置存储及认证 → 消息链路和平台 Sender → HTTP/Go SDK → 后台与静态资源嵌入。每阶段以 [系统设计](DESIGN.md) 和 [工程规范](AGENTS.md) 的约束、接口与验收场景为准。

默认验证使用静态检查、编译、纯单元测试、HTTP recorder、模拟存储及 RoundTripper，不启动 dev server。真实 PostgreSQL、浏览器和平台联调须另行授权，不以模拟测试替代验收。

服务端和 SDK 独立检查；根 `go test ./...` 不覆盖嵌套 SDK 模块。SDK 发布使用 `sdk/go/vX.Y.Z` 标签，不与根服务版本绑定。本次文档交付不创建代码、启动服务或执行发布。
