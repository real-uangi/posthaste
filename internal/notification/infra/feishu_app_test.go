package infra

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/real-uangi/posthaste/internal/notification/domain"
	"github.com/real-uangi/posthaste/internal/shared/config"
)

const appCredentials = `{"appId":"cli_test","appSecret":"private-app-secret"}`
const appTokenResponse = `{"code":0,"access_token":"private-access-token","expires_in":7200,"token_type":"Bearer"}`

func newTestFeishuAppSender(transport roundTripFunc) *FeishuAppSender {
	sender := NewFeishuAppSender(&config.Config{SendTimeout: time.Second})
	sender.client.Transport = transport
	return sender
}

func TestFeishuAppValidationAndClientPolicy(t *testing.T) {
	sender := NewFeishuAppSender(&config.Config{SendTimeout: time.Second})
	if sender.Platform() != "feishu_app" || sender.client.Timeout != time.Second || sender.client.CheckRedirect(nil, nil) != http.ErrUseLastResponse {
		t.Fatal("application sender registration or HTTP policy is incorrect")
	}
	for _, tc := range []struct {
		name, credentials, target string
		valid                     bool
	}{
		{"valid", appCredentials, `{"chatId":"oc_group"}`, true},
		{"empty app ID", `{"appId":" ","appSecret":"secret"}`, `{"chatId":"oc_group"}`, false},
		{"missing app secret", `{"appId":"cli_test"}`, `{"chatId":"oc_group"}`, false},
		{"null credentials", `null`, `{"chatId":"oc_group"}`, false},
		{"unknown credential field", `{"appId":"cli_test","appSecret":"secret","webhookUrl":"private"}`, `{"chatId":"oc_group"}`, false},
		{"empty chat", appCredentials, `{"chatId":" "}`, false},
		{"null config", appCredentials, `null`, false},
		{"credentials in public config", appCredentials, `{"chatId":"oc_group","appSecret":"private"}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := sender.ValidateAccount(json.RawMessage(tc.credentials))
			if err == nil {
				err = sender.ValidateTarget(json.RawMessage(tc.target))
			}
			if (err == nil) != tc.valid {
				t.Fatal("unexpected application account/target validation result")
			}
		})
	}
}

func TestFeishuAppTextAndCardUseAuthenticatedMessageAPI(t *testing.T) {
	for _, format := range []string{"text", "markdown"} {
		t.Run(format, func(t *testing.T) {
			calls := 0
			var bodies []*trackedBody
			sender := newTestFeishuAppSender(roundTripFunc(func(req *http.Request) (*http.Response, error) {
				calls++
				response := appTokenResponse
				if calls == 1 {
					if req.Method != http.MethodPost || req.URL.String() != "https://accounts.feishu.cn/oauth/v3/token" || req.Header.Get("Authorization") != "" {
						t.Fatal("wrong application token request")
					}
					var payload map[string]string
					if err := json.NewDecoder(req.Body).Decode(&payload); err != nil || len(payload) != 3 || payload["grant_type"] != "client_credentials" || payload["client_id"] != "cli_test" || payload["client_secret"] != "private-app-secret" {
						t.Fatal("incorrect OAuth client credentials payload")
					}
				} else {
					if req.Method != http.MethodPost || req.URL.String() != "https://open.feishu.cn/open-apis/im/v1/messages?receive_id_type=chat_id" || req.Header.Get("Authorization") != "Bearer private-access-token" {
						t.Fatal("wrong application message endpoint or authentication")
					}
					var payload map[string]string
					if err := json.NewDecoder(req.Body).Decode(&payload); err != nil || len(payload) != 3 || payload["receive_id"] != "oc_group" {
						t.Fatal("wrong application destination or non-string message content")
					}
					if format == "text" {
						var content struct{ Text string }
						if err := json.Unmarshal([]byte(payload["content"]), &content); err != nil || payload["msg_type"] != "text" || content.Text != "*plain title*\n\n**body**" {
							t.Fatal("incorrect application text content")
						}
					} else {
						var card struct {
							Schema string
							Header struct{ Title struct{ Tag, Content string } }
							Body   struct {
								Elements []struct{ Tag, Content string }
							}
						}
						if err := json.Unmarshal([]byte(payload["content"]), &card); err != nil || payload["msg_type"] != "interactive" || card.Schema != "2.0" || card.Header.Title.Tag != "plain_text" || card.Header.Title.Content != "*plain title*" || len(card.Body.Elements) != 1 || card.Body.Elements[0].Tag != "markdown" || card.Body.Elements[0].Content != "**body**" {
							t.Fatal("incorrect application card schema or formatting")
						}
					}
					response = `{"code":0,"data":{"message_id":"om_test"}}`
				}
				if req.Header.Get("Content-Type") != "application/json" {
					t.Fatal("missing JSON request media type")
				}
				body := &trackedBody{Reader: strings.NewReader(response)}
				bodies = append(bodies, body)
				return &http.Response{StatusCode: http.StatusOK, Body: body}, nil
			}))
			err := sender.Send(context.Background(), domain.TargetSnapshot{Config: json.RawMessage(`{"chatId":"oc_group"}`), Credentials: json.RawMessage(appCredentials)}, domain.Message{Title: "*plain title*", Message: "**body**", Format: format})
			if err != nil || calls != 2 || len(bodies) != 2 || !bodies[0].closed || !bodies[1].closed {
				t.Fatal("application send failed, retried, or leaked a response body")
			}
		})
	}
}

func TestFeishuAppChatsArePaginatedAndReuseToken(t *testing.T) {
	tokenCalls, chatCalls := 0, 0
	sender := newTestFeishuAppSender(roundTripFunc(func(req *http.Request) (*http.Response, error) {
		response := appTokenResponse
		if req.URL.Host == "accounts.feishu.cn" {
			tokenCalls++
		} else {
			chatCalls++
			if req.Method != http.MethodGet || req.URL.Host != "open.feishu.cn" || req.URL.Path != "/open-apis/im/v1/chats" || req.Header.Get("Authorization") != "Bearer private-access-token" || req.Header.Get("Content-Type") != "application/json" || req.URL.Query().Get("page_size") != "100" || req.URL.Query().Get("sort_type") != "ByCreateTimeAsc" || req.Body != nil {
				t.Fatal("wrong application chat-list request")
			}
			if chatCalls == 1 {
				if req.URL.Query().Get("page_token") != "" {
					t.Fatal("first chat page unexpectedly uses a cursor")
				}
				response = `{"code":0,"data":{"items":[{"chat_id":"oc_group","name":"运维群"}],"has_more":true,"page_token":"cursor +&/="}}`
			} else {
				if req.URL.Query().Get("page_token") != "cursor +&/=" {
					t.Fatal("chat-list cursor was not preserved through query encoding")
				}
				response = `{"code":0,"data":{"items":[],"has_more":false,"page_token":"ignored-final-cursor"}}`
			}
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(response))}, nil
	}))
	first, err := sender.ListChats(context.Background(), json.RawMessage(appCredentials), "")
	if err != nil || len(first.Items) != 1 || first.Items[0] != (domain.Chat{ChatID: "oc_group", Name: "运维群"}) || !first.HasMore || first.NextPageToken == "" {
		t.Fatal("incorrect first chat page")
	}
	last, err := sender.ListChats(context.Background(), json.RawMessage(appCredentials), first.NextPageToken)
	if err != nil || last.Items == nil || len(last.Items) != 0 || last.HasMore || last.NextPageToken != "" || tokenCalls != 1 || chatCalls != 2 {
		t.Fatal("empty final chat page or token reuse is incorrect")
	}
}

func TestFeishuAppTokenCacheNormalizesCredentialsAndSeparatesVersions(t *testing.T) {
	calls := 0
	sender := newTestFeishuAppSender(roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(appTokenResponse))}, nil
	}))
	key, first, err := sender.accessToken(context.Background(), json.RawMessage(appCredentials))
	if err != nil {
		t.Fatal("first application token acquisition failed")
	}
	otherKey, reused, err := sender.accessToken(context.Background(), json.RawMessage(`{ "appSecret":"private-app-secret", "appId":"cli_test" }`))
	if err != nil || otherKey != key || reused != first || calls != 1 {
		t.Fatal("credential JSON formatting changed the token cache identity")
	}
	changedKey, _, err := sender.accessToken(context.Background(), json.RawMessage(`{"appId":"cli_test","appSecret":"new-private-secret"}`))
	if err != nil || changedKey == key || calls != 2 || len(sender.tokens) != 2 {
		t.Fatal("different credential snapshots reused the same token cache entry")
	}
	first.expiresAt = time.Now().Add(-time.Second)
	sender.tokens[key] = first
	expiredOther := sender.tokens[changedKey]
	expiredOther.expiresAt = time.Now().Add(-time.Second)
	sender.tokens[changedKey] = expiredOther
	_, refreshed, err := sender.accessToken(context.Background(), json.RawMessage(appCredentials))
	if err != nil || calls != 3 || !time.Now().Before(refreshed.expiresAt) || len(sender.tokens) != 1 {
		t.Fatal("expired tokens were not refreshed and removed during a write")
	}
	remaining := time.Until(refreshed.expiresAt)
	if remaining > 7140*time.Second || remaining < 7139*time.Second {
		t.Fatal("token expiration did not reserve the required sixty seconds")
	}
}

func TestFeishuAppConcurrentSendsKeepCacheSafe(t *testing.T) {
	var tokenCalls, messageCalls atomic.Int64
	sender := newTestFeishuAppSender(roundTripFunc(func(req *http.Request) (*http.Response, error) {
		response := appTokenResponse
		if req.URL.Host == "accounts.feishu.cn" {
			tokenCalls.Add(1)
		} else {
			messageCalls.Add(1)
			if req.Header.Get("Authorization") != "Bearer private-access-token" {
				return nil, errors.New("missing application authentication")
			}
			response = `{"code":0}`
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(response))}, nil
	}))
	var group sync.WaitGroup
	errors := make(chan error, 20)
	for range 20 {
		group.Go(func() {
			errors <- sender.Send(context.Background(), domain.TargetSnapshot{Config: json.RawMessage(`{"chatId":"oc_group"}`), Credentials: json.RawMessage(appCredentials)}, domain.Message{Message: "body"})
		})
	}
	group.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal("concurrent application send failed")
		}
	}
	if messageCalls.Load() != 20 || tokenCalls.Load() < 1 || tokenCalls.Load() > 20 || len(sender.tokens) != 1 {
		t.Fatal("concurrent sends duplicated messages or corrupted token entries")
	}
}

func TestFeishuAppPlatformFailuresAreSafeAndNeverRetried(t *testing.T) {
	for _, stage := range []string{"token", "message", "chats"} {
		for _, tc := range []struct {
			name    string
			status  int
			body    string
			network bool
			evict   bool
		}{
			{"HTTP failure", 429, `private-response-secret`, false, false},
			{"unauthorized", 401, `private-response-secret`, false, true},
			{"expired token", 200, `{"code":99991663,"msg":"private-response-secret"}`, false, true},
			{"HTTP expired token", 400, `{"code":99991663,"msg":"private-response-secret"}`, false, true},
			{"invalid token", 200, `{"code":99991665,"msg":"private-response-secret"}`, false, true},
			{"business failure", 200, `{"code":123,"msg":"private-response-secret"}`, false, false},
			{"missing business code", 200, `{}`, false, false},
			{"wrong code type", 200, `{"code":"0"}`, false, false},
			{"malformed body", 200, `private-response-secret`, false, false},
			{"trailing garbage", 200, `{"code":0} private-response-secret`, false, false},
			{"second JSON", 200, `{"code":0} {}`, false, false},
			{"network failure", 0, ``, true, false},
		} {
			t.Run(stage+"/"+tc.name, func(t *testing.T) {
				calls := 0
				var bodies []*trackedBody
				sender := newTestFeishuAppSender(roundTripFunc(func(req *http.Request) (*http.Response, error) {
					calls++
					status, response := tc.status, tc.body
					if stage != "token" && calls == 1 {
						status, response = 200, appTokenResponse
					} else if tc.network {
						return nil, errors.New("private-network-secret " + req.URL.String() + " " + req.Header.Get("Authorization"))
					}
					body := &trackedBody{Reader: strings.NewReader(response)}
					bodies = append(bodies, body)
					return &http.Response{StatusCode: status, Body: body}, nil
				}))
				var err error
				if stage == "chats" {
					_, err = sender.ListChats(context.Background(), json.RawMessage(appCredentials), "")
				} else {
					err = sender.Send(context.Background(), domain.TargetSnapshot{Config: json.RawMessage(`{"chatId":"oc_group"}`), Credentials: json.RawMessage(appCredentials)}, domain.Message{Message: "body"})
				}
				if err == nil || !errors.Is(err, domain.ErrUnavailable) || strings.Contains(err.Error(), "private") || strings.Contains(err.Error(), "https://") {
					t.Fatal("missing or unsafe platform error")
				}
				expectedCalls := 2
				if stage == "token" {
					expectedCalls = 1
				}
				if calls != expectedCalls {
					t.Fatal("application operation retried or continued after a token failure")
				}
				for _, body := range bodies {
					if !body.closed {
						t.Fatal("application operation leaked a response body")
					}
				}
				if stage != "token" && (len(sender.tokens) == 0) != tc.evict {
					t.Fatal("rejected token cache eviction is incorrect")
				}
			})
		}
	}
}

func TestFeishuAppRejectsIncompleteTokenAndChatResponses(t *testing.T) {
	for _, tc := range []struct {
		name, response string
		chats          bool
	}{
		{"empty access token", `{"code":0,"access_token":"","expires_in":7200,"token_type":"Bearer"}`, false},
		{"missing expiry", `{"code":0,"access_token":"private","token_type":"Bearer"}`, false},
		{"wrong token type", `{"code":0,"access_token":"private","expires_in":7200,"token_type":"other"}`, false},
		{"missing chat data", `{"code":0}`, true},
		{"missing chat items", `{"code":0,"data":{"has_more":false}}`, true},
		{"missing pagination state", `{"code":0,"data":{"items":[]}}`, true},
		{"missing next cursor", `{"code":0,"data":{"items":[],"has_more":true}}`, true},
		{"missing chat identity", `{"code":0,"data":{"items":[{"name":"group"}],"has_more":false}}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			sender := newTestFeishuAppSender(roundTripFunc(func(*http.Request) (*http.Response, error) {
				calls++
				response := tc.response
				if tc.chats && calls == 1 {
					response = appTokenResponse
				}
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(response))}, nil
			}))
			var err error
			if tc.chats {
				_, err = sender.ListChats(context.Background(), json.RawMessage(appCredentials), "")
			} else {
				_, _, err = sender.accessToken(context.Background(), json.RawMessage(appCredentials))
			}
			if !errors.Is(err, domain.ErrUnavailable) {
				t.Fatal("incomplete platform response was accepted")
			}
		})
	}
}

