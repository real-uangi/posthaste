package application

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/real-uangi/posthaste/internal/notification/domain"
)

type storeStub struct {
	routes       []domain.Route
	account      domain.Account
	credentials  json.RawMessage
	target       domain.TargetSnapshot
	savedGroup   domain.SaveGroup
	saved        domain.SaveTarget
	savedAccount domain.SaveAccount
	err          error
}

func (s *storeStub) Account(context.Context, string) (domain.Account, error) { return s.account, s.err }
func (s *storeStub) AccountSnapshot(context.Context, string) (domain.AccountSnapshot, error) {
	return domain.AccountSnapshot{Account: s.account, Credentials: s.credentials}, s.err
}
func (s *storeStub) ListAccounts(context.Context) ([]domain.Account, error) { return nil, s.err }
func (s *storeStub) SaveAccount(_ context.Context, input domain.SaveAccount) (domain.Account, error) {
	s.savedAccount = input
	return domain.Account{}, s.err
}
func (s *storeStub) DeleteAccount(context.Context, string) error { return s.err }

func (s *storeStub) Routes(context.Context, string) ([]domain.Route, error) { return s.routes, s.err }
func (s *storeStub) Target(context.Context, string) (domain.TargetSnapshot, error) {
	return s.target, s.err
}
func (s *storeStub) ListTargets(context.Context) ([]domain.Target, error) { return nil, s.err }
func (s *storeStub) SaveTarget(_ context.Context, input domain.SaveTarget) (domain.Target, error) {
	s.saved = input
	return domain.Target{}, s.err
}
func (s *storeStub) DeleteTarget(context.Context, string) error         { return s.err }
func (s *storeStub) ListGroups(context.Context) ([]domain.Group, error) { return nil, s.err }
func (s *storeStub) SaveGroup(_ context.Context, input domain.SaveGroup) (domain.Group, error) {
	s.savedGroup = input
	return domain.Group{}, s.err
}
func (s *storeStub) DeleteGroup(context.Context, string) error { return s.err }

type publisherStub struct {
	envelopes []domain.Envelope
	err       error
}

func (p *publisherStub) Publish(envelope domain.Envelope) error {
	p.envelopes = append(p.envelopes, envelope)
	return p.err
}

type senderStub struct {
	platform        string
	validateAccount func(json.RawMessage) error
	validateTarget  func(json.RawMessage) error
	send            func(context.Context, domain.TargetSnapshot, domain.Message) error
}

func (s *senderStub) Platform() string { return s.platform }
func (s *senderStub) ValidateAccount(secret json.RawMessage) error {
	if s.validateAccount != nil {
		return s.validateAccount(secret)
	}
	return nil
}
func (s *senderStub) ValidateTarget(cfg json.RawMessage) error {
	if s.validateTarget != nil {
		return s.validateTarget(cfg)
	}
	return nil
}
func (s *senderStub) Send(ctx context.Context, target domain.TargetSnapshot, msg domain.Message) error {
	return s.send(ctx, target, msg)
}

type chatListerFunc func(context.Context, json.RawMessage, string) (domain.ChatPage, error)

func (f chatListerFunc) ListChats(ctx context.Context, credentials json.RawMessage, pageToken string) (domain.ChatPage, error) {
	return f(ctx, credentials, pageToken)
}

func TestAccountChatsUseStoredCredentialsAndAllowDisabledAccounts(t *testing.T) {
	secret := json.RawMessage(`{"appId":"app","appSecret":"private-secret"}`)
	store := &storeStub{account: domain.Account{ID: uuid.NewString(), Platform: "feishu_app", Enabled: false}, credentials: secret}
	called := false
	lister := chatListerFunc(func(ctx context.Context, credentials json.RawMessage, pageToken string) (domain.ChatPage, error) {
		called = true
		if string(credentials) != string(secret) || pageToken != "cursor+/=?" {
			t.Fatal("stored credentials or opaque cursor lost")
		}
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > time.Second {
			t.Fatal("chat request has no bounded context")
		}
		return domain.ChatPage{Items: []domain.Chat{{ChatID: "oc_one", Name: "group"}}, HasMore: true, NextPageToken: "next"}, nil
	})
	service, _ := NewService(store, &publisherStub{}, nil, lister, time.Second)
	page, err := service.ListAccountChats(context.Background(), store.account.ID, "cursor+/=?")
	if err != nil || !called || len(page.Items) != 1 || !page.HasMore || page.NextPageToken != "next" {
		t.Fatalf("page=%+v err=%v called=%t", page, err, called)
	}
}

