# Posthaste Go SDK

这是已实现的独立 Go 模块，仅依赖标准库，通过 Posthaste HTTP v1 API 提交消息。模块路径为 `github.com/real-uangi/posthaste/sdk/go`，包名为 `posthaste`，要求 Go 1.26.0 或更新版本。

当前代码已包含 SDK 与单元测试，但**尚未创建或发布版本标签**。同仓开发可在接入项目中通过本地 `replace` 使用；未来发布时使用子模块标签 `sdk/go/v0.1.0`，不能把仓库根标签当作 SDK 版本。

## 发送消息

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
		BaseURL: "https://notify.example.com/posthaste/",
		Token:   os.Getenv("POSTHASTE_TOKEN"),
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	accepted, err := client.Send(ctx, posthaste.SendRequest{
		Caller:  "production",
		Title:   "服务告警",
		Message: "**订单服务**错误率超过阈值",
		Format:  posthaste.FormatMarkdown,
	})
	if err != nil {
		var apiErr *posthaste.APIError
		switch {
		case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
			fmt.Fprintln(os.Stderr, "请求取消或超时，接收结果未知")
		case errors.As(err, &apiErr):
			fmt.Fprintf(os.Stderr, "HTTP %d，错误码 %d\n", apiErr.StatusCode, apiErr.Code)
		default:
			fmt.Fprintln(os.Stderr, err)
		}
		return
	}
	fmt.Println("已进入内存队列：", accepted.MessageID)
}
```

省略 `Caller` 时服务端使用默认组，未知 caller 也由服务端回退；省略 `Format` 时服务端使用 `text`。SDK 保留 caller 原文并进行 query 编码，不做 trim、大小写转换或平台格式转换。

成功只表示真实 HTTP 202 和有效受理信封，**不代表目标平台已经收到**。messageId 仅用于日志关联；SDK 不重试、不保存队列、不查询投递历史。网络超时后重新提交可能重复发送。

## HTTP 配置

`BaseURL` 支持 HTTP(S) 和反向代理路径前缀，不接受 userinfo、query 或 fragment，`Token` 必须非空。默认每个 SDK Client 拥有独立的 `http.Client`，超时 10 秒，禁止自动跟随重定向。

可通过 `Config.HTTPClient` 注入自有 `*http.Client`；SDK 不修改该对象。调用方负责其超时、重定向、代理、TLS 和 Transport 行为，并保证共享时的并发安全。`Send` 只执行一次 HTTP 调用流程，注入客户端自身的重定向或 Transport 行为仍由调用方控制。

同一 SDK Client 可并发发送。有效非 202 错误信封返回 `*APIError`，只暴露数字状态和错误码；畸形响应返回带 HTTP 状态的安全协议错误，不输出响应原文。网络与 context 错误保留错误链。

## 独立验证

在本目录执行：

```sh
GOWORK=off go test ./...
GOWORK=off go test -race ./...
GOWORK=off go vet ./...
```

测试使用模拟 RoundTripper 和 HTTP recorder，不监听端口、不启动服务端、不连接数据库。根仓库的 `go test ./...` 不覆盖本独立子模块。

完整协议与公开类型见 [SDK 规范](../../docs/GO_SDK.md) 和 [HTTP API](../../docs/API.md)。SDK 不导入服务端内部类型，不封装后台配置接口。