func TestFeishuAppRejectedOldTokenCannotEvictNewToken(t *testing.T) {
	key := [32]byte{1}
	old := feishuAppToken{value: "old-private-token", expiresAt: time.Now().Add(time.Hour)}
	newer := feishuAppToken{value: old.value, expiresAt: old.expiresAt.Add(time.Minute)}
	sender := NewFeishuAppSender(&config.Config{SendTimeout: time.Second})
	sender.tokens[key] = newer
	code := 99991663
	sender.discardRejectedToken(key, old, 200, &code)
	if sender.tokens[key] != newer {
		t.Fatal("a concurrent refresh was evicted by an earlier request")
	}
	sender.discardRejectedToken(key, newer, 401, nil)
	if len(sender.tokens) != 0 {
		t.Fatal("the rejected current token remained cached")
	}
}

func TestFeishuAppHTTPExpiredTokenAuthenticatesOnlyOnNextOperation(t *testing.T) {
	tokenCalls, messageCalls := 0, 0
	sender := newTestFeishuAppSender(roundTripFunc(func(req *http.Request) (*http.Response, error) {
		status, response := 200, appTokenResponse
		if req.URL.Host == "accounts.feishu.cn" {
			tokenCalls++
		} else {
			messageCalls++
			response = `{"code":0}`
			if messageCalls == 1 {
				status, response = 400, `{"code":99991665,"msg":"private-expired-token"}`
			}
		}
		return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(response))}, nil
	}))
	target := domain.TargetSnapshot{Config: json.RawMessage(`{"chatId":"oc_group"}`), Credentials: json.RawMessage(appCredentials)}
	if err := sender.Send(context.Background(), target, domain.Message{Message: "first"}); !errors.Is(err, domain.ErrUnavailable) || tokenCalls != 1 || messageCalls != 1 || len(sender.tokens) != 0 {
		t.Fatal("HTTP token rejection did not fail once and discard the cached token")
	}
	if err := sender.Send(context.Background(), target, domain.Message{Message: "next"}); err != nil || tokenCalls != 2 || messageCalls != 2 {
		t.Fatal("the next operation did not acquire a fresh application token")
	}
}

func TestFeishuAppTokenAndMessageShareRequestContext(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	calls := 0
	sender := newTestFeishuAppSender(roundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		if req.Context() != ctx {
			t.Fatal("token and message requests did not share the operation context")
		}
		if calls == 1 {
			cancel()
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(appTokenResponse))}, nil
		}
		return nil, req.Context().Err()
	}))
	err := sender.Send(ctx, domain.TargetSnapshot{Config: json.RawMessage(`{"chatId":"oc_group"}`), Credentials: json.RawMessage(appCredentials)}, domain.Message{Message: "body"})
	if !errors.Is(err, domain.ErrUnavailable) || calls != 2 {
		t.Fatal("a canceled operation escaped its context or retried")
	}
}
