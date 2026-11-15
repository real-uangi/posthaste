package httpadapter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	authapp "github.com/real-uangi/posthaste/internal/auth/application"
	authinfra "github.com/real-uangi/posthaste/internal/auth/infra"
	notificationapp "github.com/real-uangi/posthaste/internal/notification/application"
	"github.com/real-uangi/posthaste/internal/notification/domain"
	"github.com/real-uangi/posthaste/internal/shared/config"
)

const (
	routeAccountID = "51cb0902-f77b-4c87-8063-8be4955090cc"
	routeTargetID  = "a6f0e5f9-4eb8-4791-b2b3-91555592cd54"
)

type protocolStore struct {
	domain.Store
	routes           []domain.Route
	routeErr         error
	requested        []string
	accounts         map[string]domain.Account
	savedAccounts    []domain.SaveAccount
	deletedAccounts  []string
	deleteAccountErr error
	targets          []domain.Target
	snapshot         domain.TargetSnapshot
	savedTargets     []domain.SaveTarget
	saveTargetErr    error
	savedGroups      []domain.SaveGroup
}

func (s *protocolStore) Routes(_ context.Context, caller string) ([]domain.Route, error) {
	s.requested = append(s.requested, caller)
	return s.routes, s.routeErr
}

func (s *protocolStore) ListTargets(context.Context) ([]domain.Target, error) {
	return s.targets, nil
}

func (s *protocolStore) Account(_ context.Context, id string) (domain.Account, error) {
	account, exists := s.accounts[id]
	if !exists {
		return domain.Account{}, domain.ErrNotFound
	}
	return account, nil
}

func (s *protocolStore) ListAccounts(context.Context) ([]domain.Account, error) {
	accounts := make([]domain.Account, 0, len(s.accounts))
	for _, account := range s.accounts {
		accounts = append(accounts, account)
	}
	return accounts, nil
}

func (s *protocolStore) SaveAccount(_ context.Context, input domain.SaveAccount) (domain.Account, error) {
	s.savedAccounts = append(s.savedAccounts, input)
	id := input.ID
	if id == "" {
		id = routeAccountID
	}
	account := domain.Account{ID: id, Name: input.Name, Platform: input.Platform,
		Enabled: input.Enabled, CredentialsConfigured: true}
	s.accounts[id] = account
	return account, nil
}

func (s *protocolStore) DeleteAccount(_ context.Context, id string) error {
	s.deletedAccounts = append(s.deletedAccounts, id)
	if s.deleteAccountErr != nil {
		return s.deleteAccountErr
	}
	delete(s.accounts, id)
	return nil
}

func (s *protocolStore) Target(context.Context, string) (domain.TargetSnapshot, error) {
	return s.snapshot, nil
}

func (s *protocolStore) SaveTarget(_ context.Context, input domain.SaveTarget) (domain.Target, error) {
	s.savedTargets = append(s.savedTargets, input)
	if s.saveTargetErr != nil {
		return domain.Target{}, s.saveTargetErr
	}
	return domain.Target{ID: routeTargetID, AccountID: input.AccountID, Name: input.Name,
		Enabled: input.Enabled, Config: input.Config}, nil
}

func (s *protocolStore) SaveGroup(_ context.Context, input domain.SaveGroup) (domain.Group, error) {
	s.savedGroups = append(s.savedGroups, input)
	return domain.Group{ID: "805d2f43-b874-4ccf-90ba-b6415071e9e5", Name: input.Name,
		Caller: input.Caller, TargetIDs: input.TargetIDs}, nil
}

type protocolPublisher struct {
	envelopes []domain.Envelope
	err       error
}

func (p *protocolPublisher) Publish(envelope domain.Envelope) error {
	p.envelopes = append(p.envelopes, envelope)
	return p.err
}

type protocolSender struct {
	credentials []json.RawMessage
	configs     []json.RawMessage
}

