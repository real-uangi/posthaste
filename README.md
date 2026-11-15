# Posthaste

Posthaste 是面向内部业务系统的自托管消息／告警推送中心：通过统一 HTTP API、Shoutrrr Generic Webhook 或 Go SDK 提交消息，再按 caller 目标组推送到一个或多个平台目标。

首版后端、管理后台和独立 Go SDK 已实现。项目尚未部署，SDK 尚未发布版本标签；真实 PostgreSQL、浏览器和飞书／Telegram 联调仍需单独验收。

## 文档导航

| 文档 | 读者与内容 |
| --- | --- |
| [系统设计](DESIGN.md) | 后端与架构维护者：领域边界、目录、数据、消息链路、生命周期、配置和扩展 |
| [工程规范](AGENTS.md) | 所有开发者和代理：分层、安全、校验、修改与验证约束 |
| [HTTP API](docs/API.md) | 接入方和后台开发者：请求/响应、caller、配置管理与 Beszel 示例 |
| [Go SDK](docs/GO_SDK.md) | Go 服务接入方和 SDK 维护者：公开接口、示例、测试与发布 |

## 功能

- 内部单租户、单实例运行，一个 Go 服务与同源管理后台。
- 统一 `POST /api/v1/notify`，接收 title/message，支持 text 和 Markdown。
- caller 选择目标组；缺省或未知 caller 使用默认组，命中空组则明确拒绝。
- 独立平台账号统一维护凭证，账号下配置群组/话题出口；TG 账号可有多个出口，飞书群 Webhook 账号对应一个固定群出口。
- caller 路由组可绑定不同账号下的多个出口。后台提供账号、群组出口、路由组和默认组管理，以单管理员口令登录；出口测试复用正常异步链路。
- 独立 Go SDK，方便其他 Go 服务发送消息，与 HTTP v1 保持同一协议。

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

后端使用 allingo + fx + DDD，划分 notification、auth 两个域，每域按 application/domain/infra 分层。PostgreSQL 只保存账号、出口、路由组及成员配置，凭证以账号为单位加密保存；不装配 Redis、消息中间件、对象存储或定时任务。

一个账号更新 Token 后，其所有出口的后续消息使用新凭证；停用账号会停用其全部出口的受理，但不改写出口自身开关。caller 路由和 Go SDK 不需要知道账号凭证。飞书应用机器人（应用身份加多个 chatId）留待后续接入，当前飞书类型仍为群 Webhook。

后台采用 React、TypeScript、Vite、Tailwind CSS 和 pnpm，构建产物嵌入 Go。Go SDK 位于 [sdk/go](sdk/go/README.md) 独立模块，仅依赖标准库，不将 allingo 或数据库依赖带入调用方项目。

## 消息接入示例

服务部署后的 HTTP 调用形状如下：

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

Beszel 的通知 URL 使用：

```text
generic://notify.example.com/api/v1/notify?template=json&caller=beszel&@Authorization=Bearer%20YOUR_TOKEN
```

其中 caller 只是服务端配置的路由键，不是身份凭证；省略 format 时按纯文本发送。完整响应、安全状态与集成说明见 [HTTP API](docs/API.md)。SDK 接口、错误处理和本地引用方法见 [Go SDK 规范](docs/GO_SDK.md) 与 [SDK README](sdk/go/README.md)，当前尚无已发布标签。

## 安装依赖、检查与构建

需要 Go 1.26、Node.js 22 和 pnpm 10.6.3。先安装前端依赖，再进行检查或构建：

```sh
pnpm --dir web install --frozen-lockfile
make check
make build
```

`make check` 构建真实前端资源，执行前端单元测试、服务端单测/race/vet，以及 SDK 独立单测/race/vet；这些检查不启动 HTTP、数据库或真实平台。`make build` 输出 `bin/posthaste`，不运行程序。

Go embed 在编译时需要真实 `web/dist`，因此不能跳过前端 build 直接编译一个干净 checkout。`node_modules`、`dist`、测试产物和二进制不入库。main/PR 的 Checks 先构建前端再检查 Go，SDK 使用独立 job；标签发布复用同一套检查，通过后才发布制品，不自动部署。

## 标签发布与容器

[Release 工作流](.github/workflows/release.yml) 仅由仓库根版本标签 `vX.Y.Z` 的 push 触发，稳定版本号不接受前导零、预发布或构建后缀。发布标签解析出的提交必须属于 `origin/main` 历史，可以发布已合入 main 的历史提交，不限于当前 main 顶端。

发布顺序是：先让工作流和待发布代码进入 main，再为满足条件的提交创建并推送版本标签；被打标签的提交本身必须包含发布工作流、GoReleaser 配置和 Dockerfile，因为 GitHub 按标签提交读取工作流。工作流执行版本/分支归属 gate → 可复用 Checks → GoReleaser 发布。SDK 的 `sdk/go/vX.Y.Z` 标签独立管理，不触发服务端发布。仅修改配置或执行本地 snapshot 验证不会正式发布，显式推送版本标签后才触发发布流程。

