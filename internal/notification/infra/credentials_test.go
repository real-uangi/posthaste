package infra

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"

	"github.com/real-uangi/posthaste/internal/notification/domain"
	"github.com/real-uangi/posthaste/internal/shared/config"
)

func TestCredentialsAreAuthenticatedForTheirAccount(t *testing.T) {
	cipher, err := NewCredentialCipher(&config.Config{EncryptionKey: bytes.Repeat([]byte{1}, 32)})
	if err != nil {
		t.Fatal(err)
	}
	plain := []byte(`{"botToken":"123:secret"}`)
	one, err := cipher.Encrypt("account-one", plain)
	if err != nil {
		t.Fatal(err)
	}
	two, err := cipher.Encrypt("account-one", plain)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(one, two) || bytes.Contains(one, plain) {
		t.Fatal("ciphertext reused nonce or exposed plaintext")
	}
	decoded, err := cipher.Decrypt("account-one", one)
	if err != nil || !bytes.Equal(decoded, plain) {
		t.Fatalf("round trip failed: %v", err)
	}
	if _, err := cipher.Decrypt("account-two", one); !errors.Is(err, domain.ErrUnavailable) {
		t.Fatal("ciphertext accepted for another account")
	}
	one[len(one)-1] ^= 1
	if _, err := cipher.Decrypt("account-one", one); !errors.Is(err, domain.ErrUnavailable) {
		t.Fatal("modified ciphertext accepted")
	}
	if _, err := cipher.Decrypt("account-one", []byte("short")); !errors.Is(err, domain.ErrUnavailable) {
		t.Fatal("short ciphertext accepted")
	}
}

func TestPublicAccountNeverSerializesCredentials(t *testing.T) {
	account := publicAccount(accountRecord{ID: "id", CredentialsCiphertext: []byte("private-ciphertext")})
	data, err := json.Marshal(account)
	if err != nil {
		t.Fatal(err)
	}
	var public map[string]any
	if err := json.Unmarshal(data, &public); err != nil {
		t.Fatal(err)
	}
	if _, exists := public["credentials"]; exists {
		t.Fatal("credentials field in response")
	}
	if bytes.Contains(data, []byte("private-ciphertext")) || public["credentialsConfigured"] != true {
		t.Fatal("credential redaction failed")
	}
}
