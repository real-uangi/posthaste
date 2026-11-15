package posthaste

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const testMessageID = "21f719f9-3daa-46ee-a139-8dfd0b5ff3ab"

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func recordedResponse(status int, body string) *http.Response {
	recorder := httptest.NewRecorder()
	recorder.Header().Set("Content-Type", "application/json")
	recorder.WriteHeader(status)
	_, _ = recorder.WriteString(body)
	return recorder.Result()
}

func testClient(t *testing.T, transport http.RoundTripper) *Client {
	t.Helper()
	client, err := NewClient(Config{
		BaseURL: "https://notify.example.com/posthaste/",
		Token:   "test-token",
		HTTPClient: &http.Client{
			Transport: transport,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func TestNewClientRejectsInvalidConfigurationWithoutLeakingIt(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		url   string
		token string
	}{
		{name: "empty URL", url: "", token: "token"},
		{name: "relative URL", url: "/posthaste", token: "token"},
		{name: "unsupported scheme", url: "ftp://example.com", token: "token"},
		{name: "missing host", url: "https:///posthaste", token: "token"},
		{name: "empty hostname", url: "https://:443", token: "token"},
		{name: "userinfo", url: "https://private-secret@example.com", token: "token"},
		{name: "query", url: "https://example.com?token=private-secret", token: "token"},
		{name: "empty query", url: "https://example.com?", token: "token"},
		{name: "fragment", url: "https://example.com#private-secret", token: "token"},
		{name: "empty fragment", url: "https://example.com#", token: "token"},
		{name: "malformed URL", url: "https://private-secret%gh@example.com", token: "token"},
		{name: "empty token", url: "https://example.com", token: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, err := NewClient(Config{BaseURL: tt.url, Token: tt.token})
			if err == nil || client != nil {
				t.Fatal("invalid configuration was accepted")
			}
			if strings.Contains(err.Error(), "private-secret") {
				t.Fatal("configuration error leaked credentials")
			}
		})
	}
}

func TestNewClientPreservesReverseProxyPath(t *testing.T) {
	t.Parallel()
	tests := []struct{ base, endpoint string }{
		{"https://example.com", "https://example.com/api/v1/notify"},
		{"http://localhost:8080/", "http://localhost:8080/api/v1/notify"},
		{"https://example.com/posthaste", "https://example.com/posthaste/api/v1/notify"},
		{"https://example.com/posthaste/", "https://example.com/posthaste/api/v1/notify"},
		{"https://example.com/a%20b/c%2Fd/", "https://example.com/a%20b/c%2Fd/api/v1/notify"},
	}
	for _, tt := range tests {
		t.Run(tt.base, func(t *testing.T) {
			client, err := NewClient(Config{BaseURL: tt.base, Token: "token"})
			if err != nil {
				t.Fatal(err)
			}
			if got := client.endpoint.String(); got != tt.endpoint {
				t.Fatalf("endpoint = %q, want %q", got, tt.endpoint)
			}
		})
	}
}

func TestNewClientOwnsItsDefaultHTTPClient(t *testing.T) {
	t.Parallel()
	one, err := NewClient(Config{BaseURL: "https://example.com", Token: "token"})
	if err != nil {
		t.Fatal(err)
	}
	two, err := NewClient(Config{BaseURL: "https://example.com", Token: "token"})
	if err != nil {
		t.Fatal(err)
	}
	if one.httpClient == two.httpClient || one.httpClient == http.DefaultClient {
		t.Fatal("default clients must be independent")
	}
	if one.httpClient.Timeout != 10*time.Second {
		t.Fatalf("timeout = %s", one.httpClient.Timeout)
	}
	if err := one.httpClient.CheckRedirect(nil, nil); err != http.ErrUseLastResponse {
		t.Fatalf("redirect policy = %v", err)
	}
}

func TestNewClientDoesNotModifyOrUseInjectedHTTPClientDuringConstruction(t *testing.T) {
	t.Parallel()
	var calls atomic.Int64
	transport := roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		calls.Add(1)
		return nil, errors.New("unexpected request")
	})
	redirect := func(_ *http.Request, _ []*http.Request) error { return nil }
	injected := &http.Client{Timeout: 37 * time.Second, Transport: transport, CheckRedirect: redirect}
	client, err := NewClient(Config{BaseURL: "https://example.com", Token: "token", HTTPClient: injected})
	if err != nil {
		t.Fatal(err)
	}
	if client.httpClient != injected || injected.Timeout != 37*time.Second ||
		reflect.ValueOf(injected.Transport).Pointer() != reflect.ValueOf(transport).Pointer() ||
		reflect.ValueOf(injected.CheckRedirect).Pointer() != reflect.ValueOf(redirect).Pointer() {
		t.Fatal("injected HTTP client was copied or modified")
	}
	if calls.Load() != 0 {
		t.Fatal("constructing a client made a request")
	}
}

