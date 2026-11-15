package infra

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/real-uangi/posthaste/internal/notification/domain"
	"github.com/real-uangi/posthaste/internal/shared/config"
)

type BarkSender struct {
	client *http.Client
}

type barkCredentials struct {
	ServerURL string         `json:"serverUrl"`
	DeviceKey string         `json:"deviceKey"`
	BasicAuth *barkBasicAuth `json:"basicAuth,omitempty"`
}

type barkBasicAuth struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type barkConfig struct {
	Group string `json:"group,omitempty"`
	Level string `json:"level,omitempty"`
	Sound string `json:"sound,omitempty"`
	Call  *bool  `json:"call,omitempty"`
	URL   string `json:"url,omitempty"`
}

func NewBarkSender(cfg *config.Config) *BarkSender {
	return &BarkSender{client: platformClient(cfg.SendTimeout)}
}

func (*BarkSender) Platform() string { return "bark" }

func (*BarkSender) ValidateAccount(credentials json.RawMessage) error {
	var secret barkCredentials
	if err := decodePlatformJSON(credentials, &secret); err != nil || strings.TrimSpace(secret.DeviceKey) == "" {
		return domain.ErrInvalid
	}
	u, err := url.Parse(secret.ServerURL)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || strings.Contains(secret.ServerURL, "#") {
		return domain.ErrInvalid
	}
	if secret.BasicAuth != nil && (strings.TrimSpace(secret.BasicAuth.Username) == "" || strings.TrimSpace(secret.BasicAuth.Password) == "" || strings.Contains(secret.BasicAuth.Username, ":")) {
		return domain.ErrInvalid
	}
	return nil
}

func (*BarkSender) ValidateTarget(config json.RawMessage) error {
	var public *barkConfig
	if err := decodePlatformJSON(config, &public); err != nil || public == nil {
		return domain.ErrInvalid
	}
	switch public.Level {
	case "", "active", "passive", "timeSensitive", "critical":
	default:
		return domain.ErrInvalid
	}
	if public.URL != "" {
		u, err := url.Parse(public.URL)
		if err != nil || !u.IsAbs() || u.User != nil || ((u.Scheme == "http" || u.Scheme == "https") && u.Hostname() == "") {
			return domain.ErrInvalid
		}
	}
	return nil
}

func (s *BarkSender) Send(ctx context.Context, target domain.TargetSnapshot, message domain.Message) error {
	var secret barkCredentials
	var public barkConfig
	if err := json.Unmarshal(target.Credentials, &secret); err != nil {
		return domain.ErrUnavailable
	}
	if err := json.Unmarshal(target.Config, &public); err != nil {
		return domain.ErrUnavailable
	}
	endpoint, err := url.Parse(secret.ServerURL)
	if err != nil {
		return domain.ErrUnavailable
	}
	payload := map[string]any{"device_key": secret.DeviceKey}
	if message.Title != "" {
		payload["title"] = message.Title
	}
	if message.Format == "markdown" {
		payload["markdown"] = message.Message
	} else {
		payload["body"] = message.Message
	}
	if public.Group != "" {
		payload["group"] = public.Group
	}
	if public.Level != "" {
		payload["level"] = public.Level
	}
	if public.Sound != "" {
		payload["sound"] = public.Sound
	}
	if public.Call != nil {
		payload["call"] = "0"
		if *public.Call {
			payload["call"] = "1"
		}
	}
	if public.URL != "" {
		payload["url"] = public.URL
	}
	var authorization string
	if secret.BasicAuth != nil {
		authorization = "Basic " + base64.StdEncoding.EncodeToString([]byte(secret.BasicAuth.Username+":"+secret.BasicAuth.Password))
	}
	var response struct {
		Code *int `json:"code"`
	}
	// A device key authorizes sending to that device. Keep it in the JSON body,
	// never in a URL path/query where request and reverse-proxy logs can expose it.
	_, err = platformJSON(ctx, s.client, http.MethodPost, endpoint.JoinPath("push").String(), authorization, payload, &response)
	if err != nil {
		return err
	}
	if response.Code == nil || *response.Code != http.StatusOK {
		return fmt.Errorf("%w: platform_business_error", domain.ErrUnavailable)
	}
	return nil
}

var _ domain.Sender = (*BarkSender)(nil)
