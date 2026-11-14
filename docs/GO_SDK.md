# Go SDK 设计与使用规范

本文定义 Posthaste Go SDK 的目标接口、HTTP 行为和独立模块约束。**当前项目仅完成设计，SDK 尚未实现；下文代码是目标 API 的使用示例，不能视为已有可安装版本。**

## 1. 定位与模块边界

SDK 只负责通过 Posthaste HTTP v1 接口发送消息，不提供管理后台 CRUD、飞书或 Telegram 的直接调用、平台格式转换、异步队列、自动重试、幂等、恢复或日志框架。鉴权、caller 路由、目标选择及业务参数校验由服务端负责。

- 同仓放置于 `sdk/go/`，拥有独立 `go.mod`。
- module：`github.com/real-uangi/posthaste/sdk/go`；package：`posthaste`。
- 只依赖 Go 标准库，不引用服务端 `internal` 包或服务端 `go.mod`，不引入 options 模式和代码生成。
- 仅手写请求、响应解析所需的最少 wire DTO；公开类型与服务端实现类型分别维护，以 HTTP 契约保持一致。

## 2. 公开接口

```go
package posthaste

import (
	"context"
	"net/http"
)

type Config struct {
	BaseURL    string
	Token      string
	HTTPClient *http.Client
}

type Format string

const (
	FormatText     Format = "text"
	FormatMarkdown Format = "markdown"
)

type SendRequest struct {
	Caller  string
	Title   string
	Message string
	Format  Format
}

type Acceptance struct {
	MessageID string
}

type APIError struct {
	StatusCode int
	Code       int
}

func NewClient(cfg Config) (*Client, error)
func (c *Client) Send(ctx context.Context, req SendRequest) (Acceptance, error)
func (e *APIError) Error() string
```

`Client` 隐藏内部实现。`Send` 成功返回 `Acceptance`；失败返回零值 `Acceptance` 与错误。`APIError` 通过指针返回，调用方使用 `errors.As` 识别；其 `Error()` 只包含安全的 HTTP 状态和错误码，例如 `posthaste: HTTP 401 (code 401)`，不包含服务端原文。

### 客户端构造

`NewClient` 在配置进入 SDK 时完成一次校验：`BaseURL` 必须是包含 host 的绝对 HTTP(S) URL，不接受 userinfo、query 或 fragment；`Token` 必须非空。构造失败不发出网络请求，错误中不回显 token 或原始敏感配置。支持反向代理的路径前缀，例如 `https://example.com/posthaste/` 对应 `/posthaste/api/v1/notify`，不能用以 `/` 开头的相对地址解析方式覆盖前缀。

`HTTPClient == nil` 时，新建本 SDK 实例专用的 `http.Client`，超时为 10 秒，`CheckRedirect` 返回 `http.ErrUseLastResponse`，禁止自动跟随重定向。不要修改全局 `http.DefaultClient`。

提供 `HTTPClient` 时直接使用它，不修改它的 Timeout、Transport 或 CheckRedirect；超时、重定向、代理和 TLS 行为由调用方负责。SDK 每次 `Send` 只调用一次 HTTP 执行流程，不增加重试；注入的 Client 或 Transport 自身行为仍由调用方负责。

### 单次发送

`Send` 使用传入的 context 创建独立请求，不在 Client 中共享可变的请求、body buffer 或响应状态；因此同一 Client 可并发使用，调用方需保证注入的 HTTPClient/Transport 也满足其并发使用契约。

SDK 负责 URL 编码、JSON 编码和响应解析，不重复实现服务端的正文、长度、caller 是否存在等业务校验。调用方可以通过 context 提前取消请求；HTTPClient 的超时与 context 中先到期的限制生效。

## 3. HTTP 协议映射

请求为 `POST /api/v1/notify`，使用 `Authorization: Bearer <Token>` 与 `Content-Type: application/json`。

| Go 字段 | HTTP 表达 | 语义 |
| --- | --- | --- |
| `Caller` | query 参数 `caller` | 使用 URL query 编码；空值省略。未知 caller 由服务端回退到默认组 |
| `Title` | JSON `title` | 可选标题，空值可以省略 |
| `Message` | JSON `message` | 服务端要求正文非空 |
| `Format` | JSON `format` | 空值省略，由服务端默认使用 `text`；可显式使用 `text` 或 `markdown` |

请求 body 上限为 64 KiB，由服务端按实际接收的 HTTP body 执行。SDK 不另建一套可能与服务端不同的大小或格式校验规则。

服务端统一使用 allingo 信封：`code` 为数值 HTTP 状态码，`data` 为结果或 `null`，`message` 为安全文案，`time` 为 RFC3339 时间。

```json
{
  "code": 202,
  "data": {"messageId": "21f719f9-3daa-46ee-a139-8dfd0b5ff3ab"},
  "message": "accepted",
  "time": "2026-11-15T00:00:00+08:00"
}
```

错误响应使用同一信封，`data` 为 `null`，例如 HTTP 401 对应 `code: 401`。

SDK 只解析本用例使用的 `code`、`data` 和成功结果中的 `messageId`，检查这些字段的必需性与类型，不把缺失字段的零值误当作有效响应。未知额外字段忽略；`message`、`time` 不参与受理判断，不解析或校验其文案和时间格式：

