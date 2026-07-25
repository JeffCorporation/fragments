package server

import (
	"crypto/rand"
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"fragments/catalog"
)

// Config is everything the HTTP server needs. It is derived from the catalog
// Config (for the thumbnail directory) plus a handful of FRAGMENTS_* env vars.
type Config struct {
	Addr       string        // listen address, e.g. ":8080"
	Password   string        // shared login password (FRAGMENTS_PASSWORD)
	Secret     []byte        // HMAC key for signing session cookies
	Secure     bool          // set the Secure cookie flag (true behind TLS)
	ThumbDir   string        // directory of generated thumbnails
	SessionTTL time.Duration // session lifetime
	Workers    int           // default worker-pool concurrency (FRAGMENTS_WORKERS)

	// TrustedProxies is the CIDR/IP list to trust for X-Forwarded-For
	// (FRAGMENTS_TRUSTED_PROXIES). Empty → trust none, so c.ClientIP() is the
	// real peer. Behind a reverse proxy (Caddy/nginx) set it to the proxy's
	// address so the forwarded client IP is honored — otherwise every client
	// collapses to the proxy IP and the login rate-limiter buckets them together.
	TrustedProxies []string

	// SecretGenerated is true when no FRAGMENTS_SECRET was provided and a random
	// one was generated; the caller should warn that sessions won't survive a
	// restart.
	SecretGenerated bool

	// OIDC, when Enabled, replaces password login entirely: /api/login is
	// disabled and FRAGMENTS_PASSWORD is not required.
	OIDC OIDCConfig
}

// OIDCConfig configures OIDC login (FRAGMENTS_OIDC_* env vars). It is enabled
// when both Issuer and ClientID are set; the provider (e.g. Pocket ID) then
// becomes the only authentication method.
type OIDCConfig struct {
	Enabled      bool
	Issuer       string   // FRAGMENTS_OIDC_ISSUER, kept verbatim: go-oidc requires an exact match with the discovery document (trailing slash included)
	ClientID     string   // FRAGMENTS_OIDC_CLIENT_ID
	ClientSecret string   // FRAGMENTS_OIDC_CLIENT_SECRET; empty → public client, PKCE only
	PublicURL    string   // FRAGMENTS_PUBLIC_URL, absolute http(s) URL without trailing slash; redirect URI = PublicURL + oidcCallbackPath
	Scopes       []string // FRAGMENTS_OIDC_SCOPES (space-separated), default "openid profile email"
	ProviderName string   // FRAGMENTS_OIDC_PROVIDER_NAME, login-button label in the SPA
}

// loadOIDCConfig reads the FRAGMENTS_OIDC_* env vars. Setting only one of
// issuer/client-id is a misconfiguration worth refusing to start over: the
// operator clearly intended OIDC, and silently falling back to password auth
// would leave the instance protected by a different method than expected.
func loadOIDCConfig() (OIDCConfig, error) {
	issuer := strings.TrimSpace(os.Getenv("FRAGMENTS_OIDC_ISSUER"))
	clientID := strings.TrimSpace(os.Getenv("FRAGMENTS_OIDC_CLIENT_ID"))
	if issuer == "" && clientID == "" {
		return OIDCConfig{}, nil
	}
	if issuer == "" || clientID == "" {
		return OIDCConfig{}, fmt.Errorf("FRAGMENTS_OIDC_ISSUER and FRAGMENTS_OIDC_CLIENT_ID must both be set to enable OIDC")
	}

	publicURL := strings.TrimSpace(os.Getenv("FRAGMENTS_PUBLIC_URL"))
	if publicURL == "" {
		return OIDCConfig{}, fmt.Errorf("FRAGMENTS_PUBLIC_URL is required when OIDC is enabled (external base URL, e.g. https://photos.example.com)")
	}
	u, err := url.Parse(publicURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return OIDCConfig{}, fmt.Errorf("FRAGMENTS_PUBLIC_URL: %q is not an absolute http(s) URL", publicURL)
	}

	scopes := strings.Fields(os.Getenv("FRAGMENTS_OIDC_SCOPES"))
	if len(scopes) == 0 {
		scopes = []string{"openid", "profile", "email"}
	}

	return OIDCConfig{
		Enabled:      true,
		Issuer:       issuer,
		ClientID:     clientID,
		ClientSecret: os.Getenv("FRAGMENTS_OIDC_CLIENT_SECRET"),
		PublicURL:    strings.TrimRight(publicURL, "/"),
		Scopes:       scopes,
		ProviderName: envOr("FRAGMENTS_OIDC_PROVIDER_NAME", "OIDC"),
	}, nil
}

// LoadConfig reads the FRAGMENTS_* environment (the .env is already loaded by the
// catalog config) and validates the required fields. addr, if non-empty,
// overrides FRAGMENTS_ADDR.
func LoadConfig(cat *catalog.Config, addr string) (Config, error) {
	oidcCfg, err := loadOIDCConfig()
	if err != nil {
		return Config{}, err
	}

	// OIDC, when configured, is the only auth method — the password is then
	// neither required nor used.
	password := os.Getenv("FRAGMENTS_PASSWORD")
	if password == "" && !oidcCfg.Enabled {
		return Config{}, fmt.Errorf("FRAGMENTS_PASSWORD is required (set it in .env or the environment)")
	}

	if addr == "" {
		// Localhost-only by default; `serve -network` (or an explicit -addr /
		// FRAGMENTS_ADDR binding to 0.0.0.0) opts into LAN exposure.
		addr = envOr("FRAGMENTS_ADDR", "127.0.0.1:8080")
	}

	secret := []byte(os.Getenv("FRAGMENTS_SECRET"))
	generated := false
	if len(secret) < 16 {
		secret = make([]byte, 32)
		if _, err := rand.Read(secret); err != nil {
			return Config{}, fmt.Errorf("generate session secret: %w", err)
		}
		generated = true
	}

	days := 30
	if d := os.Getenv("FRAGMENTS_SESSION_DAYS"); d != "" {
		if n, err := strconv.Atoi(d); err == nil && n > 0 {
			days = n
		}
	}

	workers := 0 // 0 → the coordinator's own default
	if w := os.Getenv("FRAGMENTS_WORKERS"); w != "" {
		if n, err := strconv.Atoi(w); err == nil && n > 0 {
			workers = n
		}
	}

	// Validate eagerly: gin's SetTrustedProxies silently keeps only the entries
	// preceding a malformed one, which would degrade client-IP attribution (and
	// the login rate-limiter) without any signal. Better to refuse to start.
	var trusted []string
	for _, p := range strings.Split(os.Getenv("FRAGMENTS_TRUSTED_PROXIES"), ",") {
		if p = strings.TrimSpace(p); p == "" {
			continue
		}
		if _, _, err := net.ParseCIDR(p); err != nil && net.ParseIP(p) == nil {
			return Config{}, fmt.Errorf("FRAGMENTS_TRUSTED_PROXIES: %q is not an IP or CIDR", p)
		}
		trusted = append(trusted, p)
	}

	return Config{
		Addr:            addr,
		Password:        password,
		Secret:          secret,
		Secure:          strings.EqualFold(os.Getenv("FRAGMENTS_SECURE"), "true"),
		ThumbDir:        cat.ThumbDir,
		SessionTTL:      time.Duration(days) * 24 * time.Hour,
		Workers:         workers,
		TrustedProxies:  trusted,
		SecretGenerated: generated,
		OIDC:            oidcCfg,
	}, nil
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
