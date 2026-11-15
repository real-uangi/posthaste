package domain

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

const Topic = "notification.dispatch.requested.v1"

var (
	ErrInvalid     = errors.New("invalid_notification_input")
	ErrNotFound    = errors.New("notification_resource_not_found")
	ErrConflict    = errors.New("notification_configuration_conflict")
	ErrUnavailable = errors.New("notification_unavailable")
)

type Message struct {
	Title   string `json:"title,omitempty"`
	Message string `json:"message"`
	Format  string `json:"format,omitempty"`
}

type Acceptance struct {
	MessageID string `json:"messageId"`
}

type Account struct {
	ID                    string `json:"id"`
	Name                  string `json:"name"`
	Platform              string `json:"platform"`
	Enabled               bool   `json:"enabled"`
	CredentialsConfigured bool   `json:"credentialsConfigured"`
}

type SaveAccount struct {
	ID          string          `json:"id,omitempty"`
	Name        string          `json:"name"`
	Platform    string          `json:"platform"`
	Enabled     bool            `json:"enabled"`
	Credentials json.RawMessage `json:"credentials,omitempty"`
}

type Target struct {
	ID        string          `json:"id"`
	AccountID string          `json:"accountId"`
	Name      string          `json:"name"`
	Enabled   bool            `json:"enabled"`
	Config    json.RawMessage `json:"config"`
}

type SaveTarget struct {
	ID        string          `json:"id,omitempty"`
	AccountID string          `json:"accountId"`
	Name      string          `json:"name"`
	Enabled   bool            `json:"enabled"`
	Config    json.RawMessage `json:"config"`
}

type Group struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Caller    string   `json:"caller"`
	TargetIDs []string `json:"targetIds"`
}

type SaveGroup struct {
	ID        string   `json:"id,omitempty"`
	Name      string   `json:"name"`
	Caller    string   `json:"caller"`
	TargetIDs []string `json:"targetIds"`
}

// TargetSnapshot captures an exit and its account at acceptance. Enabled is the
// conjunction of their switches. Credentials must never be serialized into
// public responses or logged together with the envelope.
type TargetSnapshot struct {
	ID          string          `json:"id"`
	AccountID   string          `json:"accountId"`
	Name        string          `json:"name"`
	Platform    string          `json:"platform"`
	Enabled     bool            `json:"enabled"`
	Config      json.RawMessage `json:"-"`
	Credentials json.RawMessage `json:"-"`
}

type Route struct {
	Caller  string
	Targets []TargetSnapshot
}

type Envelope struct {
	MessageID       string
	CallerRequested string
	CallerResolved  string
	Fallback        bool
	AcceptedAt      time.Time
	Message         Message
	Targets         []TargetSnapshot
}

type Store interface {
	Account(context.Context, string) (Account, error)
	ListAccounts(context.Context) ([]Account, error)
	SaveAccount(context.Context, SaveAccount) (Account, error)
	DeleteAccount(context.Context, string) error
	// Routes reads the requested and default groups in one consistent view.
	// The application, rather than the persistence adapter, chooses the route.
	Routes(context.Context, string) ([]Route, error)
	Target(context.Context, string) (TargetSnapshot, error)
	ListTargets(context.Context) ([]Target, error)
	SaveTarget(context.Context, SaveTarget) (Target, error)
	DeleteTarget(context.Context, string) error
	ListGroups(context.Context) ([]Group, error)
	SaveGroup(context.Context, SaveGroup) (Group, error)
	DeleteGroup(context.Context, string) error
}

type Publisher interface {
	Publish(Envelope) error
}

type Sender interface {
	Platform() string
	ValidateAccount(credentials json.RawMessage) error
	ValidateTarget(config json.RawMessage) error
	Send(context.Context, TargetSnapshot, Message) error
}