func (*protocolSender) Platform() string { return "feishu" }
func (s *protocolSender) ValidateAccount(credentials json.RawMessage) error {
	s.credentials = append(s.credentials, credentials)
	return nil
}
func (s *protocolSender) ValidateTarget(cfg json.RawMessage) error {
	s.configs = append(s.configs, cfg)
	return nil
}
func (*protocolSender) Send(context.Context, domain.TargetSnapshot, domain.Message) error {
	return errors.New("protocol test must not send to platforms")
}

type protocolHarness struct {
	engine    *gin.Engine
	store     *protocolStore
	publisher *protocolPublisher
	sender    *protocolSender
}

func newProtocolHarness(t *testing.T) protocolHarness {
	t.Helper()
	gin.SetMode(gin.TestMode)
	cfg := &config.Config{Origin: "https://posthaste.test", SecureCookies: true,
		AdminPassword: "admin-password", IngressToken: "ingress-token"}
	store := &protocolStore{routes: []domain.Route{{Caller: "default", Targets: []domain.TargetSnapshot{{
		ID: routeTargetID, AccountID: routeAccountID, Platform: "feishu", Enabled: true, Config: json.RawMessage(`{}`),
		Credentials: json.RawMessage(`{"webhookUrl":"https://private.example/private-secret"}`),
	}}}}}
	store.accounts = map[string]domain.Account{routeAccountID: {
		ID: routeAccountID, Name: "account", Platform: "feishu", Enabled: true, CredentialsConfigured: true,
	}}
	store.snapshot = store.routes[0].Targets[0]
	publisher := &protocolPublisher{}
	sender := &protocolSender{}
	notifications, err := notificationapp.NewService(store, publisher, []domain.Sender{sender}, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	auth := authapp.NewService(authinfra.NewVerifier(cfg), authinfra.NewSessions())
	engine := gin.New()
	assets := fstest.MapFS{
		"index.html":    &fstest.MapFile{Data: []byte("<!doctype html><main>Posthaste test frontend</main>")},
		"assets/app.js": &fstest.MapFile{Data: []byte("console.log('posthaste')")},
	}
	if err := Register(engine, cfg, auth, notifications, assets); err != nil {
		t.Fatal(err)
	}
	return protocolHarness{engine: engine, store: store, publisher: publisher, sender: sender}
}

func protocolRequest(engine *gin.Engine, method, target, body string, headers map[string]string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for name, value := range headers {
		req.Header.Set(name, value)
	}
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, req)
	return recorder
}

type protocolEnvelope struct {
	Code    int             `json:"code"`
	Data    json.RawMessage `json:"data"`
	Message string          `json:"message"`
	Time    time.Time       `json:"time"`
}

func requireProtocolEnvelope(t *testing.T, response *httptest.ResponseRecorder, status int) protocolEnvelope {
	t.Helper()
	if response.Code != status {
		t.Fatalf("HTTP status = %d, want %d; body = %s", response.Code, status, response.Body.String())
	}
	if !strings.HasPrefix(response.Header().Get("Content-Type"), "application/json") {
		t.Fatalf("Content-Type = %q", response.Header().Get("Content-Type"))
	}
	var envelope protocolEnvelope
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Code != status || envelope.Time.IsZero() || envelope.Message == "" {
		t.Fatalf("invalid response envelope: %+v", envelope)
	}
	if status >= 400 && string(envelope.Data) != "null" {
		t.Fatalf("error data = %s, want null", envelope.Data)
	}
	return envelope
}

func protocolLogin(t *testing.T, h protocolHarness) *http.Cookie {
	t.Helper()
	response := protocolRequest(h.engine, http.MethodPost, "/api/v1/admin/auth/login",
		`{"password":"admin-password"}`, map[string]string{"Origin": "https://posthaste.test"})
	requireProtocolEnvelope(t, response, http.StatusOK)
	for _, cookie := range response.Result().Cookies() {
		if cookie.Name == cookieName {
			return cookie
		}
	}
	t.Fatal("login did not set a session cookie")
	return nil
}

