package infra

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/real-uangi/posthaste/internal/notification/domain"
	"github.com/real-uangi/posthaste/internal/shared/config"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return fn(req) }

type trackedBody struct {
	io.Reader
	closed bool
}

func (b *trackedBody) Close() error { b.closed = true; return nil }

func TestTelegramTextAndRichMarkdownPayloads(t *testing.T) {
	for _, format := range []string{"text", "markdown"} {
		t.Run(format, func(t *testing.T) {
			calls := 0
			body := &trackedBody{Reader: strings.NewReader(`{"ok":true,"result":{"message_id":7}}`)}
			client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				calls++
				method := "sendMessage"
				if format == "markdown" {
					method = "sendRichMessage"
				}
				if req.Method != "POST" || req.URL.String() != "https://api.telegram.org/bot123:secret/"+method || req.Header.Get("Content-Type") != "application/json" {
					t.Fatal("unexpected Telegram request")
				}
				var payload map[string]json.RawMessage
				if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
					t.Fatal(err)
				}
				if string(payload["chat_id"]) != `"-100123"` || string(payload["message_thread_id"]) != "42" {
					t.Fatal("destination or topic lost")
				}
				if _, exists := payload["parse_mode"]; exists {
					t.Fatal("legacy parse mode used")
				}
				if format == "text" {
					var text string
					_ = json.Unmarshal(payload["text"], &text)
					if text != "==title== $x$ &amp;\n\n**body**" {
						t.Fatalf("text = %q", text)
					}
				} else {
					var rich map[string]string
					_ = json.Unmarshal(payload["rich_message"], &rich)
					if rich["markdown"] != "\\=\\=title\\=\\= \\$x\\$ \\&amp\\;\n\n**body**" {
						t.Fatalf("Markdown title was interpreted: %q", rich["markdown"])
					}
					if _, exists := payload["text"]; exists {
						t.Fatal("rich and plain fields mixed")
					}
				}
				return &http.Response{StatusCode: 200, Body: body, Header: make(http.Header)}, nil
			})}
			sender := &TelegramSender{client: client}
			err := sender.Send(context.Background(), domain.TargetSnapshot{Config: json.RawMessage(`{"chatId":"-100123","messageThreadId":42}`), Credentials: json.RawMessage(`{"botToken":"123:secret"}`)}, domain.Message{Title: "==title== $x$ &amp;", Message: "**body**", Format: format})
			if err != nil || calls != 1 || !body.closed {
				t.Fatalf("err=%v calls=%d closed=%t", err, calls, body.closed)
			}
		})
	}
}