func TestSendEncodesCallerAndOmitsDefaultFields(t *testing.T) {
	t.Parallel()
	const caller = " production +/&?中文 "
	var calls int
	client := testClient(t, roundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		if req.Method != http.MethodPost || req.URL.Path != "/posthaste/api/v1/notify" ||
			req.Header.Get("Authorization") != "Bearer test-token" || req.Header.Get("Content-Type") != "application/json" {
			t.Errorf("unexpected request: %s %s", req.Method, req.URL)
		}
		var body map[string]json.RawMessage
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if _, ok := body["caller"]; ok {
			t.Error("caller was included in JSON")
		}
		if calls == 1 {
			if req.URL.Query().Get("caller") != caller || len(req.URL.Query()) != 1 {
				t.Errorf("caller was not preserved: %q", req.URL.RawQuery)
			}
			if string(body["title"]) != `"标题"` || string(body["message"]) != `"**正文**"` ||
				string(body["format"]) != `"markdown"` {
				t.Errorf("unexpected body: %v", body)
			}
		} else if req.URL.RawQuery != "" || len(body) != 1 || string(body["message"]) != `"default"` {
			t.Errorf("default request retained fields: %s, %v", req.URL.RawQuery, body)
		}
		return recordedResponse(202, `{"code":202,"data":{"messageId":"`+testMessageID+`"}}`), nil
	}))
	if _, err := client.Send(context.Background(), SendRequest{Caller: caller, Title: "标题", Message: "**正文**", Format: FormatMarkdown}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Send(context.Background(), SendRequest{Message: "default"}); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("requests = %d, want 2", calls)
	}
}

func TestSendLeavesBusinessValidationToServer(t *testing.T) {
	t.Parallel()
	var calls int
	client := testClient(t, roundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		var body struct {
			Message string `json:"message"`
			Format  string `json:"format"`
		}
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body.Message != "" || body.Format != "server-decides" {
			t.Errorf("unexpected body: %+v", body)
		}
		return recordedResponse(400, `{"code":400,"data":null}`), nil
	}))
	_, err := client.Send(context.Background(), SendRequest{Format: Format("server-decides")})
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != 400 || calls != 1 {
		t.Fatalf("server validation result = %v, requests = %d", err, calls)
	}
}

