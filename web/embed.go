// Package web embeds the built Vue single-page app (web/dist) into the binary so
// `fragments serve` ships as one executable. In development the SPA is served by
// Vite on :5173 (which proxies /api and /thumbs to this server) and these
// embedded files are ignored.
package web

import (
	"embed"
	"io/fs"
)

// distFS holds the Vite build output. The `all:` prefix also embeds files whose
// names start with '_' or '.' (Vite emits none today, but it is future-proof).
//
//go:embed all:dist
var distFS embed.FS

// Dist returns the SPA file system rooted at dist/ (so "index.html" resolves
// directly). Until the first `vite build`, dist/ holds only a placeholder.
func Dist() (fs.FS, error) {
	return fs.Sub(distFS, "dist")
}
