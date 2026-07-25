package server

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"

	"fragments/catalog"
)

// fakeIdP is a minimal OIDC provider: discovery, JWKS, and a token endpoint
// that mints RS256 ID tokens. The authorize endpoint is never served — tests
// jump straight to the callback, as a browser would after the IdP redirect.
type fakeIdP struct {
	srv *httptest.Server
	key *rsa.PrivateKey

	mu            sync.Mutex
	nonce         string // nonce to embed in the next ID token
	aud           string // audience to embed
	lastVerifier  string // code_verifier received by the token endpoint
	tokenRequests int
}

func newFakeIdP(t *testing.T) *fakeIdP {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate rsa key: %v", err)
	}
	p := &fakeIdP{key: key, aud: "fragments"}

	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"issuer":                                p.srv.URL,
			"authorization_endpoint":                p.srv.URL + "/auth",
			"token_endpoint":                        p.srv.URL + "/token",
			"jwks_uri":                              p.srv.URL + "/keys",
			"id_token_signing_alg_values_supported": []string{"RS256"},
		})
	})
	mux.HandleFunc("/keys", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{
			{Key: &key.PublicKey, KeyID: "testkey", Algorithm: "RS256", Use: "sig"},
		}})
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			http.Error(w, "bad form", http.StatusBadRequest)
			return
		}
		p.mu.Lock()
		p.tokenRequests++
		p.lastVerifier = r.PostFormValue("code_verifier")
		nonce, aud := p.nonce, p.aud
		p.mu.Unlock()
		idToken := p.signIDToken(t, nonce, aud)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"access_token": "test-access-token",
			"token_type":   "bearer",
			"id_token":     idToken,
		})
	})

	p.srv = httptest.NewServer(mux)
	t.Cleanup(p.srv.Close)
	return p
}

func (p *fakeIdP) signIDToken(t *testing.T, nonce, aud string) string {
	t.Helper()
	signer, err := jose.NewSigner(
		jose.SigningKey{Algorithm: jose.RS256, Key: jose.JSONWebKey{Key: p.key, KeyID: "testkey"}},
		(&jose.SignerOptions{}).WithType("JWT"),
	)
	if err != nil {
		t.Fatalf("new signer: %v", err)
	}
	now := time.Now()
	claims, _ := json.Marshal(map[string]any{
		"iss":   p.srv.URL,
		"sub":   "test-user",
		"aud":   aud,
		"exp":   now.Add(5 * time.Minute).Unix(),
		"iat":   now.Unix(),
		"nonce": nonce,
	})
	obj, err := signer.Sign(claims)
	if err != nil {
		t.Fatalf("sign id token: %v", err)
	}
	s, err := obj.CompactSerialize()
	if err != nil {
		t.Fatalf("serialize id token: %v", err)
	}
	return s
}

var testSecret = []byte("0123456789abcdef0123456789abcdef")

func newOIDCTestServer(t *testing.T, idp *fakeIdP) *Server {
	t.Helper()
	cfg := Config{
		Addr:       "127.0.0.1:0",
		Secret:     testSecret,
		SessionTTL: time.Hour,
		ThumbDir:   t.TempDir(),
		OIDC: OIDCConfig{
			Enabled:      true,
			Issuer:       idp.srv.URL,
			ClientID:     "fragments",
			PublicURL:    "http://app.test",
			Scopes:       []string{"openid", "profile", "email"},
			ProviderName: "Pocket ID",
		},
	}
	return New(cfg, &catalog.Config{ThumbDir: cfg.ThumbDir}, nil, nil, nil)
}

// startFlow drives GET /api/auth/oidc/start and returns the state cookie plus
// the parsed authorization redirect URL.
func startFlow(t *testing.T, s *Server) (*http.Cookie, *url.URL) {
	t.Helper()
	w := httptest.NewRecorder()
	s.engine.ServeHTTP(w, httptest.NewRequest(http.MethodGet, oidcStartPath, nil))
	if w.Code != http.StatusFound {
		t.Fatalf("start: got %d, want 302 (body: %s)", w.Code, w.Body.String())
	}
	loc, err := url.Parse(w.Result().Header.Get("Location"))
	if err != nil {
		t.Fatalf("start: bad Location: %v", err)
	}
	var state *http.Cookie
	for _, ck := range w.Result().Cookies() {
		if ck.Name == oidcStateCookie {
			state = ck
		}
	}
	if state == nil || state.Value == "" {
		t.Fatalf("start: no %s cookie set", oidcStateCookie)
	}
	return state, loc
}

