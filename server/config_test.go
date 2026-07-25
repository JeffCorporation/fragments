package server

import (
	"strings"
	"testing"

	"fragments/catalog"
)

// clearAuthEnv pins every auth-related env var to a known (empty) state so the
// host environment or a developer .env can't leak into the assertions.
func clearAuthEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{
		"FRAGMENTS_PASSWORD", "FRAGMENTS_SECRET", "FRAGMENTS_ADDR",
		"FRAGMENTS_SECURE", "FRAGMENTS_SESSION_DAYS", "FRAGMENTS_TRUSTED_PROXIES",
		"FRAGMENTS_WORKERS",
		"FRAGMENTS_OIDC_ISSUER", "FRAGMENTS_OIDC_CLIENT_ID",
		"FRAGMENTS_OIDC_CLIENT_SECRET", "FRAGMENTS_PUBLIC_URL",
		"FRAGMENTS_OIDC_SCOPES", "FRAGMENTS_OIDC_PROVIDER_NAME",
	} {
		t.Setenv(k, "")
	}
}

func testCatalogConfig(t *testing.T) *catalog.Config {
	t.Helper()
	return &catalog.Config{ThumbDir: t.TempDir()}
}

func TestLoadConfigPasswordRequiredWithoutOIDC(t *testing.T) {
	clearAuthEnv(t)
	if _, err := LoadConfig(testCatalogConfig(t), ""); err == nil {
		t.Fatal("want error when neither FRAGMENTS_PASSWORD nor OIDC is set")
	}

	t.Setenv("FRAGMENTS_PASSWORD", "hunter2")
	cfg, err := LoadConfig(testCatalogConfig(t), "")
	if err != nil {
		t.Fatalf("password-only config: %v", err)
	}
	if cfg.OIDC.Enabled {
		t.Error("OIDC.Enabled = true without OIDC env vars")
	}
}

func TestLoadConfigOIDCReplacesPassword(t *testing.T) {
	clearAuthEnv(t)
	t.Setenv("FRAGMENTS_OIDC_ISSUER", "https://id.example.com")
	t.Setenv("FRAGMENTS_OIDC_CLIENT_ID", "fragments")
	t.Setenv("FRAGMENTS_PUBLIC_URL", "https://photos.example.com/")

	cfg, err := LoadConfig(testCatalogConfig(t), "")
	if err != nil {
		t.Fatalf("OIDC config without password: %v", err)
	}
	o := cfg.OIDC
	if !o.Enabled {
		t.Fatal("OIDC.Enabled = false")
	}
	if o.PublicURL != "https://photos.example.com" {
		t.Errorf("PublicURL = %q, want trailing slash trimmed", o.PublicURL)
	}
	if strings.Join(o.Scopes, " ") != "openid profile email" {
		t.Errorf("default scopes = %v", o.Scopes)
	}
	if o.ProviderName != "OIDC" {
		t.Errorf("default ProviderName = %q, want OIDC", o.ProviderName)
	}
}

func TestLoadConfigOIDCOverrides(t *testing.T) {
	clearAuthEnv(t)
	t.Setenv("FRAGMENTS_OIDC_ISSUER", "https://id.example.com")
	t.Setenv("FRAGMENTS_OIDC_CLIENT_ID", "fragments")
	t.Setenv("FRAGMENTS_PUBLIC_URL", "https://photos.example.com")
	t.Setenv("FRAGMENTS_OIDC_SCOPES", "openid email")
	t.Setenv("FRAGMENTS_OIDC_PROVIDER_NAME", "Pocket ID")
	t.Setenv("FRAGMENTS_OIDC_CLIENT_SECRET", "shhh")

	cfg, err := LoadConfig(testCatalogConfig(t), "")
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	o := cfg.OIDC
	if strings.Join(o.Scopes, " ") != "openid email" {
		t.Errorf("scopes = %v, want [openid email]", o.Scopes)
	}
	if o.ProviderName != "Pocket ID" || o.ClientSecret != "shhh" {
		t.Errorf("ProviderName/ClientSecret not honored: %+v", o)
	}
}

func TestLoadConfigOIDCMisconfigurations(t *testing.T) {
	cases := []struct {
		name string
		env  map[string]string
	}{
		{"issuer without client id", map[string]string{
			"FRAGMENTS_OIDC_ISSUER": "https://id.example.com",
		}},
		{"client id without issuer", map[string]string{
			"FRAGMENTS_OIDC_CLIENT_ID": "fragments",
		}},
		{"missing public url", map[string]string{
			"FRAGMENTS_OIDC_ISSUER":    "https://id.example.com",
			"FRAGMENTS_OIDC_CLIENT_ID": "fragments",
		}},
		{"relative public url", map[string]string{
			"FRAGMENTS_OIDC_ISSUER":    "https://id.example.com",
			"FRAGMENTS_OIDC_CLIENT_ID": "fragments",
			"FRAGMENTS_PUBLIC_URL":     "photos.example.com",
		}},
		{"non-http public url", map[string]string{
			"FRAGMENTS_OIDC_ISSUER":    "https://id.example.com",
			"FRAGMENTS_OIDC_CLIENT_ID": "fragments",
			"FRAGMENTS_PUBLIC_URL":     "ftp://photos.example.com",
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clearAuthEnv(t)
			// A password is set: these must fail on the OIDC problem itself,
			// not on the missing password.
			t.Setenv("FRAGMENTS_PASSWORD", "hunter2")
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			if _, err := LoadConfig(testCatalogConfig(t), ""); err == nil {
				t.Fatal("want error, got nil")
			}
		})
	}
}
