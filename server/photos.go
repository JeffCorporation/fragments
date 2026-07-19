package server

import (
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"fragments/catalog"
)

// handlePhotos returns one keyset page of gallery items.
// GET /api/photos?cursor=&limit=&minRating=&decision=&folder=&film=&camera=
func (s *Server) handlePhotos(c *gin.Context) {
	filter := catalog.PhotoFilter{
		Folder:         c.Query("folder"),
		FilmSimulation: c.Query("film"),
		CameraModel:    c.Query("camera"),
		Decision:       c.Query("decision"),
	}
	if v := c.Query("minRating"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			filter.MinRating = n
		}
	}
	limit := 0
	if v := c.Query("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			limit = n
		}
	}

	page, err := s.store.ListPhotos(filter, c.Query("cursor"), limit)
	if err != nil {
		// A bad cursor is the only client-caused error here.
		if strings.Contains(err.Error(), "cursor") {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid cursor"})
			return
		}
		s.log.Printf("list photos: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list photos"})
		return
	}
	c.JSON(http.StatusOK, page)
}

// handlePhotoDetail returns the full detail (incl. raw EXIF) for one capture.
// GET /api/photos/*keyBase  (the wildcard keeps the '/' inside key_base intact)
func (s *Server) handlePhotoDetail(c *gin.Context) {
	keyBase := strings.TrimPrefix(c.Param("keyBase"), "/")
	if keyBase == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing key"})
		return
	}
	d, err := s.store.GetPhoto(keyBase)
	if err != nil {
		s.log.Printf("get photo %s: %v", keyBase, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load photo"})
		return
	}
	if d == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	c.JSON(http.StatusOK, d)
}

// thumbHandler serves thumbnail files from the data/thumbs directory. http
// .FileServer handles path traversal and range/conditional requests. We wrap it
// to (a) refuse directory listings (noDirFS) and (b) attach the long
// Cache-Control only to successful (2xx) responses, so a 404 for a not-yet-
// generated thumbnail is never cached as "missing" for a day.
func (s *Server) thumbHandler() gin.HandlerFunc {
	fileServer := http.StripPrefix("/thumbs/", http.FileServer(noDirFS{http.Dir(s.cfg.ThumbDir)}))
	return gin.WrapH(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fileServer.ServeHTTP(&cacheOn2xx{ResponseWriter: w}, r)
	}))
}

// noDirFS is an http.FileSystem that hides directories: opening one returns
// os.ErrNotExist, so http.FileServer never emits an auto-generated index that
// would enumerate every folder and photo key.
type noDirFS struct{ http.Dir }

func (fs noDirFS) Open(name string) (http.File, error) {
	f, err := fs.Dir.Open(name)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	if info.IsDir() {
		f.Close()
		return nil, os.ErrNotExist
	}
	return f, nil
}

// cacheOn2xx sets the long Cache-Control header only when the wrapped handler
// writes a 2xx status (so 301/404 responses stay uncached).
type cacheOn2xx struct {
	http.ResponseWriter
	wrote bool
}

func (c *cacheOn2xx) WriteHeader(code int) {
	if code >= 200 && code < 300 {
		c.Header().Set("Cache-Control", "public, max-age=86400")
	}
	c.wrote = true
	c.ResponseWriter.WriteHeader(code)
}

func (c *cacheOn2xx) Write(b []byte) (int, error) {
	if !c.wrote {
		c.WriteHeader(http.StatusOK)
	}
	return c.ResponseWriter.Write(b)
}
