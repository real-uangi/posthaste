package infra

import (
	"context"
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

func TestBarkValidatesOfficialAndSelfHostedAccounts(t *testing.T) {
	sender := NewBarkSender(&config.Config{SendTimeout: time.Second})
	if sender.Platform() != "bark" || sender.client.Timeout != time.Second || sender.client.CheckRedirect(nil, nil) != http.ErrUseLastResponse {
		t.Fatal("Bark registration or HTTP policy is incorrect")
	}
	for _, tc := range []struct {
		name, credentials string
		valid             bool
	}{
		{"official", `{"serverUrl":"https://api.day.app","deviceKey":"private-device-key"}`, true},
		{"self hosted", `{"serverUrl":"http://192.168.1.2:8080/bark/","deviceKey":"private-device-key","basicAuth":{"username":"user","password":"private-password"}}`, true},
		{"missing server", `{"deviceKey":"private-device-key"}`, false},
		{"missing key", `{"serverUrl":"https://api.day.app"}`, false},
		{"blank key", `{"serverUrl":"https://api.day.app","deviceKey":" "}`, false},
		{"unsupported scheme", `{"serverUrl":"ftp://bark.example","deviceKey":"private-device-key"}`, false},
		{"relative address", `{"serverUrl":"/bark","deviceKey":"private-device-key"}`, false},
		{"no hostname", `{"serverUrl":"https://:443","deviceKey":"private-device-key"}`, false},
		{"invalid port", `{"serverUrl":"https://bark.example:bad","deviceKey":"private-device-key"}`, false},
		{"userinfo", `{"serverUrl":"https://user:private-password@bark.example","deviceKey":"private-device-key"}`, false},
		{"query", `{"serverUrl":"https://bark.example?key=private-device-key","deviceKey":"private-device-key"}`, false},
		{"empty query", `{"serverUrl":"https://bark.example?","deviceKey":"private-device-key"}`, false},
		{"fragment", `{"serverUrl":"https://bark.example#private","deviceKey":"private-device-key"}`, false},
		{"empty fragment", `{"serverUrl":"https://bark.example#","deviceKey":"private-device-key"}`, false},
		{"partial auth", `{"serverUrl":"https://bark.example","deviceKey":"private-device-key","basicAuth":{"username":"user"}}`, false},
		{"empty auth", `{"serverUrl":"https://bark.example","deviceKey":"private-device-key","basicAuth":{}}`, false},
		{"username delimiter", `{"serverUrl":"https://bark.example","deviceKey":"private-device-key","basicAuth":{"username":"user:other","password":"private-password"}}`, false},
		{"unknown field", `{"serverUrl":"https://api.day.app","deviceKey":"private-device-key","token":"private-token"}`, false},
		{"null", `null`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := sender.ValidateAccount(json.RawMessage(tc.credentials)); (err == nil) != tc.valid {
				t.Fatal("unexpected account validation result")
			}
		})
	}
}

func TestBarkTargetConfigContainsOnlyNotificationOptions(t *testing.T) {
	sender := &BarkSender{}
	for _, tc := range []struct {
		name, config string
		valid        bool
	}{
		{"empty", `{}`, true},
		{"notification options", `{"group":"ops","level":"critical","sound":"minuet","call":true,"url":"https://monitor.example/alert?id=1"}`, true},
		{"false call", `{"call":false}`, true},
		{"app URL", `{"url":"myapp://alerts/one"}`, true},
		{"mail URL", `{"url":"mailto:ops@example.com"}`, true},
		{"invalid level", `{"level":"urgent"}`, false},
		{"wrong call type", `{"call":"1"}`, false},
		{"relative link", `{"url":"/alerts/one"}`, false},
		{"invalid web link", `{"url":"https:alert"}`, false},
		{"link userinfo", `{"url":"https://user:private-password@monitor.example"}`, false},
		{"credentials", `{"deviceKey":"private-device-key"}`, false},
		{"unsupported option", `{"image":"https://example.com/image.png"}`, false},
		{"null", `null`, false},
		{"array", `[]`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := sender.ValidateTarget(json.RawMessage(tc.config)); (err == nil) != tc.valid {
				t.Fatal("unexpected target validation result")
			}
		})
	}
	for _, level := range []string{"active", "passive", "timeSensitive", "critical"} {
		if err := sender.ValidateTarget(json.RawMessage(`{"level":"` + level + `"}`)); err != nil {
			t.Fatal("documented notification level rejected")
		}
	}
}

