# Posthaste HTTP API

本文定义 Posthaste 首版的 HTTP 契约，供消息调用方、管理后台和 Go SDK 共用。它描述计划实现的接口，不代表当前空白项目已经提供这些能力。

## 1. 通用约定

- API 前缀为 `/api/v1`；请求和响应使用 UTF-8 JSON，带请求体时使用 `Content-Type: application/json`，允许附带 charset。
- 请求体上限为 **64 KiB**，超限返回 `413`；不支持的请求媒体类型返回 `415`。
- 所有资源 ID 和 `messageId` 均为 UUID 字符串；时间为 RFC 3339 字符串。
- 响应沿用 allingo envelope：`{code, data, message, time}`。`code` 为数字，必须等于真实 HTTP 状态码，不能用 HTTP 200 包装 code 202。
- envelope 的 `message` 是安全提示文案，不是可匹配的业务错误码。调用方依据 HTTP 状态和 `code` 处理结果，不匹配文案。
- 错误响应的 `data` 为 `null`；JSON 响应体和日志不暴露口令、会话值、入口 token、平台凭证、完整 webhook URL 或上游原始错误。登录仅通过受保护的 Set-Cookie 设置会话。

成功受理的实际 HTTP 状态为 `202 Accepted`：

```json
{
  "code": 202,
  "data": {
    "messageId": "21f719f9-3daa-46ee-a139-8dfd0b5ff3ab"
  },
  "message": "消息已进入内存队列",
  "time": "2026-11-15T10:00:00+08:00"
}
```

错误响应示例，实际 HTTP 状态为 `503 Service Unavailable`：

```json
{
  "code": 503,
  "data": null,
  "message": "没有可用的推送目标",
  "time": "2026-11-15T10:00:00+08:00"
}
```

## 2. 消息入口

### 2.1 接口与鉴权

| 方法 | 路径 | 鉴权 | 成功响应 |
| --- | --- | --- | --- |
| POST | `/api/v1/notify?caller=beszel` | `Authorization: Bearer <入口 token>` | `202`，`data={messageId}` |

所有调用方使用独立于管理后台的全局入口 Bearer token。该接口不接受管理后台 Cookie 代替 Bearer；后台口令和平台凭证也不能作为入口 token。`caller` 仅用于选择目标组，不承担身份认证。

请求体采用 Shoutrrr JSON 的 `title` / `message` 字段，并增加可选 `format`：

| 字段 | 类型 | 要求 | 含义 |
| --- | --- | --- | --- |
| `title` | string | 可省略 | 消息标题 |
| `message` | string | 必填、非空 | 消息正文 |
| `format` | string | 可省略 | `text` 或 `markdown`；省略为 `text` |

允许额外的未知 JSON 字段，但不使用它们参与路由、目标选择或平台投递。`caller` **只从 query 读取**；请求体中的同名字段没有作用。该接口接收统一 JSON，不解析各告警平台的原生事件格式。

### 2.2 caller 与目标组

- `caller` 区分大小写，按 query 解码后的字符串原样精确匹配，不做 trim 或大小写转换。
- `caller` 命中已配置目标组时，使用该组中已启用的目标。
- `caller` 缺省、为空或未知时，使用 `caller=default` 的默认组。这是明确的默认路由行为，不返回“未知 caller”错误。
- 已命中组为空，或成员全部禁用时，返回 `503`，**不再回退到默认组**。
- 默认组没有可用目标时同样返回 `503`。
- 目标成员去重，同一目标不会因为重复成员而重复投递。不同目标即使属于同一平台，也各自尝试投递。

### 2.3 文本与 Markdown 示例

纯文本请求，省略 `format`，使用默认组：

```http
POST /api/v1/notify HTTP/1.1
Authorization: Bearer <入口 token>
Content-Type: application/json

{
  "title": "部署完成",
  "message": "Posthaste 已完成本次部署。"
}
```

Markdown 请求，通过 caller 选择预配置目标组：

```http
POST /api/v1/notify?caller=beszel HTTP/1.1
Authorization: Bearer <入口 token>
Content-Type: application/json

{
  "title": "磁盘空间告警",
  "message": "**主机 web-01**\n\n磁盘使用率达到 **90%**。\n\n[查看监控](https://beszel.example.com/)",
  "format": "markdown"
}
```

标题始终按普通文本处理。`text` 正文按普通文本发送；`markdown` 正文由平台适配器映射到飞书卡片 2.0 的 Markdown 元素和 Telegram `sendRichMessage`，不使用 Telegram 旧版 Markdown parse mode。

共同基础语法包括 `**粗体**`、`*斜体*`、HTTP(S) 链接、行内与围栏代码、列表和引用。平台之间的 Markdown 支持存在差异，Posthaste 不承诺扩展语法或复杂嵌套在两个平台呈现完全相同的效果，也不引入通用 Markdown 转换引擎。

### 2.4 Beszel Generic 接入示例

在后台创建 `caller=beszel` 的目标组，绑定一个或多个已启用目标。在 Beszel 的 Notifications 中填写以下 **Shoutrrr Generic 配置 URL**：

