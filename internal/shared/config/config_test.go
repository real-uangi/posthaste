package config

import (
	"encoding/base64"
	"os"
	"strings"
	"testing"
	"time"
)

func settings(t *testing.T) {
	t.Helper()
	for key, value := range map[string]string{
		"DB_DSN":                   "postgres://test.invalid/posthaste",
		"POSTHASTE_PUBLIC_URL":     "https://Posthaste.test:443/",
		"POSTHASTE_ADMIN_PASSWORD": "admin-test-password",
		"POSTHASTE_INGRESS_TOKEN":  "ingress-test-token",
		"POSTHASTE_ENCRYPTION_KEY": base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 32))),
		"RUN_MODE":                 "release",
	} {
		t.Setenv(key, value)
	}
	for _, key := range []string{"POSTHASTE_EVENTBUS_EXECUTORS", "POSTHASTE_EVENTBUS_DISPATCHER_QUEUE_SIZE", "POSTHASTE_EVENTBUS_EXECUTOR_QUEUE_SIZE", "POSTHASTE_SEND_TIMEOUT"} {
		old, existed := os.LookupEnv(key)
		if err := os.Unsetenv(key); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if existed {
				_ = os.Setenv(key, old)
			} else {
				_ = os.Unsetenv(key)
			}
		})
	}
}

func TestLoadDefaultsWithoutStartingDependencies(t *testing.T) {
	settings(t)
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Origin != "https://posthaste.test" || !cfg.SecureCookies || cfg.Executors != 4 || cfg.DispatcherQueueSize != 64 || cfg.ExecutorQueueSize != 64 || cfg.SendTimeout != 5*time.Second {
		t.Fatalf("unexpected defaults: origin=%s workers=%d", cfg.Origin, cfg.Executors)
	}
}

func TestExplicitInvalidSettingsAreNotDefaulted(t *testing.T) {
	for _, tc := range []struct{ key, value string }{
		{"DB_DSN", ""}, {"POSTHASTE_PUBLIC_URL", "https://test.invalid/a"},
		{"POSTHASTE_PUBLIC_URL", "http://test.invalid"}, {"POSTHASTE_ADMIN_PASSWORD", ""},
		{"POSTHASTE_PUBLIC_URL", "https://:443"},
		{"POSTHASTE_INGRESS_TOKEN", "admin-test-password"}, {"POSTHASTE_ENCRYPTION_KEY", "not-a-key"},
		{"POSTHASTE_EVENTBUS_EXECUTORS", "0"}, {"POSTHASTE_EVENTBUS_EXECUTOR_QUEUE_SIZE", "invalid"},
		{"POSTHASTE_EVENTBUS_DISPATCHER_QUEUE_SIZE", ""}, {"POSTHASTE_SEND_TIMEOUT", "-1s"},
	} {
		t.Run(tc.key+"/"+tc.value, func(t *testing.T) {
			settings(t)
			t.Setenv(tc.key, tc.value)
			if _, err := Load(); err == nil {
				t.Fatal("invalid setting accepted")
			}
		})
	}
}

func TestOriginNormalization(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"https://EXAMPLE.com:443", "https://example.com"},
		{"http://example.com:80/", "http://example.com"},
		{"http://[::1]:80", "http://[::1]"},
		{"https://example.com:8443", "https://example.com:8443"},
	} {
		got, err := CanonicalOrigin(tc.in)
		if err != nil || got != tc.want {
			t.Errorf("%s => %s, %v", tc.in, got, err)
		}
	}
	for _, raw := range []string{"null", "", "https://:443", "http://:80", "https://user:password@example.com", "https://example.com/?x=1", "https://example.com/#x", "https://example.com/path"} {
		if _, err := CanonicalOrigin(raw); err == nil {
			t.Errorf("invalid origin accepted: %s", raw)
		}
	}
}
