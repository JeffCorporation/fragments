package server

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/gin-gonic/gin"
	"golang.org/x/oauth2"
)

const (
	oidcStartPath    = "/api/auth/oidc/start"
	oidcCallbackPath = "/api/auth/oidc/callback"

	// oidcStateCookie carries state/nonce/PKCE-verifier between /start and
	// /callback. Scoped to the OIDC paths so it never rides along on API calls.
	oidcStateCookie = "fragments_oidc_state"
	oidcStateTTL    = 10 * time.Minute
)

// oidcAuthenticator drives the Authorization Code + PKCE flow against the
// configured provider (e.g. Pocket ID). On success it mints the same stateless
// HMAC session as password login — no provider tokens are kept; OIDC is purely
// the login gate.
//
// Discovery is lazy: the provider is resolved on first use and cached, so the
// server can boot while the IdP is down (a warm-up goroutine in New surfaces
// configuration errors early without blocking startup).
type oidcAuthenticator struct {
	cfg  OIDCConfig
	auth *authenticator // shares the HMAC secret: the state cookie is signed like a session token

	mu       sync.Mutex
	provider *oidc.Provider
	verifier *oidc.IDTokenVerifier
	oauth    *oauth2.Config
}

func newOIDCAuthenticator(cfg OIDCConfig, auth *authenticator) *oidcAuthenticator {
	return &oidcAuthenticator{cfg: cfg, auth: auth}
}

// init runs OIDC discovery once and caches the resulting endpoints/verifier.
// Safe for concurrent use; a failed attempt is retried on the next call.
func (o *oidcAuthenticator) init(ctx context.Context) (*oauth2.Config, *oidc.IDTokenVerifier, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.provider == nil {
		provider, err := oidc.NewProvider(ctx, o.cfg.Issuer)
		if err != nil {
			// go-oidc requires the discovery document's issuer to match ours
			// byte-for-byte; a stray trailing slash is the classic mismatch.
			return nil, nil, fmt.Errorf("oidc discovery for %s failed (check FRAGMENTS_OIDC_ISSUER, including any trailing slash): %w", o.cfg.Issuer, err)
		}
		o.provider = provider
		o.verifier = provider.Verifier(&oidc.Config{ClientID: o.cfg.ClientID})
		o.oauth = &oauth2.Config{
			ClientID:     o.cfg.ClientID,
			ClientSecret: o.cfg.ClientSecret, // empty → public client, PKCE carries the proof
			Endpoint:     provider.Endpoint(),
			RedirectURL:  o.cfg.PublicURL + oidcCallbackPath,
			Scopes:       o.cfg.Scopes,
		}
	}
	return o.oauth, o.verifier, nil
}

// oidcState is the payload of the state cookie: the CSRF state echoed by the
// provider, the nonce bound into the ID token, and the PKCE code verifier.
type oidcState struct {
	State string `json:"state"`
	Nonce string `json:"nonce"`
	CV    string `json:"cv"`
	Exp   int64  `json:"exp"`
}

// encodeState signs the payload with the session HMAC secret, in the same
// "<payload>.<sig>" shape as session tokens.
func (o *oidcAuthenticator) encodeState(st oidcState) string {
	raw, _ := json.Marshal(st) // struct of strings/int64: cannot fail
	payload := base64.RawURLEncoding.EncodeToString(raw)
	return payload + "." + o.auth.sign(payload)
}

// decodeState verifies the signature and expiry, mirroring validate().
func (o *oidcAuthenticator) decodeState(s string) (oidcState, bool) {
	var st oidcState
	dot := strings.IndexByte(s, '.')
	if dot < 0 {
		return st, false
	}
	payload, sig := s[:dot], s[dot+1:]
	if subtle.ConstantTimeCompare([]byte(sig), []byte(o.auth.sign(payload))) != 1 {
		return st, false
	}
	raw, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil || json.Unmarshal(raw, &st) != nil {
		return oidcState{}, false
	}
	if time.Now().Unix() >= st.Exp {
		return oidcState{}, false
	}
	return st, true
}

func randToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func (s *Server) setOIDCStateCookie(c *gin.Context, value string, maxAge int) {
	http.SetCookie(c.Writer, &http.Cookie{
		Name: oidcStateCookie, Value: value, Path: "/api/auth/oidc", MaxAge: maxAge,
		// SameSite must stay Lax: the callback is a top-level navigation from
		// the IdP, and Strict would drop the cookie there, breaking every login.
		HttpOnly: true, Secure: s.auth.secure, SameSite: http.SameSiteLaxMode,
	})
}