func TestNotifyReturnsReal202AndRoutesCallerFromQuery(t *testing.T) {
	tests := []struct {
		name, query, body, resolved string
		fallback                    bool
	}{
		{"default", "", `{"message":"hello","caller":"configured","ignored":true}`, "default", true},
		{"unknown caller", "?caller=unknown", `{"message":"hello"}`, "default", true},
		{"known caller", "?caller=configured", `{"title":"title","message":"**hello**","format":"markdown"}`, "configured", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newProtocolHarness(t)
			h.store.routes = append(h.store.routes, domain.Route{Caller: "configured", Targets: h.store.routes[0].Targets})
			response := protocolRequest(h.engine, http.MethodPost, "/api/v1/notify"+tt.query,
				tt.body, map[string]string{"Authorization": "Bearer ingress-token"})
			envelope := requireProtocolEnvelope(t, response, http.StatusAccepted)
			var accepted domain.Acceptance
			if err := json.Unmarshal(envelope.Data, &accepted); err != nil {
				t.Fatal(err)
			}
			if _, err := uuid.Parse(accepted.MessageID); err != nil {
				t.Fatalf("messageId is not a UUID: %q", accepted.MessageID)
			}
			if len(h.publisher.envelopes) != 1 {
				t.Fatalf("published messages = %d", len(h.publisher.envelopes))
			}
			published := h.publisher.envelopes[0]
			if published.MessageID != accepted.MessageID || published.CallerResolved != tt.resolved || published.Fallback != tt.fallback {
				t.Fatalf("incorrect routing snapshot: %+v", published)
			}
			if tt.query == "" && (h.store.requested[0] != "" || published.Message.Format != "text") {
				t.Fatal("JSON caller affected routing or default format was not text")
			}
			if strings.Contains(response.Body.String(), "private-secret") || strings.Contains(response.Body.String(), "webhookUrl") {
				t.Fatal("acceptance response exposed target credentials")
			}
		})
	}
}

func TestNotifyRequiresIngressBearerRatherThanAdminSession(t *testing.T) {
	h := newProtocolHarness(t)
	cookie := protocolLogin(t, h)
	for _, authorization := range []string{"", "Bearer admin-password", "Basic ingress-token", "Bearer wrong-token"} {
		response := protocolRequest(h.engine, http.MethodPost, "/api/v1/notify?token=ingress-token",
			`{"message":"hello"}`, map[string]string{"Authorization": authorization}, cookie)
		requireProtocolEnvelope(t, response, http.StatusUnauthorized)
	}
	if len(h.publisher.envelopes) != 0 {
		t.Fatal("unauthorized requests reached the publisher")
	}
	response := protocolRequest(h.engine, http.MethodGet, "/api/v1/admin/auth/session", "",
		map[string]string{"Authorization": "Bearer ingress-token"})
	requireProtocolEnvelope(t, response, http.StatusUnauthorized)
}

func TestNotifyInputBoundaries(t *testing.T) {
	tests := []struct {
		name, media, body string
		status            int
	}{
		{"JSON with charset", "application/json; charset=utf-8", `{"message":"hello"}`, 202},
		{"wrong media type", "text/plain", `{"message":"hello"}`, 415},
		{"missing media type", "", `{"message":"hello"}`, 415},
		{"malformed JSON", "application/json", `{"message":"private-secret"`, 400},
		{"extra JSON", "application/json", `{"message":"hello"} {}`, 400},
		{"trailing garbage", "application/json", `{"message":"hello"} private-secret`, 400},
		{"empty body", "application/json", "", 400},
		{"null body", "application/json", "null", 400},
		{"empty message", "application/json", `{"message":""}`, 400},
		{"unsupported format", "application/json", `{"message":"hello","format":"html"}`, 400},
		{"oversized JSON", "application/json", `{"message":"` + strings.Repeat("x", 64<<10) + `"}`, 413},
		{"oversized trailing whitespace", "application/json", `{"message":"hello"}` + strings.Repeat(" ", 64<<10), 413},
		{"exact size", "application/json", `{"message":"hello"}` + strings.Repeat(" ", (64<<10)-len(`{"message":"hello"}`)), 202},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newProtocolHarness(t)
			response := protocolRequest(h.engine, http.MethodPost, "/api/v1/notify", tt.body,
				map[string]string{"Authorization": "Bearer ingress-token", "Content-Type": tt.media})
			requireProtocolEnvelope(t, response, tt.status)
			if strings.Contains(response.Body.String(), "private-secret") {
				t.Fatal("binding error exposed request contents")
			}
			wantPublished := 0
			if tt.status == http.StatusAccepted {
				wantPublished = 1
			}
			if len(h.publisher.envelopes) != wantPublished {
				t.Fatalf("published messages = %d, want %d", len(h.publisher.envelopes), wantPublished)
			}
		})
	}
}