一次成功发布产出：

- GitHub 同名标签的 Release：`posthaste_vX.Y.Z_linux_amd64.tar.gz`、`posthaste_vX.Y.Z_linux_arm64.tar.gz` 和 SHA256 `checksums.txt`；归档仅含静态程序与公开占位配置 `.env.example`。
- 多架构镜像：`ghcr.io/real-uangi/posthaste:vX.Y.Z`，支持 `linux/amd64`、`linux/arm64`，并更新 `latest`。`latest` 指最近一次成功的镜像发布，不保证是当前 main 或最高版本；部署可固定版本标签。

镜像使用含 CA 证书的 `gcr.io/distroless/static-debian13:nonroot`，默认 `RUN_MODE=release`、`HTTP_PORT=8080`。镜像只包含对应架构的静态程序，不包含 `.env`、运行时密钥或数据库；制品和镜像分别由 [GoReleaser 配置](.goreleaser.yaml) 与 [Dockerfile](Dockerfile) 定义。

GHCR 首个 package 默认是 private。首发后，组织管理员需手动将其设置为 public，并验收匿名拉取；后续发布继承 package 可见性，源码仓库 public 不会自动公开镜像包。

镜像发布且已完成可见性设置后，可按以下示例运行；命令仅为部署说明，本次未执行：

```sh
docker pull ghcr.io/real-uangi/posthaste:v0.1.0
docker run --rm --name posthaste --env-file .env -p 8080:8080 ghcr.io/real-uangi/posthaste:v0.1.0
```

运行前按下节配置容器可访问的 PostgreSQL DSN、公开 HTTPS 地址和真实密钥；容器不会自动创建数据库。归档中的静态程序也可解压后直接运行，使用同一套环境配置。

## 配置与运行前提

部署需要 PostgreSQL、HTTPS 反向代理，以及到飞书／Telegram 官方 API 的网络连接。默认单实例，后台与 API 同源。关键配置如下：

| 配置 | 用途 |
| --- | --- |
| `DB_DSN` | allingo PostgreSQL 连接串 |
| `POSTHASTE_PUBLIC_URL` | 后台公开 HTTPS origin，用于同源校验 |
| `POSTHASTE_ADMIN_PASSWORD` | 管理员口令 |
| `POSTHASTE_INGRESS_TOKEN` | 独立消息入口 Bearer Token |
| `POSTHASTE_ENCRYPTION_KEY` | 标准 Base64 编码的 32 字节平台凭证加密密钥 |
| `RUN_MODE=release` | 生产 allingo 模式，避免调试监听 |

无默认管理员口令、入口 Token 或主密钥。以 [.env.example](.env.example) 为模板创建本地 `.env`，替换所有占位值；平台凭证密钥为随机 32 字节的标准 Base64 表达，例如由 `openssl rand -base64 32` 生成。完整配置见 [系统设计](DESIGN.md)，修改主密钥需要显式处理已有凭证。

配置好外部依赖后，部署时运行 `./bin/posthaste`。启动会连接 PostgreSQL、迁移声明的四类配置模型，并幂等创建空默认组，然后开始 HTTP 监听。反向代理将公开 HTTPS 根路径转发到 `HTTP_PORT`，访问首页使用管理员口令登录，先添加平台账号，再在账号下添加出口，最后绑定默认或 caller 路由组。首次默认组为空，配置前提交消息会返回 503。

账号拆分属于未发布首版的配置 schema 调整；旧目标数据不能仅靠 AutoMigrate 自动变成账号和出口，旧密文也不能直接改用账号 ID 解密。已有旧配置库时，应按 [系统设计](DESIGN.md) 显式迁移，不启动自动回填或清库逻辑。

入口 Token、后台口令和凭证主密钥互相独立。不要提交 `.env`；示例文件不会自动创建数据库、生成真实凭证或启动服务。程序需要访问真实依赖，以上部署步骤未作为本地单测执行。

## 验证范围与后续维护

实现按基础装配与配置 → 配置存储及认证 → 消息链路和平台 Sender → HTTP/Go SDK → 后台与静态资源嵌入组织。后续变更继续以 [系统设计](DESIGN.md) 和 [工程规范](AGENTS.md) 为准。

默认验证使用静态检查、编译、纯单元测试、HTTP recorder、模拟存储及 RoundTripper；存储测试使用无 socket 的 SQL 驱动验证查询、事务与模型声明，不连接 PostgreSQL。真实 PostgreSQL 的迁移、并发行为，浏览器交互和平台实际接收结果仍需另行验收，不以模拟测试替代。

服务端和 SDK 独立检查；根 `go test ./...` 不覆盖嵌套 SDK 模块。SDK 发布使用 `sdk/go/vX.Y.Z` 标签，不与根服务版本绑定。发布、部署和推送须按单独授权执行。