// loginRedirect sends the browser back to the SPA login page. These handlers
// answer top-level navigations, not XHR, so errors travel as a query code the
// SPA maps to a message; details stay in the server log.
func loginRedirect(c *gin.Context, errCode string) {
	target := "/login"
	if errCode != "" {
		target += "?error=" + errCode
	}
	c.Redirect(http.StatusFound, target)
}

// handleOIDCStart begins the Authorization Code + PKCE flow: it parks
// state/nonce/verifier in a signed short-lived cookie and forwards the browser
// to the provider's authorization endpoint.
func (s *Server) handleOIDCStart(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	oa, _, err := s.oidcAuth.init(ctx)
	if err != nil {
		s.log.Printf("oidc: %v", err)
		loginRedirect(c, "oidc_unavailable")
		return
	}

	state, err1 := randToken()
	nonce, err2 := randToken()
	if err1 != nil || err2 != nil {
		s.log.Printf("oidc: generating state/nonce failed: %v %v", err1, err2)
		loginRedirect(c, "oidc_failed")
		return
	}
	cv := oauth2.GenerateVerifier()

	st := oidcState{State: state, Nonce: nonce, CV: cv, Exp: time.Now().Add(oidcStateTTL).Unix()}
	s.setOIDCStateCookie(c, s.oidcAuth.encodeState(st), int(oidcStateTTL.Seconds()))

	c.Redirect(http.StatusFound, oa.AuthCodeURL(state, oidc.Nonce(nonce), oauth2.S256ChallengeOption(cv)))
}

// handleOIDCCallback finishes the flow: state check against the signed cookie,
// code exchange (PKCE), ID-token verification (signature, issuer, audience,
// expiry, nonce), then the regular stateless session cookie.
func (s *Server) handleOIDCCallback(c *gin.Context) {
	cookie, cookieErr := c.Cookie(oidcStateCookie)
	// Single-use either way: expire the state cookie before any outcome.
	s.setOIDCStateCookie(c, "", -1)

	if errCode := c.Query("error"); errCode != "" {
		// The provider refused (e.g. access_denied: user not allowed on the
		// client in Pocket ID). Not our error to diagnose. %q: the value is
		// attacker-controlled on an unauthenticated endpoint — escape it so a
		// crafted ?error= can't forge log lines (CWE-117).
		s.log.Printf("oidc: provider returned error=%q", errCode)
		loginRedirect(c, "access_denied")
		return
	}

	st, ok := oidcState{}, false
	if cookieErr == nil {
		st, ok = s.oidcAuth.decodeState(cookie)
	}
	if !ok || subtle.ConstantTimeCompare([]byte(c.Query("state")), []byte(st.State)) != 1 {
		s.log.Printf("oidc: state mismatch or missing/expired state cookie")
		loginRedirect(c, "oidc_failed")
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	oa, verifier, err := s.oidcAuth.init(ctx)
	if err != nil {
		s.log.Printf("oidc: %v", err)
		loginRedirect(c, "oidc_unavailable")
		return
	}

	tok, err := oa.Exchange(ctx, c.Query("code"), oauth2.VerifierOption(st.CV))
	if err != nil {
		s.log.Printf("oidc: code exchange failed: %v", err)
		loginRedirect(c, "oidc_failed")
		return
	}
	rawID, ok := tok.Extra("id_token").(string)
	if !ok {
		s.log.Printf("oidc: token response has no id_token")
		loginRedirect(c, "oidc_failed")
		return
	}
	idToken, err := verifier.Verify(ctx, rawID)
	if err != nil {
		s.log.Printf("oidc: id_token verification failed: %v", err)
		loginRedirect(c, "oidc_failed")
		return
	}
	if idToken.Nonce != st.Nonce {
		s.log.Printf("oidc: nonce mismatch")
		loginRedirect(c, "oidc_failed")
		return
	}

	exp := time.Now().Add(s.auth.ttl)
	s.setSessionCookie(c, s.auth.issue(exp), exp)
	c.Redirect(http.StatusFound, "/")
}

// handleAuthConfig tells the SPA which login UI to render. Public by design:
// it reveals only what the login page shows anyway.
func (s *Server) handleAuthConfig(c *gin.Context) {
	if s.cfg.OIDC.Enabled {
		c.JSON(http.StatusOK, gin.H{"mode": "oidc", "providerName": s.cfg.OIDC.ProviderName})
		return
	}
	c.JSON(http.StatusOK, gin.H{"mode": "password"})
}