func TestNotifyKnownEmptyGroupAndQueueFailureAreUnavailable(t *testing.T) {
	t.Run("known empty group does not fall back", func(t *testing.T) {
		h := newProtocolHarness(t)
		h.store.routes = append(h.store.routes, domain.Route{Caller: "empty", Targets: []domain.TargetSnapshot{}})
		response := protocolRequest(h.engine, http.MethodPost, "/api/v1/notify?caller=empty",
			`{"message":"hello"}`, map[string]string{"Authorization": "Bearer ingress-token"})
		requireProtocolEnvelope(t, response, http.StatusServiceUnavailable)
		if len(h.publisher.envelopes) != 0 {
			t.Fatal("empty configured group was submitted to another group")
		}
	})
	t.Run("failed queue admission", func(t *testing.T) {
		h := newProtocolHarness(t)
		h.publisher.err = errors.New("private-secret queue error")
		response := protocolRequest(h.engine, http.MethodPost, "/api/v1/notify",
			`{"message":"hello"}`, map[string]string{"Authorization": "Bearer ingress-token"})
		requireProtocolEnvelope(t, response, http.StatusServiceUnavailable)
		if strings.Contains(response.Body.String(), "private-secret") {
			t.Fatal("queue error was exposed")
		}
	})
}

func TestAdminOriginPasswordAndSessionLifecycle(t *testing.T) {
	h := newProtocolHarness(t)
	for _, origin := range []string{"", "https://untrusted.test", "https://posthaste.test.evil"} {
		response := protocolRequest(h.engine, http.MethodPost, "/api/v1/admin/auth/login",
			`{"password":"admin-password"}`, map[string]string{"Origin": origin})
		requireProtocolEnvelope(t, response, http.StatusForbidden)
	}
	response := protocolRequest(h.engine, http.MethodPost, "/api/v1/admin/auth/login",
		`{"password":"wrong-password"}`, map[string]string{"Origin": "https://posthaste.test"})
	requireProtocolEnvelope(t, response, http.StatusUnauthorized)
	response = protocolRequest(h.engine, http.MethodPost, "/api/v1/admin/auth/login",
		`{}`, map[string]string{"Origin": "https://posthaste.test"})
	requireProtocolEnvelope(t, response, http.StatusBadRequest)

	response = protocolRequest(h.engine, http.MethodPost, "/api/v1/admin/auth/login",
		`{"password":"admin-password"}`, map[string]string{"Origin": "https://posthaste.test:443"})
	envelope := requireProtocolEnvelope(t, response, http.StatusOK)
	var cookie *http.Cookie
	for _, candidate := range response.Result().Cookies() {
		if candidate.Name == cookieName {
			cookie = candidate
		}
	}
	if cookie == nil || cookie.Value == "" || !cookie.HttpOnly || !cookie.Secure ||
		cookie.SameSite != http.SameSiteLaxMode || cookie.Path != "/api/v1/admin" ||
		cookie.Domain != "" || cookie.MaxAge != 12*60*60 {
		t.Fatalf("invalid session cookie: %+v", cookie)
	}
	var session struct {
		Authenticated bool      `json:"authenticated"`
		ExpiresAt     time.Time `json:"expiresAt"`
	}
	if err := json.Unmarshal(envelope.Data, &session); err != nil {
		t.Fatal(err)
	}
	if !session.Authenticated || time.Until(session.ExpiresAt) < 12*time.Hour-time.Minute ||
		time.Until(session.ExpiresAt) > 12*time.Hour+time.Minute {
		t.Fatalf("invalid session expiry: %+v", session)
	}
	for _, secret := range []string{cookie.Value, "admin-password", "ingress-token"} {
		if strings.Contains(response.Body.String(), secret) {
			t.Fatal("login response exposed a credential")
		}
	}
	response = protocolRequest(h.engine, http.MethodGet, "/api/v1/admin/auth/session", "", nil, cookie)
	requireProtocolEnvelope(t, response, http.StatusOK)
	if strings.Contains(response.Body.String(), cookie.Value) {
		t.Fatal("session response exposed the cookie value")
	}
	for _, origin := range []string{"", "https://untrusted.test"} {
		response = protocolRequest(h.engine, http.MethodPost, "/api/v1/admin/auth/logout", "",
			map[string]string{"Origin": origin}, cookie)
		requireProtocolEnvelope(t, response, http.StatusForbidden)
	}
	response = protocolRequest(h.engine, http.MethodGet, "/api/v1/admin/auth/session", "", nil, cookie)
	requireProtocolEnvelope(t, response, http.StatusOK)
	response = protocolRequest(h.engine, http.MethodPost, "/api/v1/admin/auth/logout", "",
		map[string]string{"Origin": "https://posthaste.test"}, cookie)
	requireProtocolEnvelope(t, response, http.StatusOK)
	cleared := response.Result().Cookies()
	if len(cleared) != 1 || cleared[0].Name != cookieName || cleared[0].Value != "" || cleared[0].MaxAge >= 0 {
		t.Fatalf("logout did not clear the session cookie: %+v", cleared)
	}
	response = protocolRequest(h.engine, http.MethodGet, "/api/v1/admin/auth/session", "", nil, cookie)
	requireProtocolEnvelope(t, response, http.StatusUnauthorized)
}

