// Package posthaste sends messages through the Posthaste HTTP v1 API.
// A successful Send reports in-memory acceptance, not delivery to a platform.
package posthaste

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Config configures a Client. An injected HTTPClient is used without modification.
type Config struct {
	BaseURL    string
	Token      string
	HTTPClient *http.Client
}

// Format selects the server's message format. The empty value defaults to text.
type Format string

const (
	FormatText     Format = "text"
	FormatMarkdown Format = "markdown"
)

// SendRequest submits a message to the target group identified by Caller.
// The server routes an empty or unknown Caller to its default group.
type SendRequest struct {
	Caller  string
	Title   string
	Message string
	Format  Format
}

// Acceptance identifies a message accepted into the server's in-memory queue.
// It does not identify a queryable delivery record or guarantee delivery.
type Acceptance struct {
	MessageID string
}

// APIError reports a valid non-202 error envelope without exposing its message.
// Use errors.As with *APIError to inspect StatusCode and Code.
type APIError struct {
	StatusCode int
	Code       int
}

func (e *APIError) Error() string {
	return fmt.Sprintf("posthaste: HTTP %d (code %d)", e.StatusCode, e.Code)
}

// Client is safe for concurrent use. Callers must not modify its injected
// HTTPClient or Transport while requests are running.
type Client struct {
	endpoint   url.URL
	token      string
	httpClient *http.Client
}

// NewClient validates configuration without making network requests.
// BaseURL may contain a reverse-proxy path prefix, but no credentials, query,
// or fragment. The default HTTP client has a ten-second timeout and no redirects.
func NewClient(cfg Config) (*Client, error) {
	base, err := url.Parse(cfg.BaseURL)
	if err != nil || base == nil || (base.Scheme != "http" && base.Scheme != "https") ||
		base.Hostname() == "" || base.User != nil || base.RawQuery != "" ||
		base.ForceQuery || strings.Contains(cfg.BaseURL, "#") {
		return nil, errors.New("posthaste: BaseURL must be an absolute HTTP(S) URL without userinfo, query, or fragment")
	}
	if cfg.Token == "" {
		return nil, errors.New("posthaste: Token must not be empty")
	}

	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{
			Timeout: 10 * time.Second,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return http.ErrUseLastResponse
			},
		}
	}

	return &Client{
		endpoint:   *base.JoinPath("api/v1/notify"),
		token:      cfg.Token,
		httpClient: httpClient,
	}, nil
}

// Send makes one HTTP request without retries. Only a valid HTTP 202 acceptance
// is successful. Cancellation and transport errors preserve their error chains;
// retrying after a timeout may submit the same message again.
func (c *Client) Send(ctx context.Context, req SendRequest) (Acceptance, error) {
	payload := struct {
		Title   string `json:"title,omitempty"`
		Message string `json:"message"`
		Format  Format `json:"format,omitempty"`
	}{Title: req.Title, Message: req.Message, Format: req.Format}
	body, err := json.Marshal(payload)
	if err != nil {
		return Acceptance{}, fmt.Errorf("posthaste: encode request: %w", err)
	}

	// Each call owns its URL and query; concurrent sends cannot change another
	// call's caller or leak a previous caller into a default-group request.
	endpoint := c.endpoint
	if req.Caller != "" {
		query := make(url.Values)
		query.Set("caller", req.Caller)
		endpoint.RawQuery = query.Encode()
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), bytes.NewReader(body))
	if err != nil {
		return Acceptance{}, fmt.Errorf("posthaste: create request: %w", err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+c.token)
	httpReq.Header.Set("Content-Type", "application/json")

	response, err := c.httpClient.Do(httpReq)
	if err != nil {
		return Acceptance{}, fmt.Errorf("posthaste: send request: %w", err)
	}
	defer response.Body.Close()

	responseBody, err := io.ReadAll(response.Body)
	if err != nil {
		return Acceptance{}, fmt.Errorf("posthaste: read response (HTTP %d): %w", response.StatusCode, err)
	}
	var envelope struct {
		Code *int            `json:"code"`
		Data json.RawMessage `json:"data"`
	}
	// Unmarshal consumes the whole body, rejecting trailing JSON or garbage.
	// Unused message/time metadata and future fields do not affect acceptance.
	if err := json.Unmarshal(responseBody, &envelope); err != nil || envelope.Code == nil ||
		*envelope.Code != response.StatusCode || len(envelope.Data) == 0 {
		return Acceptance{}, fmt.Errorf("posthaste: invalid response (HTTP %d)", response.StatusCode)
	}
	if response.StatusCode != http.StatusAccepted {
		if !bytes.Equal(bytes.TrimSpace(envelope.Data), []byte("null")) {
			return Acceptance{}, fmt.Errorf("posthaste: invalid response (HTTP %d)", response.StatusCode)
		}
		return Acceptance{}, &APIError{StatusCode: response.StatusCode, Code: *envelope.Code}
	}
	var accepted struct {
		MessageID string `json:"messageId"`
	}
	if err := json.Unmarshal(envelope.Data, &accepted); err != nil || accepted.MessageID == "" {
		return Acceptance{}, fmt.Errorf("posthaste: invalid response (HTTP %d)", response.StatusCode)
	}
	return Acceptance{MessageID: accepted.MessageID}, nil
}