```text
generic://notify.example.com/api/v1/notify?template=json&caller=beszel&@Authorization=Bearer%20YOUR_TOKEN
```

将 `notify.example.com` 和 `YOUR_TOKEN` 替换为实际域名与入口 token；token 作为 URL 参数值时需要 URL 编码。`template=json` 选择 Shoutrrr JSON 模板，`@Authorization` 被 Generic 转换为 HTTP 请求头 `Authorization: Bearer YOUR_TOKEN`，`caller=beszel` 转发至 Posthaste。

上述 `generic://` 是填入 Beszel 的配置，不是 HTTP 服务监听协议。Generic 最终向 `https://notify.example.com/api/v1/notify?caller=beszel` 发出带鉴权头的 POST 请求；不要用这个普通 HTTPS 地址代替包含模板和鉴权头配置的通知 URL。token 不作为 Posthaste 的 query 鉴权参数。

Beszel Generic 使用 Shoutrrr JSON 格式，其 `title` / `message` 可直接由本接口接收；未提供 `format` 时按文本推送。`beszel` 不是 Posthaste 的内置平台解析器：它仅是管理员配置的 caller。即使未配置该 caller，也会按照上述规则进入默认组。

### 2.5 受理与投递语义

`202` 只表示消息已进入当前进程的 eventbus 内存队列，不能表示平台已接收、用户已收到或持久化成功。`messageId` 用于日志关联，不能用于查询消息记录；首版不提供消息历史、回执查询或投递状态接口。

应用在入队前解析目标组并构造消息与目标配置的不可变快照；入队后的配置变更不修改该消息的快照。静态订阅处理器逐个目标尝试投递，一个目标失败后继续尝试其他目标。每个目标只进行一次投递尝试；没有自动重试、幂等受理、进程重启恢复或送达保证。重复提交会作为独立消息处理。

eventbus 入队失败或等待队列超时返回 `503`，不返回虚假的 `202`。平台超时、凭证失效、限流、Markdown 或长度限制等异步投递失败只记录安全日志，不改变已经返回的 HTTP 响应。

## 3. 管理后台

后台 SPA 与 API 同源。管理接口统一位于 `/api/v1/admin`，使用单管理员环境口令登录和 Cookie 会话，不复用消息入口 Bearer，也不提供账户注册、多角色或多租户接口。

### 3.1 登录与会话

| 方法 | 路径（相对管理前缀） | 请求体 | 成功响应 data |
| --- | --- | --- | --- |
| POST | `/auth/login` | `{password}` | `{authenticated:true,expiresAt}` |
| GET | `/auth/session` | 无 | `{authenticated:true,expiresAt}` |
| POST | `/auth/logout` | 无 | `null` |

登录和普通管理 CRUD 成功返回 `200`；`expiresAt` 为 RFC 3339 时间。登录失败、会话不存在或已过期返回 `401`。登录接口不要求已有会话，其余管理接口要求有效会话。

会话固定有效期为 12 小时，不滑动续期；退出或进程重启后失效。Cookie 名为 `posthaste_session`，属性为 `HttpOnly`、`SameSite=Lax`、`Path=/api/v1/admin`，不设置跨域 Domain，生产环境启用 `Secure`。会话值不出现在 JSON 响应中。

**全部管理 POST，包括登录和退出，都验证同源 Origin。** Origin 必须与配置的 `POSTHASTE_PUBLIC_URL` origin 相同，缺失或不符返回 `403`；不将请求 Host 作为可信的期望来源。前端通过同源请求携带 Cookie；外部服务应使用消息入口，不通过后台 Cookie 发消息。

登录示例：

```http
POST /api/v1/admin/auth/login HTTP/1.1
Origin: https://posthaste.example.com
Content-Type: application/json

{"password":"<管理员环境口令>"}
```

```json
{
  "code": 200,
  "data": {
    "authenticated": true,
    "expiresAt": "2026-11-15T22:00:00+08:00"
  },
  "message": "登录成功",
  "time": "2026-11-15T10:00:00+08:00"
}
```

### 3.2 推送目标

| 方法 | 路径（相对管理前缀） | 请求/用途 | 成功响应 data |
| --- | --- | --- | --- |
| GET | `/targets/list` | 列出全部配置目标，无分页 | `{items:[Target]}` |
| POST | `/targets/save` | 创建或整体更新目标配置 | `Target` |
| POST | `/targets/delete` | `{id}` | `null` |
| POST | `/targets/test` | `{id,title?,message,format?}` | `202`，`{messageId}` |

`Target` 的公开形状为：

```json
{
  "id": "a6f0e5f9-4eb8-4791-b2b3-91555592cd54",
  "name": "运维飞书群",
  "platform": "feishu",
  "enabled": true,
  "config": {},
  "credentialsConfigured": true
}
```

返回对象永远不包含 `credentials`。`credentialsConfigured` 仅说明已经配置凭证，不代表经过平台认证或最近投递成功。

