package server

import (
	"archive/zip"
	"context"
	"fmt"
	"io"
	"net/http"
	"path"
	"strings"

	"github.com/gin-gonic/gin"

	"fragments/catalog"
)

// handleExportAlbum streams a ZIP of an album's original files pulled from S3,
// in membership order. GET /api/albums/:id/export?raw=true also includes the RAF
// siblings. Requires S3 to be configured (the gallery itself does not).
//
// Entries are skipped (and logged) on a per-object fetch error so a single bad
// object doesn't abort the whole archive; the produced zip stays valid.
func (s *Server) handleExportAlbum(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}
	includeRAF := c.Query("raw") == "true" || c.Query("raw") == "1"

	name, items, found, err := s.store.AlbumExportItems(id)
	if err != nil {
		s.log.Printf("export album: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to read album"})
		return
	}
	if !found {
		c.JSON(http.StatusNotFound, gin.H{"error": "album not found"})
		return
	}
	if len(items) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "album is empty"})
		return
	}
	if err := s.catCfg.Validate(); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "export needs S3 to be configured: " + err.Error()})
		return
	}

	ctx := c.Request.Context()
	bucket, err := catalog.NewBucket(ctx, s.catCfg)
	if err != nil {
		s.log.Printf("export album: bucket: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to open bucket"})
		return
	}

	c.Header("Content-Type", "application/zip")
	c.Header("Content-Disposition", `attachment; filename="`+sanitizeFilename(name)+`.zip"`)

	zw := zip.NewWriter(c.Writer)
	defer zw.Close()

	for _, it := range items {
		if ctx.Err() != nil { // client disconnected
			return
		}
		if err := addObjectToZip(ctx, zw, bucket, it.JPEGKey); err != nil {
			s.log.Printf("export: skipping %s: %v", it.JPEGKey, err)
		}
		if includeRAF && it.RAFKey != "" {
			if err := addObjectToZip(ctx, zw, bucket, it.RAFKey); err != nil {
				s.log.Printf("export: skipping %s: %v", it.RAFKey, err)
			}
		}
	}
}

// addObjectToZip streams one S3 object into a zip entry named by its key
// (preserving the folder structure). Streaming keeps memory flat even for RAW
// files of 100+ MB.
func addObjectToZip(ctx context.Context, zw *zip.Writer, bucket *catalog.Bucket, key string) error {
	name := zipEntryName(key)
	if name == "" {
		return fmt.Errorf("unsafe zip entry name %q", key)
	}
	body, err := bucket.OpenObject(ctx, key)
	if err != nil {
		return err
	}
	defer body.Close()
	w, err := zw.Create(name)
	if err != nil {
		return err
	}
	_, err = io.Copy(w, body)
	return err
}

// zipEntryName normalizes an S3 key into a safe relative archive path — no
// absolute prefix, backslashes, or ".." elements that a naive extractor would
// write outside its target directory. Returns "" if nothing safe remains.
func zipEntryName(key string) string {
	name := strings.ReplaceAll(key, `\`, "/")
	name = path.Clean("/" + name)[1:] // rooting first makes ".." collapse harmless
	if name == "" || name == "." {
		return ""
	}
	return name
}

// sanitizeFilename makes an album name safe for a Content-Disposition filename.
func sanitizeFilename(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "album"
	}
	repl := func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			return r
		case r == '-' || r == '_' || r == ' ':
			return r
		default:
			return '_'
		}
	}
	return strings.Map(repl, name)
}