func TestFeishuTextAndCardSignature(t *testing.T) {
	for _, format := range []string{"text", "markdown"} {
		t.Run(format, func(t *testing.T) {
			client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				if req.URL.String() != "https://open.feishu.cn/open-apis/bot/v2/hook/private" {
					t.Fatal("wrong webhook")
				}
				var payload map[string]json.RawMessage
				if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
					t.Fatal(err)
				}
				var timestamp, sign string
				_ = json.Unmarshal(payload["timestamp"], &timestamp)
				_ = json.Unmarshal(payload["sign"], &sign)
				h := hmac.New(sha256.New, []byte(timestamp+"\nshared-secret"))
				if timestamp == "" || sign != base64.StdEncoding.EncodeToString(h.Sum(nil)) {
					t.Fatal("incorrect Feishu signature")
				}
				if format == "text" {
					var content map[string]string
					_ = json.Unmarshal(payload["content"], &content)
					if string(payload["msg_type"]) != `"text"` || content["text"] != "*plain title*\n\n**body**" {
						t.Fatal("wrong text payload")
					}
				} else {
					var card struct {
						Schema string                                        `json:"schema"`
						Header struct{ Title struct{ Tag, Content string } } `json:"header"`
						Body   struct {
							Elements []struct{ Tag, Content string }
						} `json:"body"`
					}
					if err := json.Unmarshal(payload["card"], &card); err != nil {
						t.Fatal(err)
					}
					if string(payload["msg_type"]) != `"interactive"` || card.Schema != "2.0" || card.Header.Title.Tag != "plain_text" || card.Header.Title.Content != "*plain title*" || len(card.Body.Elements) != 1 || card.Body.Elements[0].Tag != "markdown" || card.Body.Elements[0].Content != "**body**" {
						t.Fatal("wrong card schema or title formatting")
					}
				}
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"code":0,"msg":"success"}`))}, nil
			})}
			sender := &FeishuSender{client: client}
			err := sender.Send(context.Background(), domain.TargetSnapshot{Credentials: json.RawMessage(`{"webhookUrl":"https://open.feishu.cn/open-apis/bot/v2/hook/private","signingSecret":"shared-secret"}`)}, domain.Message{Title: "*plain title*", Message: "**body**", Format: format})
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestPlatformResponsesAndErrorsAreSafeAndNotRetried(t *testing.T) {
	for _, platform := range []string{"feishu", "telegram"} {
		for _, tc := range []struct {
			name    string
			status  int
			body    string
			network bool
		}{
			{"http failure", 429, `private-response-secret`, false},
			{"business failure", 200, `{"code":19021,"ok":false,"description":"private-response-secret"}`, false},
			{"missing business result", 200, `{}`, false},
			{"wrong business type", 200, `{"ok":"true","code":"0"}`, false},
			{"malformed response", 200, `private-response-secret`, false},
			{"trailing garbage", 200, `{"code":0,"ok":true} private-response-secret`, false},
			{"second JSON value", 200, `{"code":0,"ok":true} {}`, false},
			{"network failure", 0, "", true},
		} {
			t.Run(platform+"/"+tc.name, func(t *testing.T) {
				calls := 0
				body := &trackedBody{Reader: strings.NewReader(tc.body)}
				client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
					calls++
					if tc.network {
						return nil, errors.New("private-network-secret " + req.URL.String())
					}
					return &http.Response{StatusCode: tc.status, Body: body}, nil
				})}
				var err error
				if platform == "telegram" {
					err = (&TelegramSender{client: client}).Send(context.Background(), domain.TargetSnapshot{Config: json.RawMessage(`{"chatId":"-1001"}`), Credentials: json.RawMessage(`{"botToken":"123:private-token"}`)}, domain.Message{Message: "body"})
				} else {
					err = (&FeishuSender{client: client}).Send(context.Background(), domain.TargetSnapshot{Credentials: json.RawMessage(`{"webhookUrl":"https://open.feishu.cn/open-apis/bot/v2/hook/private-token"}`)}, domain.Message{Message: "body"})
				}
				if err == nil || !errors.Is(err, domain.ErrUnavailable) || strings.Contains(err.Error(), "private-") || strings.Contains(err.Error(), "https://") {
					t.Fatalf("unsafe or missing error: %v", err)
				}
				if calls != 1 || (!tc.network && !body.closed) {
					t.Fatalf("calls=%d closed=%t", calls, body.closed)
				}
			})
		}
	}
}

func TestPlatformConfigValidationKeepsCredentialsOutOfPublicConfig(t *testing.T) {
	feishu := &FeishuSender{}
	telegram := &TelegramSender{}
	for _, tc := range []struct {
		sender      domain.Sender
		cfg, secret string
		valid       bool
	}{
		{feishu, `{}`, `{"webhookUrl":"https://open.feishu.cn/open-apis/bot/v2/hook/valid-id"}`, true},
		{feishu, `{}`, `{"webhookUrl":"http://open.feishu.cn/open-apis/bot/v2/hook/id"}`, false},
		{feishu, `{}`, `{"webhookUrl":"https://evil.example/open-apis/bot/v2/hook/id"}`, false},
		{feishu, `{}`, `{"webhookUrl":"https://open.feishu.cn/open-apis/bot/v2/hook/id?secret=value"}`, false},
		{feishu, `{"webhookUrl":"secret"}`, `{"webhookUrl":"https://open.feishu.cn/open-apis/bot/v2/hook/id"}`, false},
		{telegram, `{"chatId":"-1001","messageThreadId":1}`, `{"botToken":"123:token_value"}`, true},
		{telegram, `{"chatId":"-1001","messageThreadId":0}`, `{"botToken":"123:token"}`, false},
		{telegram, `{"chatId":"-1001","botToken":"secret"}`, `{"botToken":"123:token"}`, false},
		{telegram, `{"chatId":"-1001"}`, `{"botToken":"123:token/other"}`, false},
		{telegram, `{"chatId":"-1001"}`, `null`, false},
	} {
		err := tc.sender.ValidateTarget(json.RawMessage(tc.cfg))
		if err == nil {
			err = tc.sender.ValidateAccount(json.RawMessage(tc.secret))
		}
		if (err == nil) != tc.valid {
			t.Fatalf("%s valid=%t error=%v", tc.sender.Platform(), tc.valid, err)
		}
	}
	if err := telegram.ValidateTarget(json.RawMessage(`{"chatId":"-1001"}`)); err != nil {
		t.Fatal("destination validation unexpectedly needs account credentials")
	}
	for _, client := range []*http.Client{NewFeishuSender(&config.Config{SendTimeout: time.Second}).client, NewTelegramSender(&config.Config{SendTimeout: time.Second}).client} {
		if client.Timeout != time.Second || client.CheckRedirect(nil, nil) != http.ErrUseLastResponse {
			t.Fatal("platform timeout/redirect policy missing")
		}
	}
}