- 成功必须同时满足真实 HTTP 状态为 202、信封 `code` 为 202、`data.messageId` 为非空字符串，以及所解析字段的 JSON 类型正确。
- 非 202 且可解析为有效错误信封时，返回 `*APIError`；要求 `code` 为等于真实 HTTP 状态的数值，`data` 为 `null`。`StatusCode` 保存真实 HTTP 状态，`Code` 保存信封中的数值码。
- `code` 与真实 HTTP 状态不一致时返回普通协议错误，不返回 `Acceptance` 或 `APIError`。
- 非 JSON、畸形信封，或 202 响应中的成功结构不正确，返回普通协议错误；文案保留 HTTP 状态，不携带响应原文、服务端 `message` 或凭证。重定向响应也不会被当作接收成功。
- 网络与 context 错误使用 `%w` 保留错误链，让 `errors.Is` 能识别 `context.Canceled` 和 `context.DeadlineExceeded`；不将协议错误伪装为 `APIError`。
- 只要取得响应，就关闭 `response.Body`，包括解析失败路径。

**202 只代表服务端已将消息放入本进程内存队列。** `MessageID` 用于关联日志，不代表最终投递成功，也不是可查询投递历史的承诺。SDK 不等待飞书或 Telegram 接收结果；服务端重启、进程退出或后续发送失败均不会触发 SDK 自动补发。调用方也不能从网络超时推断服务端一定未接收，重提可能重复推送。

## 4. 目标 API 使用示例

以下完整示例展示默认组发送、caller 路由、Markdown、context 取消和错误分类。SDK 实现并发布后才可编译使用；示例不会执行任何后台管理操作。

```go
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/real-uangi/posthaste/sdk/go"
)

func main() {
	client, err := posthaste.NewClient(posthaste.Config{
		BaseURL: "https://notify.example.com/posthaste",
		Token:   os.Getenv("POSTHASTE_TOKEN"),
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return
	}

	// 省略 Caller 和 Format：服务端使用默认组及 text。
	accepted, err := client.Send(context.Background(), posthaste.SendRequest{
		Message: "定时备份已完成",
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return
	}
	fmt.Println("已进入内存队列：", accepted.MessageID)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	accepted, err = client.Send(ctx, posthaste.SendRequest{
		Caller:  "production",
		Title:   "服务告警",
		Message: "**订单服务**错误率超过阈值",
		Format:  posthaste.FormatMarkdown,
	})
	if err != nil {
		var apiErr *posthaste.APIError
		switch {
		case errors.Is(err, context.Canceled):
			fmt.Fprintln(os.Stderr, "请求已取消，接收结果未知")
		case errors.Is(err, context.DeadlineExceeded):
			fmt.Fprintln(os.Stderr, "请求已超时，接收结果未知")
		case errors.As(err, &apiErr):
			fmt.Fprintf(os.Stderr, "HTTP %d，错误码 %d\n",
				apiErr.StatusCode, apiErr.Code)
		default:
			fmt.Fprintln(os.Stderr, err)
		}
		return
	}
	fmt.Println("已进入内存队列：", accepted.MessageID)
}
```

需要自定义 HTTP 行为时，在构造阶段注入 Client；下面的函数显式保留禁止重定向的安全行为，SDK 不修改该 Client：

```go
package example

import (
	"net/http"
	"time"

	"github.com/real-uangi/posthaste/sdk/go"
)

func NewClient(token string) (*posthaste.Client, error) {
	httpClient := &http.Client{
		Timeout: 5 * time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	return posthaste.NewClient(posthaste.Config{
		BaseURL:    "https://notify.example.com/posthaste/",
		Token:      token,
		HTTPClient: httpClient,
	})
}
```

## 5. 验证与发布

SDK 实现后，使用模拟 `http.RoundTripper` 检查请求、返回响应；需要记录 HTTP handler 输出时使用 `httptest.NewRecorder`，不使用会监听端口的 `httptest.NewServer`。测试至少覆盖：

- 构造阶段 URL 与 token 校验；反向代理路径前缀、尾斜杠、caller query 编码与空字段省略。
- 正确的 202 接收结果、未来额外字段、必需字段缺失、HTTP/code 不一致、非 202 错误信封、非 JSON 和错误成功结构；message/time 元数据不影响受理判断。
- 默认 10 秒与禁止重定向配置；注入 Client 不被修改，且 SDK 不重复执行请求。
- context 取消、网络错误链、`errors.As`、response.Body 关闭及同一 Client 并发发送。

在 `sdk/go/` 中独立执行以下命令，不依赖服务端、数据库或工作区替换：

```sh
GOWORK=off go test ./...
GOWORK=off go test -race ./...
GOWORK=off go vet ./...
```

根目录的 `go test ./...` 不覆盖独立子模块，CI 必须单独检查 SDK。当前文档阶段不执行这些尚无实现的检查，也不据此声明测试通过。

SDK 面向服务端 HTTP v1 协议。已有 v1 服务端不应因 SDK 的兼容更新而要求新增字段或改变发送语义；可增加可选能力和响应字段，破坏性 HTTP 变更进入新的协议版本。

独立 Go 子模块首个计划版本的 tag 为 **`sdk/go/v0.1.0`**，不是仓库根模块的 `v0.1.0`。实现并正式发布后，调用方才使用：

```sh
go get github.com/real-uangi/posthaste/sdk/go@v0.1.0
```

SDK 版本与服务端发布版本分别管理，以 HTTP v1 契约保持兼容。当前工作不创建 tag、不推送、不发布包。