func TestAccountChatsRejectInvalidAccountsAndPropagateFailures(t *testing.T) {
	for _, tc := range []struct {
		name, id, platform      string
		storeErr, listErr, want error
	}{
		{name: "invalid id", id: "bad", want: domain.ErrInvalid},
		{name: "missing account", id: uuid.NewString(), storeErr: domain.ErrNotFound, want: domain.ErrNotFound},
		{name: "storage failure", id: uuid.NewString(), storeErr: domain.ErrUnavailable, want: domain.ErrUnavailable},
		{name: "wrong platform", id: uuid.NewString(), platform: "feishu", want: domain.ErrInvalid},
		{name: "platform failure", id: uuid.NewString(), platform: "feishu_app", listErr: domain.ErrUnavailable, want: domain.ErrUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			called := false
			lister := chatListerFunc(func(context.Context, json.RawMessage, string) (domain.ChatPage, error) {
				called = true
				return domain.ChatPage{}, tc.listErr
			})
			service, _ := NewService(&storeStub{account: domain.Account{Platform: tc.platform}, err: tc.storeErr}, &publisherStub{}, nil, lister, time.Second)
			_, err := service.ListAccountChats(context.Background(), tc.id, "")
			if !errors.Is(err, tc.want) || called != (tc.listErr != nil) {
				t.Fatalf("err=%v called=%t", err, called)
			}
		})
	}
}

func TestCallerSelectionAndEmptyMatchedGroup(t *testing.T) {
	defaultTarget := domain.TargetSnapshot{ID: uuid.NewString(), Platform: "telegram", Enabled: true}
	customTarget := domain.TargetSnapshot{ID: uuid.NewString(), Platform: "feishu", Enabled: true}
	for _, tc := range []struct {
		name     string
		caller   string
		routes   []domain.Route
		wantID   string
		fallback bool
		wantErr  error
	}{
		{"missing uses default", "", []domain.Route{{Caller: "default", Targets: []domain.TargetSnapshot{defaultTarget}}}, defaultTarget.ID, true, nil},
		{"unknown uses default", "unknown", []domain.Route{{Caller: "default", Targets: []domain.TargetSnapshot{defaultTarget}}}, defaultTarget.ID, true, nil},
		{"known uses own targets", "beszel", []domain.Route{{Caller: "beszel", Targets: []domain.TargetSnapshot{customTarget}}, {Caller: "default", Targets: []domain.TargetSnapshot{defaultTarget}}}, customTarget.ID, false, nil},
		{"default is explicit", "default", []domain.Route{{Caller: "default", Targets: []domain.TargetSnapshot{defaultTarget}}}, defaultTarget.ID, false, nil},
		{"empty matched does not fall back", "beszel", []domain.Route{{Caller: "beszel"}, {Caller: "default", Targets: []domain.TargetSnapshot{defaultTarget}}}, "", false, domain.ErrUnavailable},
		{"empty default unavailable", "unknown", []domain.Route{{Caller: "default"}}, "", false, domain.ErrUnavailable},
		{"case and whitespace preserved", " Beszel ", []domain.Route{{Caller: "default", Targets: []domain.TargetSnapshot{defaultTarget}}}, defaultTarget.ID, true, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			publisher := &publisherStub{}
			service, err := NewService(&storeStub{routes: tc.routes}, publisher, nil, nil, time.Second)
			if err != nil {
				t.Fatal(err)
			}
			accepted, err := service.Submit(context.Background(), tc.caller, domain.Message{Message: "body"})
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("error = %v, want %v", err, tc.wantErr)
			}
			if tc.wantErr != nil {
				if len(publisher.envelopes) != 0 || accepted.MessageID != "" {
					t.Fatal("rejected message was published")
				}
				return
			}
			if _, err := uuid.Parse(accepted.MessageID); err != nil {
				t.Fatal("invalid message ID")
			}
			if len(publisher.envelopes) != 1 {
				t.Fatalf("published %d times", len(publisher.envelopes))
			}
			envelope := publisher.envelopes[0]
			if envelope.Targets[0].ID != tc.wantID || envelope.Fallback != tc.fallback || envelope.CallerRequested != tc.caller || envelope.Message.Format != "text" {
				t.Fatalf("unexpected envelope: message=%s route=%s", envelope.MessageID, envelope.CallerResolved)
			}
		})
	}
}

