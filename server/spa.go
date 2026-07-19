package server

import (
	"io/fs"
	"net/http"
	"path"
	"strings"

	"github.com/gin-gonic/gin"

	"fragments/web"
)

// mountSPA serves the embedded Vue build: real files (JS/CSS/assets) are served
// as-is, and any other GET path falls back to index.html so the client-side
// router (vue-router history mode) can take over. API and thumbnail paths are
// never rewritten.
func (s *Server) mountSPA(r *gin.Engine) {
	dist, err := web.Dist()
	if err != nil {
		s.log.Printf("spa: cannot open embedded dist: %v", err)
		return
	}
	fileServer := http.FileServer(http.FS(dist))
	index, err := fs.ReadFile(dist, "index.html")
	if err != nil {
		s.log.Printf("spa: missing index.html in embedded dist: %v", err)
		return
	}

	r.NoRoute(func(c *gin.Context) {
		p := c.Request.URL.Path
		if c.Request.Method != http.MethodGet ||
			strings.HasPrefix(p, "/api/") || strings.HasPrefix(p, "/thumbs/") {
			c.Status(http.StatusNotFound)
			return
		}
		// Serve a real static asset when one exists at this path.
		if name := strings.TrimPrefix(p, "/"); name != "" {
			if f, ferr := dist.Open(name); ferr == nil {
				_ = f.Close()
				fileServer.ServeHTTP(c.Writer, c.Request)
				return
			}
		}
		// A path that looks like a file (has an extension) but wasn't found is a
		// missing asset, not a client route — 404 it so the browser never tries
		// to execute index.html as JS/CSS (e.g. a stale hashed chunk after a
		// redeploy). Client routes (vue-router history mode) are extensionless.
		if strings.Contains(path.Base(p), ".") {
			c.Status(http.StatusNotFound)
			return
		}
		// Otherwise hand back the SPA shell for client-side routing.
		c.Data(http.StatusOK, "text/html; charset=utf-8", index)
	})
}