func TestBarkSendsTextOrMarkdownToOfficialAndSelfHostedServices(t *testing.T) {
	for _, tc := range []struct {
		name, endpoint, format, credentials, config string
		want                                        map[string]any
	}{
		{
			name: "official text", endpoint: "https://api.day.app/push", format: "text",
			credentials: `{"serverUrl":"https://api.day.app","deviceKey":"private-device-key"}`, config: `{}`,
			want: map[string]any{"device_key": "private-device-key", "title": "*plain title*", "body": "**body**"},
		},
		{
			name: "self hosted markdown with prefix and auth", endpoint: "http://192.168.1.2:8080/bark/push", format: "markdown",
			credentials: `{"serverUrl":"http://192.168.1.2:8080/bark/","deviceKey":"private-device-key","basicAuth":{"username":"user","password":"private-password"}}`,
			config:      `{"group":"ops","level":"critical","sound":"minuet","call":true,"url":"myapp://alerts/one"}`,
			want:        map[string]any{"device_key": "private-device-key", "title": "*plain title*", "markdown": "**body**", "group": "ops", "level": "critical", "sound": "minuet", "call": "1", "url": "myapp://alerts/one"},
		},
		{
			name: "custom escaped prefix and explicit false", endpoint: "https://bark.example/team%20alerts/push", format: "text",
			credentials: `{"serverUrl":"https://bark.example/team%20alerts","deviceKey":"private-device-key"}`, config: `{"call":false}`,
			want: map[string]any{"device_key": "private-device-key", "title": "*plain title*", "body": "**body**", "call": "0"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			body := &trackedBody{Reader: strings.NewReader(`{"code":200,"message":"success"}`)}
			sender := NewBarkSender(&config.Config{SendTimeout: time.Second})
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			sender.client.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
				calls++
				if req.Method != http.MethodPost || req.URL.String() != tc.endpoint || req.Header.Get("Content-Type") != "application/json" || req.Context() != ctx {
					t.Fatal("unexpected Bark request or context")
				}
				if strings.Contains(req.URL.String(), "private-") || req.URL.RawQuery != "" {
					t.Fatal("Bark credentials entered the URL")
				}
				if strings.Contains(tc.credentials, "basicAuth") {
					username, password, ok := req.BasicAuth()
					if !ok || username != "user" || password != "private-password" {
						t.Fatal("Bark Basic authentication not preserved")
					}
				} else if req.Header.Get("Authorization") != "" {
					t.Fatal("unconfigured Basic authentication was added")
				}
				var payload map[string]any
				if err := json.NewDecoder(req.Body).Decode(&payload); err != nil || len(payload) != len(tc.want) {
					t.Fatal("incorrect Bark JSON payload shape")
				}
				for key, expected := range tc.want {
					if payload[key] != expected {
						t.Fatalf("Bark payload field %s was not preserved", key)
					}
				}
				return &http.Response{StatusCode: http.StatusOK, Body: body}, nil
			})
			err := sender.Send(ctx, domain.TargetSnapshot{Credentials: json.RawMessage(tc.credentials), Config: json.RawMessage(tc.config)}, domain.Message{Title: "*plain title*", Message: "**body**", Format: tc.format})
			if err != nil || calls != 1 || !body.closed {
				t.Fatal("Bark send failed, retried, or leaked its response body")
			}
		})
	}
}

func TestBarkEmptyTitleAndUnsetOptionsRemainAbsent(t *testing.T) {
	sender := NewBarkSender(&config.Config{SendTimeout: time.Second})
	sender.client.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		var payload map[string]any
		if err := json.NewDecoder(req.Body).Decode(&payload); err != nil || len(payload) != 2 || payload["body"] != "message" {
			t.Fatal("Bark synthesized unconfigured options")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"code":200}`))}, nil
	})
	if err := sender.Send(context.Background(), domain.TargetSnapshot{Credentials: json.RawMessage(`{"serverUrl":"https://api.day.app","deviceKey":"private-device-key"}`), Config: json.RawMessage(`{}`)}, domain.Message{Message: "message"}); err != nil {
		t.Fatal("minimal Bark message failed")
	}
}

func TestBarkFailuresAreSafeAndNeverRetried(t *testing.T) {
	for _, tc := range []struct {
		name, response string
		status         int
		network        error
	}{
		{name: "HTTP failure", status: 503, response: "private-response-secret"},
		{name: "redirect", status: 302, response: `{"code":200}`},
		{name: "business failure", status: 200, response: `{"code":400,"message":"private-device-key"}`},
		{name: "wrong success code", status: 200, response: `{"code":0}`},
		{name: "missing code", status: 200, response: `{}`},
		{name: "wrong code type", status: 200, response: `{"code":"200"}`},
		{name: "malformed response", status: 200, response: "private-response-secret"},
		{name: "trailing garbage", status: 200, response: `{"code":200} private-response-secret`},
		{name: "second JSON", status: 200, response: `{"code":200} {}`},
		{name: "network failure", network: errors.New("private-network-secret")},
		{name: "cancellation", network: context.Canceled},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			body := &trackedBody{Reader: strings.NewReader(tc.response)}
			sender := NewBarkSender(&config.Config{SendTimeout: time.Second})
			sender.client.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
				calls++
				if tc.network != nil {
					return nil, errors.New(tc.network.Error() + " " + req.URL.String() + " " + req.Header.Get("Authorization"))
				}
				return &http.Response{StatusCode: tc.status, Body: body, Header: http.Header{"Location": {"https://redirect.example/private-device-key"}}}, nil
			})
			err := sender.Send(context.Background(), domain.TargetSnapshot{Credentials: json.RawMessage(`{"serverUrl":"https://bark.example","deviceKey":"private-device-key","basicAuth":{"username":"user","password":"private-password"}}`), Config: json.RawMessage(`{}`)}, domain.Message{Message: "body"})
			if !errors.Is(err, domain.ErrUnavailable) || strings.Contains(err.Error(), "private-") || strings.Contains(err.Error(), "https://") {
				t.Fatal("missing or unsafe Bark error")
			}
			if calls != 1 || (tc.network == nil && !body.closed) {
				t.Fatal("Bark failure retried or leaked response body")
			}
		})
	}
}
