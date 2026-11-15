package infra

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/real-uangi/posthaste/internal/notification/domain"
	"github.com/real-uangi/posthaste/internal/shared/config"
)

type FeishuAppSender struct {
	client *http.Client
	mu     sync.Mutex
	tokens map[[32]byte]feishuAppToken
}

type feishuAppCredentials struct {
	AppID     string `json:"appId"`
	AppSecret string `json:"appSecret"`
}

type feishuAppConfig struct {
	ChatID string `json:"chatId"`
}

type feishuAppToken struct {
	value     string
	expiresAt time.Time
}

func NewFeishuAppSender(cfg *config.Config) *FeishuAppSender {
	return &FeishuAppSender{client: platformClient(cfg.SendTimeout), tokens: make(map[[32]byte]feishuAppToken)}
}

func (*FeishuAppSender) Platform() string { return "feishu_app" }

func (*FeishuAppSender) ValidateAccount(credentials json.RawMessage) error {
	var secret feishuAppCredentials
	if err := decodePlatformJSON(credentials, &secret); err != nil || strings.TrimSpace(secret.AppID) == "" || strings.TrimSpace(secret.AppSecret) == "" {
		return domain.ErrInvalid
	}
	return nil
}

func (*FeishuAppSender) ValidateTarget(config json.RawMessage) error {
	var public feishuAppConfig
	if err := decodePlatformJSON(config, &public); err != nil || strings.TrimSpace(public.ChatID) == "" {
		return domain.ErrInvalid
	}
	return nil
}

func (s *FeishuAppSender) Send(ctx context.Context, target domain.TargetSnapshot, message domain.Message) error {
	var public feishuAppConfig
	if err := json.Unmarshal(target.Config, &public); err != nil {
		return domain.ErrUnavailable
	}
	key, token, err := s.accessToken(ctx, target.Credentials)
	if err != nil {
		return err
	}
	messageType, content := feishuMessage(message)
	encoded, err := json.Marshal(content)
	if err != nil {
		return domain.ErrUnavailable
	}
	payload := map[string]string{"receive_id": public.ChatID, "msg_type": messageType, "content": string(encoded)}
	var response struct {
		Code *int `json:"code"`
	}
	status, err := platformJSON(ctx, s.client, http.MethodPost, "https://open.feishu.cn/open-apis/im/v1/messages?receive_id_type=chat_id", "Bearer "+token.value, payload, &response)
	s.discardRejectedToken(key, token, status, response.Code)
	if err != nil {
		return err
	}
	if response.Code == nil || *response.Code != 0 {
		return fmt.Errorf("%w: platform_business_error", domain.ErrUnavailable)
	}
	return nil
}

func (s *FeishuAppSender) ListChats(ctx context.Context, credentials json.RawMessage, pageToken string) (domain.ChatPage, error) {
	key, token, err := s.accessToken(ctx, credentials)
	if err != nil {
		return domain.ChatPage{}, err
	}
	query := url.Values{"page_size": {"100"}, "sort_type": {"ByCreateTimeAsc"}}
	if pageToken != "" {
		query.Set("page_token", pageToken)
	}
	var response struct {
		Code *int `json:"code"`
		Data *struct {
			Items []struct {
				ChatID string `json:"chat_id"`
				Name   string `json:"name"`
			} `json:"items"`
			HasMore   *bool  `json:"has_more"`
			PageToken string `json:"page_token"`
		} `json:"data"`
	}
	status, err := platformJSON(ctx, s.client, http.MethodGet, "https://open.feishu.cn/open-apis/im/v1/chats?"+query.Encode(), "Bearer "+token.value, nil, &response)
	s.discardRejectedToken(key, token, status, response.Code)
	if err != nil {
		return domain.ChatPage{}, err
	}
	if response.Code == nil || *response.Code != 0 {
		return domain.ChatPage{}, fmt.Errorf("%w: platform_business_error", domain.ErrUnavailable)
	}
	if response.Data == nil || response.Data.Items == nil || response.Data.HasMore == nil || (*response.Data.HasMore && response.Data.PageToken == "") {
		return domain.ChatPage{}, fmt.Errorf("%w: platform_response_invalid", domain.ErrUnavailable)
	}
	page := domain.ChatPage{Items: make([]domain.Chat, 0, len(response.Data.Items)), HasMore: *response.Data.HasMore, NextPageToken: response.Data.PageToken}
	if !page.HasMore {
		page.NextPageToken = ""
	}
	for _, item := range response.Data.Items {
		if item.ChatID == "" {
			return domain.ChatPage{}, fmt.Errorf("%w: platform_response_invalid", domain.ErrUnavailable)
		}
		page.Items = append(page.Items, domain.Chat{ChatID: item.ChatID, Name: item.Name})
	}
	return page, nil
}

func (s *FeishuAppSender) accessToken(ctx context.Context, credentials json.RawMessage) ([32]byte, feishuAppToken, error) {
	var secret feishuAppCredentials
	if err := json.Unmarshal(credentials, &secret); err != nil {
		return [32]byte{}, feishuAppToken{}, domain.ErrUnavailable
	}
	canonical, _ := json.Marshal(secret)
	// Include the complete credential version, so pending snapshots cannot reuse
	// a token obtained with a different App Secret after an account update.
	key := sha256.Sum256(canonical)
	s.mu.Lock()
	token, exists := s.tokens[key]
	s.mu.Unlock()
	if exists && time.Now().Before(token.expiresAt) {
		return key, token, nil
	}
	var response struct {
		Code        *int   `json:"code"`
		AccessToken string `json:"access_token"`
		ExpiresIn   int64  `json:"expires_in"`
		TokenType   string `json:"token_type"`
	}
	payload := map[string]string{"grant_type": "client_credentials", "client_id": secret.AppID, "client_secret": secret.AppSecret}
	if err := postJSON(ctx, s.client, "https://accounts.feishu.cn/oauth/v3/token", payload, &response); err != nil {
		return key, feishuAppToken{}, err
	}
	if response.Code == nil || *response.Code != 0 {
		return key, feishuAppToken{}, fmt.Errorf("%w: platform_business_error", domain.ErrUnavailable)
	}
	if strings.TrimSpace(response.AccessToken) == "" || response.ExpiresIn <= 0 || !strings.EqualFold(response.TokenType, "Bearer") {
		return key, feishuAppToken{}, fmt.Errorf("%w: platform_response_invalid", domain.ErrUnavailable)
	}
	now := time.Now()
	token = feishuAppToken{value: response.AccessToken, expiresAt: now.Add(time.Duration(response.ExpiresIn)*time.Second - time.Minute)}
	s.mu.Lock()
	defer s.mu.Unlock()
	// Expired entries are removed during normal writes; no background refresh or
	// cleanup task is needed for this single-process credential cache.
	for cachedKey, cachedToken := range s.tokens {
		if !now.Before(cachedToken.expiresAt) {
			delete(s.tokens, cachedKey)
		}
	}
	if now.Before(token.expiresAt) {
		s.tokens[key] = token
	}
	return key, token, nil
}

func (s *FeishuAppSender) discardRejectedToken(key [32]byte, token feishuAppToken, status int, code *int) {
	if status != http.StatusUnauthorized && (code == nil || (*code != 99991663 && *code != 99991665)) {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// Another request may already have refreshed this credential version. Only
	// evict the token used by this failed attempt, without retrying its message.
	if current, exists := s.tokens[key]; exists && current == token {
		delete(s.tokens, key)
	}
}

var _ domain.Sender = (*FeishuAppSender)(nil)
var _ domain.ChatLister = (*FeishuAppSender)(nil)