func TestAcceptedEnvelopeOwnsConfigAndCredentialBuffers(t *testing.T) {
	target := domain.TargetSnapshot{ID: uuid.NewString(), AccountID: uuid.NewString(), Config: json.RawMessage(`{"chatId":"one"}`), Credentials: json.RawMessage(`{"botToken":"old"}`)}
	other := target
	other.ID = uuid.NewString()
	store := &storeStub{routes: []domain.Route{{Caller: "default", Targets: []domain.TargetSnapshot{target, other}}}}
	publisher := &publisherStub{}
	service, _ := NewService(store, publisher, nil, nil, time.Second)
	if _, err := service.Submit(context.Background(), "", domain.Message{Message: "body"}); err != nil {
		t.Fatal(err)
	}
	copy(target.Config, strings.Repeat("x", len(target.Config)))
	copy(target.Credentials, strings.Repeat("x", len(target.Credentials)))
	store.routes[0].Targets[0].ID = "changed"
	if publisher.envelopes[0].Targets[0].ID != target.ID {
		t.Fatal("accepted exit identity changed with the store")
	}
	for _, accepted := range publisher.envelopes[0].Targets {
		if string(accepted.Config) != `{"chatId":"one"}` || string(accepted.Credentials) != `{"botToken":"old"}` || accepted.AccountID != target.AccountID {
			t.Fatal("accepted snapshot changed with the account or store")
		}
	}
	publisher.envelopes[0].Targets[0].Credentials[0] = 'x'
	if publisher.envelopes[0].Targets[1].Credentials[0] != '{' {
		t.Fatal("exits share mutable account credential buffers")
	}
}

