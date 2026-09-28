package config

import (
	"testing"
)

// withEnv sets environment variables for the duration of the test.
func withEnv(t *testing.T, kv map[string]string) {
	t.Helper()
	for k, v := range kv {
		t.Setenv(k, v)
	}
}

// baseEnv is a minimal valid configuration, so each test only needs to
// override what it is actually testing.
func baseEnv() map[string]string {
	return map[string]string{
		"APP_BASE_URL":          "http://localhost:8080",
		"EXTERNAL_DB_MODE":      "mock",
		"APP_ENCRYPTION_KEY":    "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=", // 32 raw bytes, base64
		"ENTRY_LINK_SECRET":     "MDEyMzQ1Njc4OWFiY2RlZg==",                     // 16 raw bytes, base64
		"COOKIE_SECURE":         "false",
		"BOOTSTRAP_ADMIN_EMAIL": "",
	}
}

func TestLoadDefaultsTo2FAEnabled(t *testing.T) {
	withEnv(t, baseEnv())
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.Admin2FAEnabled {
		t.Error("Admin2FAEnabled defaulted to false, want true")
	}
}

func TestLoadAllows2FADisabledInMockMode(t *testing.T) {
	env := baseEnv()
	env["EXTERNAL_DB_MODE"] = "mock"
	env["ADMIN_2FA_ENABLED"] = "false"
	withEnv(t, env)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Admin2FAEnabled {
		t.Error("Admin2FAEnabled = true, want false")
	}
}

// TestLoadRefusesToDisable2FAAgainstMSSQL is the safety rail: 2FA may only be
// turned off for local/dev use against the mock directory, never against a
// real deployment.
func TestLoadRefusesToDisable2FAAgainstMSSQL(t *testing.T) {
	env := baseEnv()
	env["EXTERNAL_DB_MODE"] = "mssql"
	env["MSSQL_DSN"] = "sqlserver://user:pass@host:1433?database=db"
	env["ADMIN_2FA_ENABLED"] = "false"
	withEnv(t, env)

	if _, err := Load(); err == nil {
		t.Fatal("Load accepted ADMIN_2FA_ENABLED=false with EXTERNAL_DB_MODE=mssql")
	}
}

func TestLoadRejectsAnInvalid2FAFlag(t *testing.T) {
	env := baseEnv()
	env["ADMIN_2FA_ENABLED"] = "not-a-bool"
	withEnv(t, env)

	if _, err := Load(); err == nil {
		t.Fatal("Load accepted an invalid ADMIN_2FA_ENABLED value")
	}
}

func TestLoadDefaultsToEntryLinkSigningEnabled(t *testing.T) {
	withEnv(t, baseEnv())
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.EntryLinkSigningEnabled {
		t.Error("EntryLinkSigningEnabled defaulted to false, want true")
	}
	if len(cfg.EntryLinkSecret) != 16 {
		t.Errorf("EntryLinkSecret decoded to %d bytes, want 16", len(cfg.EntryLinkSecret))
	}
}

func TestLoadRequiresEntryLinkSecretWhenSigningEnabled(t *testing.T) {
	env := baseEnv()
	env["ENTRY_LINK_SECRET"] = ""
	withEnv(t, env)

	if _, err := Load(); err == nil {
		t.Fatal("Load accepted a missing ENTRY_LINK_SECRET with signing enabled")
	}
}

// TestLoadRefusesToDisableEntryLinkSigningAgainstMSSQL is the same safety
// rail as 2FA: unsigned entry links may only be used for local/dev testing
// against the mock directory, never against a real deployment.
func TestLoadRefusesToDisableEntryLinkSigningAgainstMSSQL(t *testing.T) {
	env := baseEnv()
	env["EXTERNAL_DB_MODE"] = "mssql"
	env["MSSQL_DSN"] = "sqlserver://user:pass@host:1433?database=db"
	env["ENTRY_LINK_SIGNING_ENABLED"] = "false"
	withEnv(t, env)

	if _, err := Load(); err == nil {
		t.Fatal("Load accepted ENTRY_LINK_SIGNING_ENABLED=false with EXTERNAL_DB_MODE=mssql")
	}
}

func TestLoadAllowsEntryLinkSigningDisabledInMockMode(t *testing.T) {
	env := baseEnv()
	env["ENTRY_LINK_SIGNING_ENABLED"] = "false"
	env["ENTRY_LINK_SECRET"] = ""
	withEnv(t, env)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.EntryLinkSigningEnabled {
		t.Error("EntryLinkSigningEnabled = true, want false")
	}
}
