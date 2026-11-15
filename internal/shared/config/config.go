package config

import (
	"encoding/base64"
	"errors"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config contains startup settings. Never log this value: it contains credentials.
type Config struct {
	Origin              string
	SecureCookies       bool
	AdminPassword       string
	IngressToken        string
	EncryptionKey       []byte
	Executors           int
	DispatcherQueueSize int
	ExecutorQueueSize   int
	SendTimeout         time.Duration
}

func Load() (*Config, error) {
	if os.Getenv("DB_DSN") == "" {
		return nil, errors.New("DB_DSN is required")
	}
	origin, err := CanonicalOrigin(os.Getenv("POSTHASTE_PUBLIC_URL"))
	if err != nil {
		return nil, errors.New("POSTHASTE_PUBLIC_URL must be an HTTP(S) origin")
	}
	password, token := os.Getenv("POSTHASTE_ADMIN_PASSWORD"), os.Getenv("POSTHASTE_INGRESS_TOKEN")
	if password == "" || token == "" || password == token {
		return nil, errors.New("distinct POSTHASTE_ADMIN_PASSWORD and POSTHASTE_INGRESS_TOKEN are required")
	}
	key, err := base64.StdEncoding.DecodeString(os.Getenv("POSTHASTE_ENCRYPTION_KEY"))
	if err != nil || len(key) != 32 {
		return nil, errors.New("POSTHASTE_ENCRYPTION_KEY must encode 32 bytes in standard Base64")
	}
	cfg := &Config{Origin: origin, SecureCookies: strings.HasPrefix(origin, "https://"), AdminPassword: password, IngressToken: token, EncryptionKey: key, SendTimeout: 5 * time.Second}
	for _, setting := range []struct {
		name     string
		dest     *int
		fallback int
	}{
		{"POSTHASTE_EVENTBUS_EXECUTORS", &cfg.Executors, 4},
		{"POSTHASTE_EVENTBUS_DISPATCHER_QUEUE_SIZE", &cfg.DispatcherQueueSize, 64},
		{"POSTHASTE_EVENTBUS_EXECUTOR_QUEUE_SIZE", &cfg.ExecutorQueueSize, 64},
	} {
		*setting.dest = setting.fallback
		if raw, present := os.LookupEnv(setting.name); present {
			value, err := strconv.Atoi(raw)
			if err != nil || value <= 0 {
				return nil, errors.New(setting.name + " must be a positive integer")
			}
			*setting.dest = value
		}
	}
	if raw, present := os.LookupEnv("POSTHASTE_SEND_TIMEOUT"); present {
		cfg.SendTimeout, err = time.ParseDuration(raw)
		if err != nil || cfg.SendTimeout <= 0 {
			return nil, errors.New("POSTHASTE_SEND_TIMEOUT must be a positive duration")
		}
	}
	if os.Getenv("RUN_MODE") == "release" && !cfg.SecureCookies {
		return nil, errors.New("POSTHASTE_PUBLIC_URL must use HTTPS in release mode")
	}
	return cfg, nil
}

// CanonicalOrigin normalizes default ports so browser and configured origins agree.
func CanonicalOrigin(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return "", errors.New("invalid origin")
	}
	u.Host = strings.ToLower(u.Host)
	if (u.Scheme == "https" && u.Port() == "443") || (u.Scheme == "http" && u.Port() == "80") {
		u.Host = u.Hostname()
		if strings.Contains(u.Host, ":") {
			u.Host = "[" + u.Host + "]"
		}
	}
	return u.Scheme + "://" + u.Host, nil
}
