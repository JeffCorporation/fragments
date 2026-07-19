package server

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

const sessionCookie = "fragments_session"

// authenticator signs/validates stateless session cookies and derives the CSRF
// token bound to a session. The cookie value is "<payload>.<sig>" where payload
// is the expiry (unix seconds) and sig = HMAC(secret, payload). No server-side
// session state is kept; "logout everywhere" is done by rotating FRAGMENTS_SECRET.
type authenticator struct {
	password []byte
	secret   []byte
	secure   bool
	ttl      time.Duration
	limiter  *loginLimiter
}

func newAuthenticator(cfg Config) *authenticator {
	return &authenticator{
		password: []byte(cfg.Password),
		secret:   cfg.Secret,
		secure:   cfg.Secure,
		ttl:      cfg.SessionTTL,
		limiter:  newLoginLimiter(),
	}
}

func (a *authenticator) sign(payload string) string {
	m := hmac.New(sha256.New, a.secret)
	m.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

// issue mints a session token valid until exp.
func (a *authenticator) issue(exp time.Time) string {
	payload := strconv.FormatInt(exp.Unix(), 10)
	return base64.RawURLEncoding.EncodeToString([]byte(payload)) + "." + a.sign(payload)
}

// validate returns true if token has a valid signature and has not expired.
func (a *authenticator) validate(token string) bool {
	dot := strings.IndexByte(token, '.')
	if dot < 0 {
		return false
	}
	payloadB64, sig := token[:dot], token[dot+1:]
	pb, err := base64.RawURLEncoding.DecodeString(payloadB64)
	if err != nil {
		return false
	}
	payload := string(pb)
	if subtle.ConstantTimeCompare([]byte(sig), []byte(a.sign(payload))) != 1 {
		return false
	}
	exp, err := strconv.ParseInt(payload, 10, 64)
	if err != nil {
		return false
	}
	return time.Now().Unix() < exp
}

// csrfFor derives the CSRF token bound to a session token (double-submit).
func (a *authenticator) csrfFor(sessionToken string) string {
	m := hmac.New(sha256.New, a.secret)
	m.Write([]byte("csrf|" + sessionToken))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

// requireAuth aborts with 401 if there is no valid session, and with 403 if a
// state-changing request is missing/!matching its CSRF token.
func (s *Server) requireAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		token, err := c.Cookie(sessionCookie)
		if err != nil || !s.auth.validate(token) {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
			return
		}
		switch c.Request.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			// safe methods: no CSRF check
		default:
			got := c.GetHeader("X-CSRF-Token")
			want := s.auth.csrfFor(token)
			if subtle.ConstantTimeCompare([]byte(got), []byte(want)) != 1 {
				c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "invalid csrf token"})
				return
			}
		}
		c.Next()
	}
}

func (s *Server) handleLogin(c *gin.Context) {
	// attempt() both checks the cooldown AND records the attempt in one critical
	// section, so a burst of concurrent requests can't all slip past the backoff
	// before any of them registers (a successful login clears the counter).
	ip := c.ClientIP()
	if !s.auth.limiter.attempt(ip) {
		c.JSON(http.StatusTooManyRequests, gin.H{"error": "too many attempts, please wait"})
		return
	}

	var body struct {
		Password string `json:"password"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}

	// Constant-time compare; ConstantTimeCompare returns 0 on length mismatch.
	ok := len(s.auth.password) > 0 &&
		subtle.ConstantTimeCompare([]byte(body.Password), s.auth.password) == 1
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid password"})
		return
	}
	s.auth.limiter.reset(ip)

	exp := time.Now().Add(s.auth.ttl)
	token := s.auth.issue(exp)
	s.setSessionCookie(c, token, exp)
	c.JSON(http.StatusOK, gin.H{"csrf": s.auth.csrfFor(token)})
}

func (s *Server) handleLogout(c *gin.Context) {
	http.SetCookie(c.Writer, &http.Cookie{
		Name: sessionCookie, Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, Secure: s.auth.secure, SameSite: http.SameSiteLaxMode,
	})
	c.Status(http.StatusNoContent)
}

// handleMe confirms the session and returns a fresh CSRF token (so a page reload
// can re-prime its client without re-login).
func (s *Server) handleMe(c *gin.Context) {
	token, _ := c.Cookie(sessionCookie)
	c.JSON(http.StatusOK, gin.H{"csrf": s.auth.csrfFor(token)})
}

func (s *Server) setSessionCookie(c *gin.Context, token string, exp time.Time) {
	http.SetCookie(c.Writer, &http.Cookie{
		Name: sessionCookie, Value: token, Path: "/", Expires: exp,
		HttpOnly: true, Secure: s.auth.secure, SameSite: http.SameSiteLaxMode,
	})
}

// loginLimiter is a tiny in-memory per-IP backoff: after 5 attempts without a
// success a cooldown kicks in and grows with each further attempt, capped at
// one minute.
type loginLimiter struct {
	mu sync.Mutex
	m  map[string]*attempt
}

type attempt struct {
	fails int
	until time.Time
}

// maxLimiterEntries caps the per-IP map; above it, entries not currently in
// cooldown are swept so a flood of distinct IPs can't grow it without bound.
const maxLimiterEntries = 10000

func newLoginLimiter() *loginLimiter { return &loginLimiter{m: map[string]*attempt{}} }

// attempt reports whether ip may try to log in, recording the attempt in the
// same critical section. Check-then-record used to be two separate calls, which
// let N concurrent requests all pass the check before any recorded a failure —
// atomically counting on entry closes that race. A successful login must call
// reset(ip) so legitimate users never accumulate a cooldown.
func (l *loginLimiter) attempt(ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	// Bound memory: when the map grows large, drop every entry no longer in
	// cooldown (safe to forget); only actively locked-out IPs are retained.
	if len(l.m) > maxLimiterEntries {
		for k, v := range l.m {
			if now.After(v.until) {
				delete(l.m, k)
			}
		}
	}
	a := l.m[ip]
	if a == nil {
		a = &attempt{}
		l.m[ip] = a
	}
	if now.Before(a.until) {
		return false
	}
	a.fails++
	if a.fails >= 5 {
		d := time.Duration(a.fails-4) * 2 * time.Second
		if d > time.Minute {
			d = time.Minute
		}
		a.until = now.Add(d)
	}
	return true
}

func (l *loginLimiter) reset(ip string) {
	l.mu.Lock()
	delete(l.m, ip)
	l.mu.Unlock()
}
