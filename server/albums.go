package server

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"fragments/catalog"
)

// handlePatchPhoto updates the user-owned rating and/or decision of one capture.
// PATCH /api/photos/*keyBase  {rating?: 0..5, decision?: "discard"|"none"}
// rating>0 and decision='discard' are mutually exclusive (the store clears the
// other), so a rating "keeps" and the skull "rejects".
func (s *Server) handlePatchPhoto(c *gin.Context) {
	keyBase := strings.TrimPrefix(c.Param("keyBase"), "/")
	if keyBase == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing key"})
		return
	}
	var body struct {
		Rating   *int    `json:"rating"`
		Decision *string `json:"decision"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}
	if body.Rating == nil && body.Decision == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "nothing to update"})
		return
	}

	// Client mistakes (sentinel errors) map to 400; anything else is an internal
	// store failure that must not leak driver detail to the client.
	found := true
	if body.Rating != nil {
		ok, err := s.store.SetRating(keyBase, *body.Rating)
		switch {
		case errors.Is(err, catalog.ErrInvalidRating):
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		case err != nil:
			s.log.Printf("patch photo %s: %v", keyBase, err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "update failed"})
			return
		}
		found = found && ok
	}
	if body.Decision != nil {
		ok, err := s.store.SetDecision(keyBase, *body.Decision)
		switch {
		case errors.Is(err, catalog.ErrInvalidDecision):
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		case err != nil:
			s.log.Printf("patch photo %s: %v", keyBase, err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "update failed"})
			return
		}
		found = found && ok
	}
	if !found {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	c.Status(http.StatusNoContent)
}

// handleListAlbums: GET /api/albums
func (s *Server) handleListAlbums(c *gin.Context) {
	albums, err := s.store.ListAlbums()
	if err != nil {
		s.log.Printf("list albums: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list albums"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"albums": albums})
}

// handleCreateAlbum: POST /api/albums {name}
func (s *Server) handleCreateAlbum(c *gin.Context) {
	var body struct {
		Name string `json:"name"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}
	name := strings.TrimSpace(body.Name)
	if name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "album name is required"})
		return
	}
	album, err := s.store.CreateAlbum(name)
	if err != nil {
		s.log.Printf("create album: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create album"})
		return
	}
	c.JSON(http.StatusCreated, album)
}

// handleGetAlbum: GET /api/albums/:id → album + ordered photos
func (s *Server) handleGetAlbum(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}
	album, photos, err := s.store.GetAlbum(id)
	if err != nil {
		s.log.Printf("get album: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load album"})
		return
	}
	if album == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "album not found"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"album": album, "items": photos})
}

// handleDeleteAlbum: DELETE /api/albums/:id
func (s *Server) handleDeleteAlbum(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}
	deleted, err := s.store.DeleteAlbum(id)
	if err != nil {
		s.log.Printf("delete album: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete album"})
		return
	}
	if !deleted {
		c.JSON(http.StatusNotFound, gin.H{"error": "album not found"})
		return
	}
	c.Status(http.StatusNoContent)
}

// handleAddAlbumPhoto: POST /api/albums/:id/photos {keyBase}
func (s *Server) handleAddAlbumPhoto(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}
	var body struct {
		KeyBase string `json:"keyBase"`
	}
	if err := c.ShouldBindJSON(&body); err != nil || strings.TrimSpace(body.KeyBase) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "keyBase is required"})
		return
	}
	exists, err := s.store.AlbumExists(id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed"})
		return
	}
	if !exists {
		c.JSON(http.StatusNotFound, gin.H{"error": "album not found"})
		return
	}
	added, err := s.store.AddPhotoToAlbum(id, body.KeyBase)
	if err != nil {
		if errors.Is(err, catalog.ErrPhotoNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "photo not found"})
			return
		}
		s.log.Printf("add album photo: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to add photo"})
		return
	}
	// added=false means the photo was already a member (idempotent no-op).
	c.JSON(http.StatusOK, gin.H{"added": added})
}

// handleRemoveAlbumPhoto: DELETE /api/albums/:id/photos/*keyBase
func (s *Server) handleRemoveAlbumPhoto(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}
	keyBase := strings.TrimPrefix(c.Param("keyBase"), "/")
	if keyBase == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing key"})
		return
	}
	removed, err := s.store.RemovePhotoFromAlbum(id, keyBase)
	if err != nil {
		s.log.Printf("remove album photo: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to remove photo"})
		return
	}
	if !removed {
		c.JSON(http.StatusNotFound, gin.H{"error": "not in album"})
		return
	}
	c.Status(http.StatusNoContent)
}

// handleReorderAlbum: PATCH /api/albums/:id/order {keyBases: [...]}
func (s *Server) handleReorderAlbum(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}
	var body struct {
		KeyBases []string `json:"keyBases"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}
	if err := s.store.ReorderAlbum(id, body.KeyBases); err != nil {
		s.log.Printf("reorder album: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to reorder"})
		return
	}
	c.Status(http.StatusNoContent)
}

// parseID extracts and validates the :id path param, writing a 400 on failure.
func parseID(c *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return 0, false
	}
	return id, true
}