func TestSendValidatesTheCompleteResponseContract(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		status int
		body   string
		ok     bool
		api    bool
	}{
		{name: "accepted", status: 202, body: `{"code":202,"data":{"messageId":"` + testMessageID + `"}}`, ok: true},
		{name: "future fields", status: 202, body: `{"code":202,"data":{"messageId":"id","future":true},"extra":{},"message":[],"time":false}`, ok: true},
		{name: "service unavailable", status: 503, body: `{"code":503,"data":null,"message":"private-secret","time":"not-a-time"}`, api: true},
		{name: "bad request", status: 400, body: `{"code":400,"data": null }`, api: true},
		{name: "unexpected non202 status", status: 200, body: `{"code":200,"data":null}`, api: true},
		{name: "missing code", status: 202, body: `{"data":{"messageId":"id"}}`},
		{name: "null code", status: 202, body: `{"code":null,"data":{"messageId":"id"}}`},
		{name: "string code", status: 202, body: `{"code":"202","data":{"messageId":"id"}}`},
		{name: "missing data", status: 202, body: `{"code":202}`},
		{name: "null data", status: 202, body: `{"code":202,"data":null}`},
		{name: "array data", status: 202, body: `{"code":202,"data":[]}`},
		{name: "missing message ID", status: 202, body: `{"code":202,"data":{}}`},
		{name: "empty message ID", status: 202, body: `{"code":202,"data":{"messageId":""}}`},
		{name: "null message ID", status: 202, body: `{"code":202,"data":{"messageId":null}}`},
		{name: "numeric message ID", status: 202, body: `{"code":202,"data":{"messageId":42}}`},
		{name: "fake accepted HTTP200", status: 200, body: `{"code":202,"data":{"messageId":"id"}}`},
		{name: "accepted code mismatch", status: 202, body: `{"code":200,"data":{"messageId":"id"}}`},
		{name: "error code mismatch", status: 503, body: `{"code":500,"data":null}`},
		{name: "missing error data", status: 503, body: `{"code":503}`},
		{name: "object error data", status: 503, body: `{"code":503,"data":{}}`},
		{name: "HTML error", status: 502, body: `<html>private-secret</html>`},
		{name: "trailing garbage", status: 202, body: `{"code":202,"data":{"messageId":"id"}}private-secret`},
		{name: "two JSON documents", status: 202, body: `{"code":202,"data":{"messageId":"id"}} {}`},
		{name: "null envelope", status: 202, body: `null`},
		{name: "array envelope", status: 202, body: `[]`},
		{name: "empty body", status: 202, body: ``},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			calls := 0
			client := testClient(t, roundTripFunc(func(_ *http.Request) (*http.Response, error) {
				calls++
				return recordedResponse(tt.status, tt.body), nil
			}))
			accepted, err := client.Send(context.Background(), SendRequest{Message: "test"})
			if calls != 1 {
				t.Fatalf("requests = %d", calls)
			}
			if tt.ok {
				if err != nil || accepted.MessageID == "" {
					t.Fatalf("acceptance = %+v, error = %v", accepted, err)
				}
				return
			}
			if err == nil || accepted != (Acceptance{}) {
				t.Fatalf("expected zero acceptance and an error, got %+v, %v", accepted, err)
			}
			var apiErr *APIError
			if errors.As(err, &apiErr) != tt.api {
				t.Fatalf("APIError = %v, want %v: %v", apiErr, tt.api, err)
			}
			if tt.api && (apiErr.StatusCode != tt.status || apiErr.Code != tt.status) {
				t.Fatalf("unexpected APIError: %+v", apiErr)
			}
			if strings.Contains(err.Error(), "private-secret") || !strings.Contains(err.Error(), fmt.Sprint(tt.status)) {
				t.Fatalf("unsafe or missing-status error: %v", err)
			}
		})
	}
}

func TestSendDefaultClientDoesNotFollowRedirects(t *testing.T) {
	t.Parallel()
	client, err := NewClient(Config{BaseURL: "https://example.com", Token: "token"})
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	client.httpClient.Transport = roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		calls++
		response := recordedResponse(307, `{"code":307,"data":null}`)
		response.Header.Set("Location", "https://other.example.com/notify")
		return response, nil
	})
	_, err = client.Send(context.Background(), SendRequest{Message: "test"})
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != 307 || calls != 1 {
		t.Fatalf("redirect result = %v, requests = %d", err, calls)
	}
}

func TestSendPreservesInjectedClientRedirectPolicy(t *testing.T) {
	t.Parallel()
	calls := 0
	client := testClient(t, roundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		if calls == 1 {
			response := recordedResponse(307, "")
			response.Header.Set("Location", "/forwarded")
			return response, nil
		}
		if req.URL.Path != "/forwarded" {
			t.Errorf("redirect path = %q", req.URL.Path)
		}
		return recordedResponse(202, `{"code":202,"data":{"messageId":"id"}}`), nil
	}))
	accepted, err := client.Send(context.Background(), SendRequest{Message: "test"})
	if err != nil || accepted.MessageID != "id" || calls != 2 {
		t.Fatalf("redirect result = %+v, %v, requests = %d", accepted, err, calls)
	}
}