func TestAccountCreationRequiresCredentialsAndUpdateOmissionSkipsValidation(t *testing.T) {
	validated := 0
	sender := &senderStub{platform: "telegram", validateAccount: func(raw json.RawMessage) error {
		validated++
		if string(raw) != `{"botToken":"123:new"}` {
			return domain.ErrInvalid
		}
		return nil
	}}
	store := &storeStub{}
	service, _ := NewService(store, &publisherStub{}, []domain.Sender{sender}, nil, time.Second)
	if _, err := service.SaveAccount(context.Background(), domain.SaveAccount{Name: "bot", Platform: "telegram"}); !errors.Is(err, domain.ErrInvalid) {
		t.Fatal("created account without credentials")
	}
	if _, err := service.SaveAccount(context.Background(), domain.SaveAccount{Name: "bot", Platform: "telegram", Enabled: true, Credentials: json.RawMessage(`{"botToken":"123:new"}`)}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.SaveAccount(context.Background(), domain.SaveAccount{ID: uuid.NewString(), Name: "renamed", Platform: "telegram", Enabled: false}); err != nil {
		t.Fatal(err)
	}
	if validated != 1 || store.savedAccount.Credentials != nil || store.savedAccount.Enabled {
		t.Fatal("metadata update revalidated or changed credentials")
	}
}

func TestTargetRebindingValidatesTheSelectedAccountPlatform(t *testing.T) {
	accountID := uuid.NewString()
	store := &storeStub{account: domain.Account{ID: accountID, Platform: "telegram", Enabled: false}}
	var validatedConfig string
	telegram := &senderStub{platform: "telegram", validateAccount: func(json.RawMessage) error { t.Fatal("destination save revalidated credentials"); return nil }, validateTarget: func(raw json.RawMessage) error {
		validatedConfig = string(raw)
		if validatedConfig != `{"chatId":"-2"}` {
			return domain.ErrInvalid
		}
		return nil
	}}
	feishu := &senderStub{platform: "feishu", validateTarget: func(json.RawMessage) error { t.Fatal("wrong account platform used"); return nil }}
	service, _ := NewService(store, &publisherStub{}, []domain.Sender{telegram, feishu}, nil, time.Second)
	input := domain.SaveTarget{ID: uuid.NewString(), AccountID: accountID, Name: "exit", Enabled: false, Config: json.RawMessage(`{"chatId":"-2"}`)}
	if _, err := service.SaveTarget(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	if store.saved.AccountID != accountID || store.saved.Enabled || validatedConfig == "" {
		t.Fatal("rebind or disabled flag lost")
	}
	input.Config = json.RawMessage(`{}`)
	if _, err := service.SaveTarget(context.Background(), input); !errors.Is(err, domain.ErrInvalid) {
		t.Fatal("invalid destination config accepted")
	}
	store.err = domain.ErrNotFound
	if _, err := service.SaveTarget(context.Background(), input); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("missing account was ignored")
	}
}

func TestPublishFailureDoesNotReturnAcceptance(t *testing.T) {
	publisher := &publisherStub{err: errors.New("private queue error")}
	service, _ := NewService(&storeStub{routes: []domain.Route{{Caller: "default", Targets: []domain.TargetSnapshot{{ID: uuid.NewString()}}}}}, publisher, nil, nil, time.Second)
	accepted, err := service.Submit(context.Background(), "", domain.Message{Message: "body"})
	if !errors.Is(err, domain.ErrUnavailable) || accepted.MessageID != "" || len(publisher.envelopes) != 1 {
		t.Fatalf("accepted=%v, err=%v", accepted, err)
	}
}

func TestDeliverContinuesAfterFailureWithIndependentTimeouts(t *testing.T) {
	var attempted []string
	sender := &senderStub{platform: "fake", send: func(ctx context.Context, target domain.TargetSnapshot, msg domain.Message) error {
		attempted = append(attempted, target.ID)
		if ctx.Err() != nil {
			t.Fatal("received already canceled context")
		}
		if _, ok := ctx.Deadline(); !ok {
			t.Fatal("missing per-target deadline")
		}
		if target.ID == "first" {
			return domain.ErrUnavailable
		}
		return nil
	}}
	service, _ := NewService(&storeStub{}, &publisherStub{}, []domain.Sender{sender}, nil, time.Second)
	err := service.Deliver(domain.Envelope{MessageID: uuid.NewString(), Targets: []domain.TargetSnapshot{{ID: "first", Platform: "fake"}, {ID: "second", Platform: "fake"}}})
	if !errors.Is(err, domain.ErrUnavailable) || len(attempted) != 2 || attempted[1] != "second" {
		t.Fatalf("attempts=%v err=%v", attempted, err)
	}
}

func TestDisabledTestTargetIsNotPublished(t *testing.T) {
	id := uuid.NewString()
	publisher := &publisherStub{}
	service, _ := NewService(&storeStub{target: domain.TargetSnapshot{ID: id, Enabled: false}}, publisher, nil, nil, time.Second)
	_, err := service.Test(context.Background(), id, domain.Message{Message: "test"})
	if !errors.Is(err, domain.ErrUnavailable) || len(publisher.envelopes) != 0 {
		t.Fatal("disabled target entered queue")
	}
}

func TestSavePreservesFalseOmittedCredentialsAndEmptyMembers(t *testing.T) {
	store := &storeStub{}
	sender := &senderStub{platform: "fake"}
	service, _ := NewService(store, &publisherStub{}, []domain.Sender{sender}, nil, time.Second)
	_, err := service.SaveAccount(context.Background(), domain.SaveAccount{ID: uuid.NewString(), Name: "account", Platform: "fake", Enabled: false})
	if err != nil || store.savedAccount.Enabled || store.savedAccount.Credentials != nil {
		t.Fatalf("save changed omitted credentials/false: %v", err)
	}
	id := uuid.NewString()
	_, err = service.SaveGroup(context.Background(), domain.SaveGroup{Name: "group", Caller: " Beszel ", TargetIDs: []string{id, strings.ToUpper(id)}})
	if err != nil || len(store.savedGroup.TargetIDs) != 1 || store.savedGroup.Caller != " Beszel " {
		t.Fatalf("dedup or caller changed: %v", err)
	}
	_, err = service.SaveGroup(context.Background(), domain.SaveGroup{ID: uuid.NewString(), Name: "group", Caller: "beszel", TargetIDs: []string{}})
	if err != nil || store.savedGroup.TargetIDs == nil || len(store.savedGroup.TargetIDs) != 0 {
		t.Fatal("empty group was lost")
	}
}

func TestInvalidMessageAndDuplicatePlatform(t *testing.T) {
	service, _ := NewService(&storeStub{}, &publisherStub{}, nil, nil, time.Second)
	for _, msg := range []domain.Message{{Message: " "}, {Message: "body", Format: "html"}} {
		if _, err := service.Submit(context.Background(), "", msg); !errors.Is(err, domain.ErrInvalid) {
			t.Fatalf("invalid message accepted: %v", err)
		}
	}
	_, err := NewService(&storeStub{}, &publisherStub{}, []domain.Sender{&senderStub{platform: "same"}, &senderStub{platform: "same"}}, nil, time.Second)
	if err == nil {
		t.Fatal("duplicate platform did not fail construction")
	}
}