func callback(t *testing.T, s *Server, query string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, oidcCallbackPath+"?"+query, nil)
	for _, ck := range cookies {
		req.AddCookie(ck)
	}
	w := httptest.NewRecorder()
	s.engine.ServeHTTP(w, req)
	return w
}

func findSessionCookie(w *httptest.ResponseRecorder) *http.Cookie {
	for _, ck := range w.Result().Cookies() {
		if ck.Name == sessionCookie && ck.Value != "" {
			return ck
		}
	}
	return nil
}

func TestOIDCStartRedirect(t *testing.T) {
	idp := newFakeIdP(t)
	s := newOIDCTestServer(t, idp)

	stateCk, loc := startFlow(t, s)

	if got, want := strings.TrimSuffix(loc.String(), "?"+loc.RawQuery), idp.srv.URL+"/auth"; !strings.HasPrefix(got, want) {
		t.Errorf("redirect target = %s, want prefix %s", got, want)
	}
	q := loc.Query()
	if q.Get("state") == "" || q.Get("nonce") == "" || q.Get("code_challenge") == "" {
		t.Errorf("missing state/nonce/code_challenge in %s", loc.RawQuery)
	}
	if q.Get("code_challenge_method") != "S256" {
		t.Errorf("code_challenge_method = %q, want S256", q.Get("code_challenge_method"))
	}
	if q.Get("client_id") != "fragments" {
		t.Errorf("client_id = %q", q.Get("client_id"))
	}
	if q.Get("redirect_uri") != "http://app.test"+oidcCallbackPath {
		t.Errorf("redirect_uri = %q", q.Get("redirect_uri"))
	}
	// The cookie must decode back to the same state/nonce the IdP was sent.
	st, ok := s.oidcAuth.decodeState(stateCk.Value)
	if !ok {
		t.Fatalf("state cookie does not decode/verify")
	}
	if st.State != q.Get("state") || st.Nonce != q.Get("nonce") {
		t.Errorf("cookie state/nonce do not match redirect params")
	}
}

func TestOIDCCallbackHappyPath(t *testing.T) {
	idp := newFakeIdP(t)
	s := newOIDCTestServer(t, idp)

	stateCk, loc := startFlow(t, s)
	q := loc.Query()
	idp.mu.Lock()
	idp.nonce = q.Get("nonce")
	idp.mu.Unlock()

	w := callback(t, s, "code=test-code&state="+url.QueryEscape(q.Get("state")), stateCk)
	if w.Code != http.StatusFound || w.Result().Header.Get("Location") != "/" {
		t.Fatalf("callback: got %d → %q, want 302 → / (body: %s)", w.Code, w.Result().Header.Get("Location"), w.Body.String())
	}
	sess := findSessionCookie(w)
	if sess == nil {
		t.Fatal("no session cookie issued")
	}
	if !s.auth.validate(sess.Value) {
		t.Error("issued session cookie does not validate")
	}
	// PKCE round-trip: the verifier sent to the token endpoint must hash to the
	// challenge from the authorization redirect.
	idp.mu.Lock()
	verifier := idp.lastVerifier
	idp.mu.Unlock()
	if verifier == "" {
		t.Fatal("token endpoint did not receive a code_verifier")
	}
	sum := sha256.Sum256([]byte(verifier))
	if got, want := base64.RawURLEncoding.EncodeToString(sum[:]), q.Get("code_challenge"); got != want {
		t.Errorf("S256(code_verifier) = %s, want %s", got, want)
	}
}

