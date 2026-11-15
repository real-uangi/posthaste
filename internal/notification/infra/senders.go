package infra

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/real-uangi/posthaste/internal/notification/domain"
	"github.com/real-uangi/posthaste/internal/shared/config"
)

type FeishuSender struct {
	client *http.Client
}

func NewFeishuSender(cfg *config.Config) *FeishuSender {
	return &FeishuSender{client: platformClient(cfg.SendTimeout)}
}

func (*FeishuSender) Platform() string { return "feishu" }

type feishuCredentials struct {
	WebhookURL    string `json:"webhookUrl"`
	SigningSecret string `json:"signingSecret,omitempty"`
}

func (*FeishuSender) ValidateTarget(cfg json.RawMessage) error {
	var public map[string]json.RawMessage
	if err := json.Unmarshal(cfg, &public); err != nil || public == nil || len(public) != 0 {
		return domain.ErrInvalid
	}
	return nil
}

func (*FeishuSender) ValidateAccount(credentials json.RawMessage) error {
	var secret feishuCredentials
	if err := decodePlatformJSON(credentials, &secret); err != nil {
		return domain.ErrInvalid
	}
	u, err := url.Parse(secret.WebhookURL)
	if err != nil || u.Scheme != "https" || u.Host != "open.feishu.cn" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return domain.ErrInvalid
	}
	const prefix = "/open-apis/bot/v2/hook/"
	if !strings.HasPrefix(u.Path, prefix) || !webhookID.MatchString(strings.TrimPrefix(u.Path, prefix)) {
		return domain.ErrInvalid
	}
	return nil
}

var webhookID = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

func (s *FeishuSender) Send(ctx context.Context, target domain.TargetSnapshot, msg domain.Message) error {
	var secret feishuCredentials
	if err := json.Unmarshal(target.Credentials, &secret); err != nil {
		return domain.ErrUnavailable
	}
	messageType, content := feishuMessage(msg)
	payload := map[string]any{"msg_type": messageType}
	if messageType == "interactive" {
		payload["card"] = content
	} else {
		payload["content"] = content
	}
	if secret.SigningSecret != "" {
		timestamp := strconv.FormatInt(time.Now().Unix(), 10)
		// Feishu uses timestamp + newline + secret as the HMAC key, signing
		// an empty message. Using the webhook body as the input is incorrect.
		h := hmac.New(sha256.New, []byte(timestamp+"\n"+secret.SigningSecret))
		payload["timestamp"], payload["sign"] = timestamp, base64.StdEncoding.EncodeToString(h.Sum(nil))
	}
	var response struct {
		Code *int `json:"code"`
	}
	if err := postJSON(ctx, s.client, secret.WebhookURL, payload, &response); err != nil {
		return err
	}
	if response.Code == nil || *response.Code != 0 {
		return fmt.Errorf("%w: platform_business_error", domain.ErrUnavailable)
	}
	return nil
}

type TelegramSender struct {
	client *http.Client
}

func NewTelegramSender(cfg *config.Config) *TelegramSender {
	return &TelegramSender{client: platformClient(cfg.SendTimeout)}
}

func (*TelegramSender) Platform() string { return "telegram" }

type telegramConfig struct {
	ChatID          string `json:"chatId"`
	MessageThreadID *int64 `json:"messageThreadId,omitempty"`
}

type telegramCredentials struct {
	BotToken string `json:"botToken"`
}

var botToken = regexp.MustCompile(`^[0-9]+:[A-Za-z0-9_-]+$`)

func (*TelegramSender) ValidateTarget(cfg json.RawMessage) error {
	var public telegramConfig
	if err := decodePlatformJSON(cfg, &public); err != nil || strings.TrimSpace(public.ChatID) == "" || (public.MessageThreadID != nil && *public.MessageThreadID <= 0) {
		return domain.ErrInvalid
	}
	return nil
}

func (*TelegramSender) ValidateAccount(credentials json.RawMessage) error {
	var secret telegramCredentials
	if err := decodePlatformJSON(credentials, &secret); err != nil || !botToken.MatchString(secret.BotToken) {
		return domain.ErrInvalid
	}
	return nil
}

