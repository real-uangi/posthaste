package httpadapter

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/real-uangi/posthaste/internal/notification/domain"
)

func TestBarkAccountSupportsOfficialAndSelfHostedServicesWithoutExposingCredentials(t *testing.T) {
	for _, credentials := range []string{
		`{"serverUrl":"https://api.day.app","deviceKey":"private-device-key"}`,
		`{"serverUrl":"http://bark.internal:8080/prefix/","deviceKey":"private-device-key","basicAuth":{"username":"operator","password":"private-password"}}`,
	} {
		h := newProtocolHarness(t)
		cookie := protocolLogin(t, h)
		body := `{"name":"iPhone","platform":"bark","enabled":true,"credentials":` + credentials + `}`
		response := protocolRequest(h.engine, http.MethodPost, "/api/v1/admin/accounts/save", body, map[string]string{"Origin": "https://posthaste.test"}, cookie)
		envelope := requireProtocolEnvelope(t, response, http.StatusOK)
		var account domain.Account
		if err := json.Unmarshal(envelope.Data, &account); err != nil || account.Platform != "bark" || !account.CredentialsConfigured {
			t.Fatal("Bark account was not accepted")
		}
		if len(h.store.savedAccounts) != 1 || string(h.store.savedAccounts[0].Credentials) != credentials {
			t.Fatal("Bark account credentials did not reach the configuration store")
		}
		if strings.Contains(response.Body.String(), "private-") || strings.Contains(response.Body.String(), "deviceKey") || strings.Contains(response.Body.String(), "basicAuth") {
			t.Fatal("Bark account response exposed credentials")
		}

		response = protocolRequest(h.engine, http.MethodPost, "/api/v1/admin/accounts/save", `{"id":"`+account.ID+`","name":"renamed","platform":"bark","enabled":false}`, map[string]string{"Origin": "https://posthaste.test"}, cookie)
		requireProtocolEnvelope(t, response, http.StatusOK)
		if len(h.store.savedAccounts) != 2 || h.store.savedAccounts[1].Credentials != nil || h.store.savedAccounts[1].Enabled {
			t.Fatal("Bark account update lost omission or false semantics")
		}
	}
}

func TestBarkTargetProfilesAndTestSendUseExistingQueue(t *testing.T) {
	h := newProtocolHarness(t)
	cookie := protocolLogin(t, h)
	h.store.accounts[routeAccountID] = domain.Account{ID: routeAccountID, Name: "iPhone", Platform: "bark", Enabled: true, CredentialsConfigured: true}
	profile := `{"group":"operations","level":"critical","sound":"alarm","call":false,"url":"https://status.example.com/alerts"}`
	body := `{"name":"urgent","accountId":"` + routeAccountID + `","enabled":false,"config":` + profile + `}`
	response := protocolRequest(h.engine, http.MethodPost, "/api/v1/admin/targets/save", body, map[string]string{"Origin": "https://posthaste.test"}, cookie)
	requireProtocolEnvelope(t, response, http.StatusOK)
	if len(h.store.savedTargets) != 1 || string(h.store.savedTargets[0].Config) != profile || h.store.savedTargets[0].Enabled {
		t.Fatal("Bark profile lost configuration or false switch")
	}
	h.store.snapshot = domain.TargetSnapshot{
		ID: routeTargetID, AccountID: routeAccountID, Platform: "bark", Enabled: true,
		Config: json.RawMessage(profile), Credentials: json.RawMessage(`{"serverUrl":"https://api.day.app","deviceKey":"private-device-key"}`),
	}
	response = protocolRequest(h.engine, http.MethodPost, "/api/v1/admin/targets/test", `{"id":"`+routeTargetID+`","title":"test","message":"**body**","format":"markdown"}`, map[string]string{"Origin": "https://posthaste.test"}, cookie)
	requireProtocolEnvelope(t, response, http.StatusAccepted)
	if len(h.publisher.envelopes) != 1 || len(h.publisher.envelopes[0].Targets) != 1 {
		t.Fatal("Bark test did not enqueue exactly one target")
	}
	envelope := h.publisher.envelopes[0]
	if envelope.Message.Format != "markdown" || envelope.Targets[0].Platform != "bark" || string(envelope.Targets[0].Config) != profile {
		t.Fatal("Bark test changed the message or target snapshot")
	}
	if strings.Contains(response.Body.String(), "private-device-key") {
		t.Fatal("Bark test response exposed credentials")
	}
}

func TestBarkManagementRejectsInvalidCredentialsAndPublicCredentialFields(t *testing.T) {
	h := newProtocolHarness(t)
	cookie := protocolLogin(t, h)
	for _, credentials := range []string{
		`{"serverUrl":"https://api.day.app","deviceKey":""}`,
		`{"serverUrl":"https://api.day.app?token=private","deviceKey":"private-key"}`,
		`{"serverUrl":"https://api.day.app","deviceKey":"private-key","basicAuth":{"username":"operator"}}`,
	} {
		response := protocolRequest(h.engine, http.MethodPost, "/api/v1/admin/accounts/save", `{"name":"iPhone","platform":"bark","enabled":true,"credentials":`+credentials+`}`, map[string]string{"Origin": "https://posthaste.test"}, cookie)
		requireProtocolEnvelope(t, response, http.StatusBadRequest)
	}
	if len(h.store.savedAccounts) != 0 {
		t.Fatal("invalid Bark account reached persistence")
	}
	h.store.accounts[routeAccountID] = domain.Account{ID: routeAccountID, Platform: "bark"}
	for _, config := range []string{`{"deviceKey":"private-key"}`, `{"serverUrl":"https://api.day.app"}`, `{"level":"invalid"}`, `{"call":"false"}`} {
		response := protocolRequest(h.engine, http.MethodPost, "/api/v1/admin/targets/save", `{"name":"notification","accountId":"`+routeAccountID+`","enabled":true,"config":`+config+`}`, map[string]string{"Origin": "https://posthaste.test"}, cookie)
		requireProtocolEnvelope(t, response, http.StatusBadRequest)
	}
	if len(h.store.savedTargets) != 0 {
		t.Fatal("invalid Bark profile reached persistence")
	}
}