type trackedBody struct {
	io.Reader
	closed bool
}

func (b *trackedBody) Close() error {
	b.closed = true
	return nil
}

type errorReader struct{ err error }

func (r errorReader) Read([]byte) (int, error) { return 0, r.err }

func TestSendClosesResponseBodyOnEveryParsingPath(t *testing.T) {
	t.Parallel()
	readErr := errors.New("response connection interrupted")
	tests := []struct {
		name   string
		status int
		reader io.Reader
	}{
		{"accepted", 202, strings.NewReader(`{"code":202,"data":{"messageId":"id"}}`)},
		{"API error", 401, strings.NewReader(`{"code":401,"data":null}`)},
		{"protocol error", 202, strings.NewReader(`not-json`)},
		{"read failure", 502, errorReader{err: readErr}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := &trackedBody{Reader: tt.reader}
			client := testClient(t, roundTripFunc(func(_ *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: tt.status, Body: body, Header: make(http.Header)}, nil
			}))
			_, err := client.Send(context.Background(), SendRequest{Message: "test"})
			if !body.closed {
				t.Fatal("response body was not closed")
			}
			if tt.name == "read failure" && !errors.Is(err, readErr) {
				t.Fatalf("read error chain was lost: %v", err)
			}
		})
	}
}

func TestSendPreservesTransportAndContextErrorsWithoutRetry(t *testing.T) {
	t.Parallel()
	networkErr := errors.New("connection interrupted")
	for _, expected := range []error{networkErr, context.Canceled, context.DeadlineExceeded} {
		t.Run(expected.Error(), func(t *testing.T) {
			calls := 0
			ctx := context.Background()
			if expected == context.Canceled {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			if expected == context.DeadlineExceeded {
				var cancel context.CancelFunc
				ctx, cancel = context.WithDeadline(ctx, time.Unix(0, 0))
				defer cancel()
			}
			client := testClient(t, roundTripFunc(func(req *http.Request) (*http.Response, error) {
				calls++
				if expected == networkErr {
					return nil, networkErr
				}
				<-req.Context().Done()
				return nil, req.Context().Err()
			}))
			accepted, err := client.Send(ctx, SendRequest{Message: "test"})
			if !errors.Is(err, expected) || accepted != (Acceptance{}) || calls != 1 {
				t.Fatalf("result = %+v, %v, requests = %d", accepted, err, calls)
			}
		})
	}
}

func TestClientSupportsConcurrentMessagesWithoutSharedRequestState(t *testing.T) {
	t.Parallel()
	var calls atomic.Int64
	client := testClient(t, roundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls.Add(1)
		var payload struct {
			Message string `json:"message"`
		}
		if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
			return nil, err
		}
		if req.URL.Query().Get("caller") != payload.Message {
			return nil, errors.New("request caller and message were mixed")
		}
		return recordedResponse(202, fmt.Sprintf(`{"code":202,"data":{"messageId":%q}}`, payload.Message)), nil
	}))
	const count = 64
	errorsCh := make(chan error, count)
	var workers sync.WaitGroup
	for i := range count {
		workers.Go(func() {
			id := fmt.Sprintf("message-%d", i)
			accepted, err := client.Send(context.Background(), SendRequest{Caller: id, Message: id})
			if err != nil {
				errorsCh <- err
			} else if accepted.MessageID != id {
				errorsCh <- fmt.Errorf("acceptance = %q, want %q", accepted.MessageID, id)
			}
		})
	}
	workers.Wait()
	close(errorsCh)
	for err := range errorsCh {
		t.Error(err)
	}
	if calls.Load() != count {
		t.Fatalf("requests = %d, want %d", calls.Load(), count)
	}
}