func TestAdminAccountSavePreservesFalseAndCredentialOmission(t *testing.T) {
	h := newProtocolHarness(t)
	cookie := protocolLogin(t, h)
	headers := map[string]string{"Origin": "https://posthaste.test"}
	body := `{"name":"account","platform":"feishu","enabled":false,"credentials":{"webhookUrl":"private-secret"}}`
	response := protocolRequest(h.engine, http.MethodPost, "/api/v1/admin/accounts/save", body, headers, cookie)
	requireProtocolEnvelope(t, response, http.StatusOK)
	if len(h.store.savedAccounts) != 1 || h.store.savedAccounts[0].Enabled || h.store.savedAccounts[0].Credentials == nil {
		t.Fatal("false enabled or supplied credentials were lost")
	}
	if strings.Contains(response.Body.String(), "private-secret") || strings.Contains(response.Body.String(), `"credentials":`) {
		t.Fatal("account save returned write-only credentials")
	}
	body = fmt.Sprintf(`{"id":%q,"name":"updated","platform":"feishu","enabled":false}`, routeAccountID)
	response = protocolRequest(h.engine, http.MethodPost, "/api/v1/admin/accounts/save", body, headers, cookie)
	requireProtocolEnvelope(t, response, http.StatusOK)
	if len(h.store.savedAccounts) != 2 || h.store.savedAccounts[1].Enabled || h.store.savedAccounts[1].Credentials != nil {
		t.Fatal("omitted credentials were converted into replacement credentials")
	}
	for _, invalid := range []string{
		`{"name":"account","platform":"feishu","credentials":{}}`,
		`{"name":"account","platform":"feishu","enabled":null,"credentials":{}}`,
		`{"name":"account","platform":"feishu","enabled":false}`,
		`{"name":"account","enabled":false,"credentials":{}}`,
		`{"platform":"feishu","enabled":false,"credentials":{}}`,
	} {
		response = protocolRequest(h.engine, http.MethodPost, "/api/v1/admin/accounts/save", invalid, headers, cookie)
		requireProtocolEnvelope(t, response, http.StatusBadRequest)
	}
	if len(h.store.savedAccounts) != 2 {
		t.Fatal("invalid account input reached persistence")
	}
	response = protocolRequest(h.engine, http.MethodGet, "/api/v1/admin/accounts/list", "", nil, cookie)
	requireProtocolEnvelope(t, response, http.StatusOK)
	if strings.Contains(response.Body.String(), `"credentials":`) || strings.Contains(response.Body.String(), "private-secret") {
		t.Fatal("account listing returned credentials")
	}
	if !strings.Contains(response.Body.String(), `"credentialsConfigured":true`) {
		t.Fatal("account listing lost credential configuration state")
	}
}

