// Package config loads and validates process configuration from the
// environment. Loading fails fast with a combined error listing every
// problem, so a misconfigured container reports all of its issues at once.
package config

import (
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/mail"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// ExternalMode selects the implementation of the external directory.
type ExternalMode string

const (
	ExternalMSSQL ExternalMode = "mssql"
	ExternalMock  ExternalMode = "mock"
)

// Config is the fully validated configuration of the process.
type Config struct {
	Addr    string
	BaseURL string
	DataDir string

	ExternalMode ExternalMode
	MSSQLDSN     string

	// EncryptionKey is the raw 32-byte AES key used for secrets at rest.
	EncryptionKey []byte

	SessionIdleTimeout     time.Duration
	SessionAbsoluteTimeout time.Duration
	CookieSecure           bool

	TrustedProxyCIDRs []*net.IPNet

	BootstrapAdminEmail    string
	BootstrapAdminPassword string

	// Admin2FAEnabled gates the mandatory TOTP step on admin login. It
	// defaults to true (as the specification requires) and may only be
	// turned off when EXTERNAL_DB_MODE=mock, so it cannot be disabled
	// against a real deployment by mistake - see DECISIONS.md.
	Admin2FAEnabled bool

	ClientListCacheTTL time.Duration

	LogLevel slog.Level
}

// DBPath is the location of the SQLite database file.
func (c Config) DBPath() string { return filepath.Join(c.DataDir, "app.db") }

// BackupDir is the directory holding periodic SQLite backups.
func (c Config) BackupDir() string { return filepath.Join(c.DataDir, "backups") }

// Load reads configuration from the environment and validates it.
func Load() (Config, error) {
	var errs []error
	fail := func(format string, a ...any) { errs = append(errs, fmt.Errorf(format, a...)) }

	c := Config{
		Addr:                   envOr("APP_ADDR", ":8080"),
		DataDir:                envOr("DATA_DIR", "/data"),
		BootstrapAdminEmail:    strings.TrimSpace(os.Getenv("BOOTSTRAP_ADMIN_EMAIL")),
		BootstrapAdminPassword: os.Getenv("BOOTSTRAP_ADMIN_PASSWORD"),
	}

	c.BaseURL = strings.TrimRight(strings.TrimSpace(os.Getenv("APP_BASE_URL")), "/")
	if c.BaseURL == "" {
		fail("APP_BASE_URL is required (e.g. https://moduli.example.com)")
	} else if !strings.HasPrefix(c.BaseURL, "http://") && !strings.HasPrefix(c.BaseURL, "https://") {
		fail("APP_BASE_URL must start with http:// or https://, got %q", c.BaseURL)
	}

	switch m := ExternalMode(strings.ToLower(strings.TrimSpace(envOr("EXTERNAL_DB_MODE", string(ExternalMock))))); m {
	case ExternalMSSQL:
		c.ExternalMode = m
		c.MSSQLDSN = os.Getenv("MSSQL_DSN")
		if strings.TrimSpace(c.MSSQLDSN) == "" {
			fail("MSSQL_DSN is required when EXTERNAL_DB_MODE=mssql")
		}
	case ExternalMock:
		c.ExternalMode = m
	default:
		fail("EXTERNAL_DB_MODE must be %q or %q, got %q", ExternalMSSQL, ExternalMock, m)
	}

	enabled2FA, err := boolOr("ADMIN_2FA_ENABLED", true)
	if err != nil {
		fail("ADMIN_2FA_ENABLED: %v", err)
	}
	c.Admin2FAEnabled = enabled2FA
	if !c.Admin2FAEnabled && c.ExternalMode == ExternalMSSQL {
		fail("ADMIN_2FA_ENABLED=false is only allowed with EXTERNAL_DB_MODE=mock; " +
			"admin 2FA must stay on against a real deployment")
	}

	switch raw := strings.TrimSpace(os.Getenv("APP_ENCRYPTION_KEY")); {
	case raw == "":
		fail("APP_ENCRYPTION_KEY is required (32 random bytes, base64-encoded)")
	default:
		key, err := decodeBase64(raw)
		if err != nil {
			fail("APP_ENCRYPTION_KEY is not valid base64: %v", err)
		} else if len(key) != 32 {
			fail("APP_ENCRYPTION_KEY must decode to exactly 32 bytes, got %d", len(key))
		} else {
			c.EncryptionKey = key
		}
	}

	c.SessionIdleTimeout = durationOr(&errs, "SESSION_IDLE_TIMEOUT", 2*time.Hour)
	c.SessionAbsoluteTimeout = durationOr(&errs, "SESSION_ABSOLUTE_TIMEOUT", 12*time.Hour)
	c.ClientListCacheTTL = durationOr(&errs, "CLIENT_LIST_CACHE_TTL", 5*time.Minute)
	if c.SessionIdleTimeout > 0 && c.SessionAbsoluteTimeout > 0 && c.SessionIdleTimeout > c.SessionAbsoluteTimeout {
		fail("SESSION_IDLE_TIMEOUT (%s) must not exceed SESSION_ABSOLUTE_TIMEOUT (%s)",
			c.SessionIdleTimeout, c.SessionAbsoluteTimeout)
	}

	secure, err := boolOr("COOKIE_SECURE", true)
	if err != nil {
		fail("COOKIE_SECURE: %v", err)
	}
	c.CookieSecure = secure

	nets, err := parseCIDRs(os.Getenv("TRUSTED_PROXY_CIDRS"))
	if err != nil {
		fail("TRUSTED_PROXY_CIDRS: %v", err)
	}
	c.TrustedProxyCIDRs = nets

	if c.BootstrapAdminEmail != "" {
		if _, err := mail.ParseAddress(c.BootstrapAdminEmail); err != nil {
			fail("BOOTSTRAP_ADMIN_EMAIL is not a valid email address: %v", err)
		}
		if len([]rune(c.BootstrapAdminPassword)) < 10 {
			fail("BOOTSTRAP_ADMIN_PASSWORD must be at least 10 characters")
		}
	} else if c.BootstrapAdminPassword != "" {
		fail("BOOTSTRAP_ADMIN_PASSWORD is set but BOOTSTRAP_ADMIN_EMAIL is not")
	}

	lvl, err := parseLevel(envOr("LOG_LEVEL", "info"))
	if err != nil {
		fail("LOG_LEVEL: %v", err)
	}
	c.LogLevel = lvl

	if len(errs) > 0 {
		return Config{}, fmt.Errorf("invalid configuration:\n  - %s", joinErrors(errs, "\n  - "))
	}
	return c, nil
}

func envOr(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

// decodeBase64 accepts both standard and URL-safe base64, padded or not, so
// that a key pasted from any of the usual tools works.
func decodeBase64(s string) ([]byte, error) {
	encodings := []*base64.Encoding{
		base64.StdEncoding, base64.RawStdEncoding,
		base64.URLEncoding, base64.RawURLEncoding,
	}
	var err error
	for _, enc := range encodings {
		var b []byte
		if b, err = enc.DecodeString(s); err == nil {
			return b, nil
		}
	}
	return nil, err
}

func durationOr(errs *[]error, key string, def time.Duration) time.Duration {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return def
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		*errs = append(*errs, fmt.Errorf("%s is not a valid duration (e.g. 90m, 12h): %v", key, err))
		return def
	}
	if d <= 0 {
		*errs = append(*errs, fmt.Errorf("%s must be positive, got %s", key, d))
		return def
	}
	return d
}

func boolOr(key string, def bool) (bool, error) {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return def, nil
	}
	v, err := strconv.ParseBool(raw)
	if err != nil {
		return def, fmt.Errorf("must be true or false, got %q", raw)
	}
	return v, nil
}

func parseCIDRs(raw string) ([]*net.IPNet, error) {
	var out []*net.IPNet
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		_, n, err := net.ParseCIDR(part)
		if err != nil {
			return nil, fmt.Errorf("%q is not a valid CIDR block (e.g. 172.16.0.0/12)", part)
		}
		out = append(out, n)
	}
	return out, nil
}

func parseLevel(s string) (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return slog.LevelInfo, errors.New("must be one of debug, info, warn, error")
	}
}

func joinErrors(errs []error, sep string) string {
	parts := make([]string, len(errs))
	for i, e := range errs {
		parts[i] = e.Error()
	}
	return strings.Join(parts, sep)
}
