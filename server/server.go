// Package server is the Gin HTTP layer for fragments: it serves the embedded Vue
// SPA, the thumbnail files, and a small JSON API over the catalog Store. Auth is
// a single shared password exchanged for a signed, stateless session cookie.
package server

import (
	"context"
	"errors"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"fragments/catalog"
	"fragments/worker"
)

// Server wires the config, the catalog store, the worker coordinator, and the
// Gin engine together.
type Server struct {
	cfg    Config
	catCfg *catalog.Config // S3 + paths, for album export
	store  *catalog.Store
	coord  *worker.Coordinator
	log    *log.Logger
	auth   *authenticator
	engine *gin.Engine
	done   chan struct{} // closed at shutdown to release long-lived SSE handlers
}

// New builds a Server. logger may be nil (logging is then discarded).
func New(cfg Config, catCfg *catalog.Config, store *catalog.Store, coord *worker.Coordinator, logger *log.Logger) *Server {
	if logger == nil {
		logger = log.New(io.Discard, "", 0)
	}
	gin.SetMode(gin.ReleaseMode)
	s := &Server{
		cfg:    cfg,
		catCfg: catCfg,
		store:  store,
		coord:  coord,
		log:    logger,
		auth:   newAuthenticator(cfg),
		done:   make(chan struct{}),
	}
	s.engine = s.buildRouter()
	return s
}

// buildRouter registers every route. Public endpoints come first; everything
// under /api (except login/health) and /thumbs requires a valid session.
func (s *Server) buildRouter() *gin.Engine {
	r := gin.New()
	// Trust only the configured proxies (nil/empty → trust none, so c.ClientIP()
	// is the real peer and can't be spoofed via X-Forwarded-For — the login
	// rate-limiter keys on it). Behind Caddy/nginx, set FRAGMENTS_TRUSTED_PROXIES
	// to the proxy address so the forwarded client IP is honored. Entries are
	// validated in LoadConfig; this error is a belt-and-braces log.
	if err := r.SetTrustedProxies(s.cfg.TrustedProxies); err != nil {
		s.log.Printf("warning: invalid FRAGMENTS_TRUSTED_PROXIES: %v", err)
	}
	r.Use(gin.Recovery(), s.requestLogger(), limitRequestBody(1<<20))

	// Public.
	r.POST("/api/login", s.handleLogin)
	r.GET("/api/health", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"status": "ok"}) })

	// Authenticated JSON API.
	api := r.Group("/api", s.requireAuth())
	// Logout sits behind requireAuth so a cross-site POST can't force-clear a
	// victim's session (the SPA ignores a logout failure and clears local state).
	api.POST("/logout", s.handleLogout)
	api.GET("/me", s.handleMe)
	api.GET("/photos", s.handlePhotos)
	api.GET("/photos/*keyBase", s.handlePhotoDetail)
	api.PATCH("/photos/*keyBase", s.handlePatchPhoto) // rating / decision (CSRF)

	// Albums (mutations CSRF-protected).
	api.GET("/albums", s.handleListAlbums)
	api.POST("/albums", s.handleCreateAlbum)
	api.GET("/albums/:id", s.handleGetAlbum)
	api.DELETE("/albums/:id", s.handleDeleteAlbum)
	api.POST("/albums/:id/photos", s.handleAddAlbumPhoto)
	api.DELETE("/albums/:id/photos/*keyBase", s.handleRemoveAlbumPhoto)
	api.PATCH("/albums/:id/order", s.handleReorderAlbum)
	api.GET("/albums/:id/export", s.handleExportAlbum) // streams a zip of S3 originals

	// Worker pool: run control (POST → CSRF-protected) + live status (GET).
	api.POST("/run", s.handleRun)
	api.POST("/run/cancel", s.handleRunCancel)
	api.GET("/status", s.handleStatus)
	api.GET("/events", s.handleEvents)

	// Thumbnails from disk (authenticated), with long cache headers.
	r.GET("/thumbs/*path", s.requireAuth(), s.thumbHandler())

	// SPA static assets + client-side routing fallback.
	s.mountSPA(r)
	return r
}

// Run starts the HTTP server and blocks until ctx is cancelled, then shuts down
// gracefully.
func (s *Server) Run(ctx context.Context) error {
	hs := &http.Server{
		Addr:              s.cfg.Addr,
		Handler:           s.engine,
		ReadHeaderTimeout: 10 * time.Second,
	}

	errc := make(chan error, 1)
	go func() {
		s.log.Printf("fragments serving on %s (secure-cookies=%v)", s.cfg.Addr, s.cfg.Secure)
		if err := hs.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errc <- err
		}
	}()

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
		s.log.Printf("shutting down...")
		// Release long-lived SSE handlers so their connections go idle and
		// http.Shutdown can return promptly (it waits for in-flight requests but
		// never cancels their contexts).
		close(s.done)
		// Stop any active catalog run; its single writer goroutine finishes the
		// in-flight write so the DB is left consistent before serve.go closes it.
		s.coord.Cancel()

		shutCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		err := hs.Shutdown(shutCtx)

		// Drain the worker writer on its OWN budget (Shutdown may have consumed
		// shutCtx) before serve.go's deferred store.Close() runs.
		drainCtx, dcancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer dcancel()
		s.coord.WaitIdle(drainCtx)
		return err
	}
}

// limitRequestBody caps every request body: JSON binding buffers the body in
// memory, so without a cap an unauthenticated POST /api/login with a
// multi-gigabyte body is a trivial memory-exhaustion DoS. No legitimate payload
// here comes anywhere near the limit.
func limitRequestBody(n int64) gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.Body != nil {
			c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, n)
		}
		c.Next()
	}
}

// requestLogger logs one concise line per request, skipping the noisy thumbnail
// and SSE paths.
func (s *Server) requestLogger() gin.HandlerFunc {
	return func(c *gin.Context) {
		path := c.Request.URL.Path
		if strings.HasPrefix(path, "/thumbs/") || path == "/api/events" {
			c.Next()
			return
		}
		start := time.Now()
		c.Next()
		s.log.Printf("%s %s %d %s", c.Request.Method, path, c.Writer.Status(), time.Since(start).Round(time.Millisecond))
	}
}