func TestAdminAccountRoutesRequireSessionAndOriginAndMapDeleteConflicts(t *testing.T) {
	h := newProtocolHarness(t)
	response := protocolRequest(h.engine, http.MethodGet, "/api/v1/admin/accounts/list", "",
		map[string]string{"Authorization": "Bearer ingress-token"})
	requireProtocolEnvelope(t, response, http.StatusUnauthorized)
	body := `{"name":"account","platform":"feishu","enabled":true,"credentials":{}}`
	response = protocolRequest(h.engine, http.MethodPost, "/api/v1/admin/accounts/save", body,
		map[string]string{"Origin": "https://posthaste.test"})
	requireProtocolEnvelope(t, response, http.StatusUnauthorized)
	cookie := protocolLogin(t, h)
	for _, target := range []string{"/api/v1/admin/accounts/save", "/api/v1/admin/accounts/delete"} {
		response = protocolRequest(h.engine, http.MethodPost, target, body, nil, cookie)
		requireProtocolEnvelope(t, response, http.StatusForbidden)
	}
	h.store.deleteAccountErr = domain.ErrConflict
	response = protocolRequest(h.engine, http.MethodPost, "/api/v1/admin/accounts/delete",
		fmt.Sprintf(`{"id":%q}`, routeAccountID), map[string]string{"Origin": "https://posthaste.test"}, cookie)
	requireProtocolEnvelope(t, response, http.StatusConflict)
	if len(h.store.deletedAccounts) != 1 || h.store.deletedAccounts[0] != routeAccountID {
		t.Fatal("account deletion did not reach the guarded store operation")
	}
}

func TestAdminTargetSaveRequiresAccountAndPreservesTargetSwitch(t *testing.T) {
	h := newProtocolHarness(t)
	cookie := protocolLogin(t, h)
	headers := map[string]string{"Origin": "https://posthaste.test"}
	body := fmt.Sprintf(`{"accountId":%q,"name":"target","enabled":false,"config":{}}`, routeAccountID)
	response := protocolRequest(h.engine, http.MethodPost, "/api/v1/admin/targets/save", body, headers, cookie)
	envelope := requireProtocolEnvelope(t, response, http.StatusOK)
	if len(h.store.savedTargets) != 1 || h.store.savedTargets[0].Enabled || h.store.savedTargets[0].AccountID != routeAccountID {
		t.Fatal("account association or false enabled was lost")
	}
	if len(h.sender.credentials) != 0 || len(h.sender.configs) != 1 {
		t.Fatal("target save attempted to validate account credentials")
	}
	var saved map[string]json.RawMessage
	if err := json.Unmarshal(envelope.Data, &saved); err != nil {
		t.Fatal(err)
	}
	for _, removed := range []string{"platform", "credentials", "credentialsConfigured"} {
		if _, exists := saved[removed]; exists {
			t.Fatalf("target response retained account field %s", removed)
		}
	}
	const otherAccountID = "68aeafc9-062d-47bc-86ea-9cc2ca448b6d"
	h.store.accounts[otherAccountID] = domain.Account{ID: otherAccountID, Name: "other account", Platform: "feishu", Enabled: true}
	body = fmt.Sprintf(`{"id":%q,"accountId":%q,"name":"reassigned","enabled":true,"config":{}}`, routeTargetID, otherAccountID)
	response = protocolRequest(h.engine, http.MethodPost, "/api/v1/admin/targets/save", body, headers, cookie)
	requireProtocolEnvelope(t, response, http.StatusOK)
	if len(h.store.savedTargets) != 2 || h.store.savedTargets[1].AccountID != otherAccountID || !h.store.savedTargets[1].Enabled {
		t.Fatal("target could not change account while preserving its own switch")
	}
	for _, invalid := range []string{
		`{"name":"target","enabled":false,"config":{}}`,
		fmt.Sprintf(`{"accountId":%q,"name":"target","config":{}}`, routeAccountID),
		fmt.Sprintf(`{"accountId":%q,"name":"target","enabled":null,"config":{}}`, routeAccountID),
		fmt.Sprintf(`{"accountId":%q,"name":"target","enabled":false}`, routeAccountID),
		fmt.Sprintf(`{"accountId":%q,"name":"target","enabled":false,"config":null}`, routeAccountID),
		`{"name":"legacy target","platform":"feishu","enabled":false,"config":{},"credentials":{"webhookUrl":"private-secret"}}`,
	} {
		response = protocolRequest(h.engine, http.MethodPost, "/api/v1/admin/targets/save", invalid, headers, cookie)
		requireProtocolEnvelope(t, response, http.StatusBadRequest)
	}
	if len(h.store.savedTargets) != 2 {
		t.Fatal("invalid target input reached persistence")
	}
	h.store.saveTargetErr = domain.ErrConflict
	response = protocolRequest(h.engine, http.MethodPost, "/api/v1/admin/targets/save", body, headers, cookie)
	requireProtocolEnvelope(t, response, http.StatusConflict)
}