`targets/save` 的 `name`、`platform`、`enabled`、`config` 必填。省略 `id` 创建，提供 `id` 更新已有目标；`platform` 仅接受 `feishu` / `telegram`，创建后不可修改。

创建必须提供 `credentials`；更新省略 `credentials` 保留原值，提供则整体替换，不按内部字段合并，也不接受前端掩码作为真实凭证。配置无效、平台不支持或试图修改平台返回 `400`。更新不存在的 ID 返回 `404`。

| 平台 | config | credentials（仅写） |
| --- | --- | --- |
| `feishu` | `{}` | `{webhookUrl:string,signingSecret?:string}` |
| `telegram` | `{chatId:string,messageThreadId?:正整数}` | `{botToken:string}` |

飞书目标的创建示例：

```json
{
  "name": "运维飞书群",
  "platform": "feishu",
  "enabled": true,
  "config": {},
  "credentials": {
    "webhookUrl": "https://open.feishu.cn/open-apis/bot/v2/hook/<机器人标识>",
    "signingSecret": "<签名密钥>"
  }
}
```

Telegram 目标使用 `platform="telegram"`，配置示例为 `{"chatId":"-1001234567890","messageThreadId":123}`，凭证为 `{"botToken":"<Bot token>"}`；无需话题时省略 `messageThreadId`。更新时若只修改名称、开关或 config，省略整个 `credentials` 字段即可保留原凭证。

仍被目标组引用的目标不能删除，返回 `409`，先解除目标组绑定。`targets/test` 只选择指定目标，复用同一 eventbus 投递链路；不存在返回 `404`，已禁用返回 `503`。测试成功受理仍是 `202`，不提供同步平台成功回执。

### 3.3 目标组

| 方法 | 路径（相对管理前缀） | 请求/用途 | 成功响应 data |
| --- | --- | --- | --- |
| GET | `/groups/list` | 列出全部目标组，无分页 | `{items:[Group]}` |
| POST | `/groups/save` | `{id?,name,caller,targetIds}` | `Group` |
| POST | `/groups/delete` | `{id}` | `null` |

`Group` 的公开形状和保存示例：

```json
{
  "id": "805d2f43-b874-4ccf-90ba-b6415071e9e5",
  "name": "Beszel 运维告警",
  "caller": "beszel",
  "targetIds": [
    "a6f0e5f9-4eb8-4791-b2b3-91555592cd54"
  ]
}
```

省略 `id` 创建，提供 `id` 更新；`name`、`caller`、`targetIds` 必填。`caller` 在所有目标组中唯一，重复返回 `409`；`targetIds` 按目标 ID 去重，引用不存在的目标返回 `404`。允许保存空组或包含禁用目标的组，但触发时无可用目标将返回 `503`。

默认组通过 `caller="default"` 标识，固定存在；可以修改名称和成员，不能删除或修改 caller，违反时返回 `409`。删除其他组后，原 caller 将成为未知 caller，后续消息按照默认路由规则进入默认组。

## 4. 状态与安全错误映射

| HTTP / code | 含义 |
| --- | --- |
| `200` | 登录、会话查询、退出和普通配置操作成功 |
| `202` | 普通发送或目标测试已进入内存队列 |
| `400` | JSON/字段/UUID 非法、消息为空、format 不支持、平台或配置不合法 |
| `401` | 入口 Bearer 无效、管理员口令错误或管理会话无效 |
| `403` | 管理 POST 的 Origin 校验失败 |
| `404` | 指定资源或引用的目标不存在，或 API 路由不存在 |
| `409` | caller 重复、删除已绑定目标、删除默认组或修改默认 caller |
| `413` | 请求体超过 64 KiB |
| `415` | 请求媒体类型不支持 |
| `500` | 未分类的内部错误，响应仅提供安全文案 |
| `503` | 无可用目标、eventbus 入队失败/超时或配置存储暂不可用 |

不向响应传递数据库 SQL、连接串、平台响应原文、网络请求 URL 或 SDK 原始错误。入口与管理后台均只暴露上述稳定状态语义；平台发送在受理后发生，其错误不能映射回已完成的发送 HTTP 请求。

## 5. Go SDK 与官方参考

Go SDK 仅封装上述消息入口与同一 envelope，不另建 wire 协议；只有真实 HTTP 202 且成功解析 `data.messageId` 才返回受理结果。它不封装管理 CRUD，不自动重试，不把 202 描述为已送达。详见 [Go SDK 规范](GO_SDK.md)。

- [Beszel Generic 通知](https://beszel.dev/guide/notifications/generic)：Generic 自定义鉴权头及 Shoutrrr JSON。
- [飞书自定义机器人发送卡片](https://open.feishu.cn/document/feishu-cards/quick-start/send-message-cards-with-custom-bot)：卡片 2.0 与 Markdown 元素。
- [Telegram sendRichMessage](https://core.telegram.org/bots/api#sendrichmessage)、[Rich message formatting options](https://core.telegram.org/bots/api#rich-message-formatting-options)：Markdown 消息接口与支持范围。

这些平台映射以 2026-11-15 核对的官方文档为设计依据；未来实现或升级平台接口时应重新核对。