func (s *TelegramSender) Send(ctx context.Context, target domain.TargetSnapshot, msg domain.Message) error {
	var public telegramConfig
	var secret telegramCredentials
	if err := json.Unmarshal(target.Config, &public); err != nil {
		return domain.ErrUnavailable
	}
	if err := json.Unmarshal(target.Credentials, &secret); err != nil {
		return domain.ErrUnavailable
	}
	payload := map[string]any{"chat_id": public.ChatID}
	if public.MessageThreadID != nil {
		payload["message_thread_id"] = *public.MessageThreadID
	}
	method := "sendMessage"
	if msg.Format == "markdown" {
		method = "sendRichMessage"
		markdown := msg.Message
		if msg.Title != "" {
			markdown = markdownEscaper.Replace(msg.Title) + "\n\n" + markdown
		}
		payload["rich_message"] = map[string]string{"markdown": markdown}
	} else {
		payload["text"] = plainMessage(msg)
	}
	var response struct {
		OK *bool `json:"ok"`
	}
	if err := postJSON(ctx, s.client, "https://api.telegram.org/bot"+secret.BotToken+"/"+method, payload, &response); err != nil {
		return err
	}
	if response.OK == nil || !*response.OK {
		return fmt.Errorf("%w: platform_business_error", domain.ErrUnavailable)
	}
	return nil
}

var markdownEscaper = strings.NewReplacer(
	// CommonMark backslash escapes cover every ASCII punctuation character.
	// This also prevents Rich Message extensions ($math$, ==marked==) and
	// HTML entities from turning the plain-text title into formatted content.
	"!", `\!`, `"`, `\"`, "#", `\#`, "$", `\$`, "%", `\%`, "&", `\&`, "'", `\'`,
	"(", `\(`, ")", `\)`, "*", `\*`, "+", `\+`, ",", `\,`, "-", `\-`, ".", `\.`, "/", `\/`,
	":", `\:`, ";", `\;`, "<", `\<`, "=", `\=`, ">", `\>`, "?", `\?`, "@", `\@`,
	"[", `\[`, `\`, `\\`, "]", `\]`, "^", `\^`, "_", `\_`, "`", "\\`", "{", `\{`, "|", `\|`, "}", `\}`, "~", `\~`,
)

func plainMessage(msg domain.Message) string {
	if msg.Title == "" {
		return msg.Message
	}
	return msg.Title + "\n\n" + msg.Message
}

func feishuMessage(msg domain.Message) (string, any) {
	if msg.Format != "markdown" {
		return "text", map[string]string{"text": plainMessage(msg)}
	}
	card := map[string]any{
		"schema": "2.0",
		"body":   map[string]any{"elements": []any{map[string]any{"tag": "markdown", "content": msg.Message}}},
	}
	if msg.Title != "" {
		card["header"] = map[string]any{"title": map[string]any{"tag": "plain_text", "content": msg.Title}}
	}
	return "interactive", card
}

func platformClient(timeout time.Duration) *http.Client {
	return &http.Client{Timeout: timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

func decodePlatformJSON(raw json.RawMessage, value any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	return decoder.Decode(value)
}

func postJSON(ctx context.Context, client *http.Client, endpoint string, payload, response any) error {
	_, err := platformJSON(ctx, client, http.MethodPost, endpoint, "", payload, response)
	return err
}

func platformJSON(ctx context.Context, client *http.Client, method, endpoint, bearer string, payload, response any) (int, error) {
	var body io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return 0, domain.ErrUnavailable
		}
		body = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return 0, domain.ErrUnavailable
	}
	req.Header.Set("Content-Type", "application/json")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	res, err := client.Do(req)
	if err != nil {
		// net/http errors include the full request URL, including bot tokens.
		// Do not wrap that error or let it reach the bus's automatic error log.
		return 0, fmt.Errorf("%w: platform_network_error", domain.ErrUnavailable)
	}
	defer res.Body.Close()
	decoder := json.NewDecoder(res.Body)
	validJSON := decoder.Decode(response) == nil
	if validJSON {
		validJSON = decoder.Decode(&struct{}{}) == io.EOF
	}
	// Decode business codes even on HTTP errors so an application sender can
	// discard an explicitly rejected token. The HTTP status still means failure.
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return res.StatusCode, fmt.Errorf("%w: platform_http_status_%d", domain.ErrUnavailable, res.StatusCode)
	}
	if !validJSON {
		return res.StatusCode, fmt.Errorf("%w: platform_response_invalid", domain.ErrUnavailable)
	}
	return res.StatusCode, nil
}

var _ domain.Sender = (*FeishuSender)(nil)
var _ domain.Sender = (*TelegramSender)(nil)