func TestDisabledParentPreventsAdmissionAndTargetTestWithoutChangingChildSwitch(t *testing.T) {
	h := newProtocolHarness(t)
	cookie := protocolLogin(t, h)
	account := h.store.accounts[routeAccountID]
	account.Enabled = false
	h.store.accounts[routeAccountID] = account
	h.store.targets = []domain.Target{{ID: routeTargetID, AccountID: routeAccountID, Name: "enabled child", Enabled: true, Config: json.RawMessage(`{}`)}}
	// The store contract returns no eligible route members and an effective
	// disabled snapshot when the account is off; the public child keeps its switch.
	h.store.routes[0].Targets = []domain.TargetSnapshot{}
	h.store.snapshot.Enabled = false
	response := protocolRequest(h.engine, http.MethodPost, "/api/v1/notify", `{"message":"hello"}`,
		map[string]string{"Authorization": "Bearer ingress-token"})
	requireProtocolEnvelope(t, response, http.StatusServiceUnavailable)
	response = protocolRequest(h.engine, http.MethodPost, "/api/v1/admin/targets/test",
		fmt.Sprintf(`{"id":%q,"message":"test"}`, routeTargetID), map[string]string{"Origin": "https://posthaste.test"}, cookie)
	requireProtocolEnvelope(t, response, http.StatusServiceUnavailable)
	if len(h.publisher.envelopes) != 0 {
		t.Fatal("disabled account produced a dispatch")
	}
	response = protocolRequest(h.engine, http.MethodGet, "/api/v1/admin/targets/list", "", nil, cookie)
	envelope := requireProtocolEnvelope(t, response, http.StatusOK)
	var listing struct {
		Items []domain.Target `json:"items"`
	}
	if err := json.Unmarshal(envelope.Data, &listing); err != nil {
		t.Fatal(err)
	}
	if len(listing.Items) != 1 || !listing.Items[0].Enabled || listing.Items[0].AccountID != routeAccountID {
		t.Fatal("account disable changed the child's configured switch")
	}
	if strings.Contains(response.Body.String(), `"credentials":`) || strings.Contains(response.Body.String(), `"platform":`) {
		t.Fatal("target listing returned account fields")
	}
}