func TestOIDCCallbackRejections(t *testing.T) {
	idp := newFakeIdP(t)
	s := newOIDCTestServer(t, idp)

	assertRejected := func(t *testing.T, w *httptest.ResponseRecorder, wantErr string) {
		t.Helper()
		if w.Code != http.StatusFound {
			t.Fatalf("got %d, want 302", w.Code)
		}
		if loc := w.Result().Header.Get("Location"); loc != "/login?error="+wantErr {
			t.Errorf("redirect = %q, want /login?error=%s", loc, wantErr)
		}
		if findSessionCookie(w) != nil {
			t.Error("session cookie issued on a failed login")
		}
	}

	t.Run("provider error", func(t *testing.T) {
		w := callback(t, s, "error=access_denied")
		assertRejected(t, w, "access_denied")
	})

	t.Run("missing state cookie", func(t *testing.T) {
		w := callback(t, s, "code=x&state=whatever")
		assertRejected(t, w, "oidc_failed")
	})

	t.Run("state mismatch", func(t *testing.T) {
		stateCk, _ := startFlow(t, s)
		w := callback(t, s, "code=x&state=not-the-state", stateCk)
		assertRejected(t, w, "oidc_failed")
	})

	t.Run("expired state", func(t *testing.T) {
		st := oidcState{State: "s", Nonce: "n", CV: "v", Exp: time.Now().Add(-time.Minute).Unix()}
		ck := &http.Cookie{Name: oidcStateCookie, Value: s.oidcAuth.encodeState(st)}
		w := callback(t, s, "code=x&state=s", ck)
		assertRejected(t, w, "oidc_failed")
	})

	t.Run("tampered state cookie", func(t *testing.T) {
		st := oidcState{State: "s", Nonce: "n", CV: "v", Exp: time.Now().Add(time.Minute).Unix()}
		ck := &http.Cookie{Name: oidcStateCookie, Value: s.oidcAuth.encodeState(st) + "x"}
		w := callback(t, s, "code=x&state=s", ck)
		assertRejected(t, w, "oidc_failed")
	})

	t.Run("nonce mismatch", func(t *testing.T) {
		stateCk, loc := startFlow(t, s)
		idp.mu.Lock()
		idp.nonce = "some-other-nonce"
		idp.mu.Unlock()
		w := callback(t, s, "code=x&state="+url.QueryEscape(loc.Query().Get("state")), stateCk)
		assertRejected(t, w, "oidc_failed")
	})

	t.Run("wrong audience", func(t *testing.T) {
		stateCk, loc := startFlow(t, s)
		idp.mu.Lock()
		idp.nonce = loc.Query().Get("nonce")
		idp.aud = "not-fragments"
		idp.mu.Unlock()
		defer func() {
			idp.mu.Lock()
			idp.aud = "fragments"
			idp.mu.Unlock()
		}()
		w := callback(t, s, "code=x&state="+url.QueryEscape(loc.Query().Get("state")), stateCk)
		assertRejected(t, w, "oidc_failed")
	})
}

func TestOIDCModeGating(t *testing.T) {
	idp := newFakeIdP(t)
	s := newOIDCTestServer(t, idp)

	t.Run("password login disabled", func(t *testing.T) {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader(`{"password":"x"}`))
		req.Header.Set("Content-Type", "application/json")
		s.engine.ServeHTTP(w, req)
		if w.Code != http.StatusNotFound {
			t.Errorf("POST /api/login = %d, want 404", w.Code)
		}
	})

	t.Run("auth config reports oidc", func(t *testing.T) {
		w := httptest.NewRecorder()
		s.engine.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/auth/config", nil))
		var body struct {
			Mode         string `json:"mode"`
			ProviderName string `json:"providerName"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatalf("bad JSON: %v", err)
		}
		if body.Mode != "oidc" || body.ProviderName != "Pocket ID" {
			t.Errorf("got %+v, want mode=oidc providerName=Pocket ID", body)
		}
	})
}

func TestPasswordModeGating(t *testing.T) {
	cfg := Config{
		Addr:       "127.0.0.1:0",
		Password:   "hunter2",
		Secret:     testSecret,
		SessionTTL: time.Hour,
		ThumbDir:   t.TempDir(),
	}
	s := New(cfg, &catalog.Config{ThumbDir: cfg.ThumbDir}, nil, nil, nil)

	t.Run("oidc start absent", func(t *testing.T) {
		w := httptest.NewRecorder()
		s.engine.ServeHTTP(w, httptest.NewRequest(http.MethodGet, oidcStartPath, nil))
		if w.Code != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404", oidcStartPath, w.Code)
		}
	})

	t.Run("auth config reports password", func(t *testing.T) {
		w := httptest.NewRecorder()
		s.engine.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/auth/config", nil))
		var body struct {
			Mode string `json:"mode"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatalf("bad JSON: %v", err)
		}
		if body.Mode != "password" {
			t.Errorf("mode = %q, want password", body.Mode)
		}
	})

	t.Run("password login works", func(t *testing.T) {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader(`{"password":"hunter2"}`))
		req.Header.Set("Content-Type", "application/json")
		s.engine.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("POST /api/login = %d, want 200 (body: %s)", w.Code, w.Body.String())
		}
		if findSessionCookie(w) == nil {
			t.Error("no session cookie issued")
		}
	})
}
