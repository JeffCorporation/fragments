package server

import (
	"archive/zip"
	"io"
	"net/http"
	"path"
	"strconv"

	"github.com/gin-gonic/gin"

	"fragments/catalog"
)

// handleDownloadPhoto streams one photo's original file(s) from S3 to the
// client. GET /api/photos/*keyBase/download?kind=jpeg|raw|both — dispatched
// from handlePhotoDetail because Gin cannot register a route segment after a
// wildcard. The bytes transit through the server (no presigned URL), so the
// session stays the only door to the bucket and any S3 provider works. Being a
// GET that mutates nothing, the route is exempt from the CSRF check by design.
func (s *Server) handleDownloadPhoto(c *gin.Context, keyBase string) {
	kind := c.Query("kind")
	switch kind {
	case "jpeg", "raw", "both":
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": "unknown kind"})
		return
	}

	d, err := s.store.GetPhoto(keyBase)
	if err != nil {
		s.log.Printf("download %s: %v", keyBase, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load photo"})
		return
	}
	if d == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	if kind != "jpeg" && d.RAFKey == "" {
		// "both" without a RAW would have nothing extra to zip; the UI greys
		// both entries out in that case anyway.
		c.JSON(http.StatusNotFound, gin.H{"error": "no RAW for this photo"})
		return
	}

	if err := s.catCfg.Validate(); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "download needs S3 to be configured: " + err.Error()})
		return
	}
	ctx := c.Request.Context()
	bucket, err := catalog.NewBucket(ctx, s.catCfg)
	if err != nil {
		s.log.Printf("download %s: bucket: %v", keyBase, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to open bucket"})
		return
	}

	// "<taken date>_<name>" when the capture date is known, plain name
	// otherwise. The extension is taken from the S3 key so it keeps its
	// original case (".JPG", ".RAF", ...).
	base := d.Name
	if d.TakenAt != nil {
		base = d.TakenAt.Format("2006-01-02") + "_" + base
	}
	base = sanitizeFilename(base)

	if kind == "both" {
		// The zip size is not known up front, so no Content-Length here.
		// Failed entries are logged and skipped so the archive stays valid,
		// same as handleExportAlbum. Entries carry the same "<date>_<name>"
		// base as the single-file downloads, flat (no folder prefix).
		c.Header("Content-Type", "application/zip")
		c.Header("Content-Disposition", `attachment; filename="`+base+`.zip"`)
		zw := zip.NewWriter(c.Writer)
		defer zw.Close()
		if err := addObjectToZipAs(ctx, zw, bucket, d.JPEGKey, base+path.Ext(d.JPEGKey)); err != nil {
			s.log.Printf("download: skipping %s: %v", d.JPEGKey, err)
		}
		if ctx.Err() != nil { // client disconnected
			return
		}
		if err := addObjectToZipAs(ctx, zw, bucket, d.RAFKey, base+path.Ext(d.RAFKey)); err != nil {
			s.log.Printf("download: skipping %s: %v", d.RAFKey, err)
		}
		return
	}

	key, size, contentType := d.JPEGKey, d.JPEGSize, "image/jpeg"
	if kind == "raw" {
		key, size, contentType = d.RAFKey, d.RAFSize, "application/octet-stream"
	}
	body, err := bucket.OpenObject(ctx, key)
	if err != nil {
		// No header has been written yet, so a JSON error is still possible.
		s.log.Printf("download %s: open %s: %v", keyBase, key, err)
		c.JSON(http.StatusBadGateway, gin.H{"error": "failed to open object"})
		return
	}
	defer body.Close()

	c.Header("Content-Type", contentType)
	c.Header("Content-Length", strconv.FormatInt(size, 10))
	c.Header("Content-Disposition", `attachment; filename="`+base+path.Ext(key)+`"`)
	c.Status(http.StatusOK)
	if _, err := io.Copy(c.Writer, body); err != nil && ctx.Err() == nil {
		// The headers are already gone: the status can't change and the
		// browser ends up with a truncated file (assumed by the spec). All
		// that's left to do is log it — unless the client itself went away.
		s.log.Printf("download %s: copy %s: %v", keyBase, key, err)
	}
}