func TestAdminGroupSaveAcceptsEmptyListAndRejectsMissingOrNullList(t *testing.T) {
	h := newProtocolHarness(t)
	cookie := protocolLogin(t, h)
	headers := map[string]string{"Origin": "https://posthaste.test"}
	response := protocolRequest(h.engine, http.MethodPost, "/api/v1/admin/groups/save",
		`{"name":"empty group","caller":"empty","targetIds":[]}`, headers, cookie)
	requireProtocolEnvelope(t, response, http.StatusOK)
	if len(h.store.savedGroups) != 1 || h.store.savedGroups[0].TargetIDs == nil || len(h.store.savedGroups[0].TargetIDs) != 0 {
		t.Fatal("empty list was not preserved")
	}
	for _, body := range []string{
		`{"name":"missing list","caller":"empty"}`,
		`{"name":"null list","caller":"empty","targetIds":null}`,
	} {
		response = protocolRequest(h.engine, http.MethodPost, "/api/v1/admin/groups/save", body, headers, cookie)
		requireProtocolEnvelope(t, response, http.StatusBadRequest)
	}
	if len(h.store.savedGroups) != 1 {
		t.Fatal("missing or null list reached persistence")
	}
	response = protocolRequest(h.engine, http.MethodPost, "/api/v1/admin/groups/save",
		`{"name":"no origin","caller":"empty","targetIds":[]}`, nil, cookie)
	requireProtocolEnvelope(t, response, http.StatusForbidden)
}

func TestRoutesMapErrorsWithoutReturningRawDetails(t *testing.T) {
	for _, tt := range []struct {
		err    error
		status int
	}{
		{domain.ErrInvalid, 400},
		{domain.ErrNotFound, 404},
		{domain.ErrConflict, 409},
		{domain.ErrUnavailable, 503},
		{errors.New("postgres private-secret DSN"), 500},
	} {
		t.Run(fmt.Sprint(tt.status), func(t *testing.T) {
			h := newProtocolHarness(t)
			h.store.routeErr = fmt.Errorf("private-secret underlying context: %w", tt.err)
			response := protocolRequest(h.engine, http.MethodPost, "/api/v1/notify",
				`{"message":"hello"}`, map[string]string{"Authorization": "Bearer ingress-token"})
			requireProtocolEnvelope(t, response, tt.status)
			if strings.Contains(response.Body.String(), "private-secret") || strings.Contains(response.Body.String(), "postgres") {
				t.Fatal("underlying error was exposed")
			}
		})
	}
}

func TestStaticRoutesKeepUnknownAPIsOutOfSPAFallback(t *testing.T) {
	h := newProtocolHarness(t)
	for _, target := range []string{"/api", "/api/v1/missing", "/api/v9/unknown", "/api/v1/notify"} {
		response := protocolRequest(h.engine, http.MethodGet, target, "", nil)
		requireProtocolEnvelope(t, response, http.StatusNotFound)
		if strings.Contains(response.Body.String(), "Posthaste test frontend") {
			t.Fatal("unknown API returned the frontend")
		}
	}
	for _, target := range []string{"/", "/targets", "/groups/edit"} {
		response := protocolRequest(h.engine, http.MethodGet, target, "", nil)
		if response.Code != 200 || !strings.Contains(response.Body.String(), "Posthaste test frontend") {
			t.Fatalf("SPA route %s returned %d: %s", target, response.Code, response.Body.String())
		}
	}
	response := protocolRequest(h.engine, http.MethodGet, "/assets/app.js", "", nil)
	if response.Code != 200 || !strings.Contains(response.Body.String(), "console.log") {
		t.Fatalf("existing asset returned %d: %s", response.Code, response.Body.String())
	}
	response = protocolRequest(h.engine, http.MethodGet, "/assets/missing.js", "", nil)
	if response.Code != http.StatusNotFound || strings.Contains(response.Body.String(), "Posthaste test frontend") {
		t.Fatal("missing asset fell back to the frontend")
	}
}

func TestRegisterRejectsMissingFrontendBuild(t *testing.T) {
	gin.SetMode(gin.TestMode)
	if err := Register(gin.New(), nil, nil, nil, fstest.MapFS{}); err == nil {
		t.Fatal("missing frontend was accepted")
	}
}
