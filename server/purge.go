package server

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"fragments/worker"
)

// handleDiscardedSummary returns the aggregate discard pile (count, S3 object
// count, bytes, album membership) for the gallery action bar and the purge
// confirmation dialog. GET /api/discarded/summary — not under /api/photos/…
// because the GET /api/photos/*keyBase wildcard forbids static siblings in
// gin's router.
func (s *Server) handleDiscardedSummary(c *gin.Context) {
	sum, err := s.store.DiscardedSummary()
	if err != nil {
		s.log.Printf("discarded summary: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to summarize discarded photos"})
		return
	}
	c.JSON(http.StatusOK, sum)
}

// handlePurgeDiscarded permanently erases every photo marked 'discard' (S3
// originals incl. RAW, thumbnails, catalog rows). POST /api/photos/purge-discarded
// with {"expectedCount": N} — the count the client displayed; the server
// recomputes the list itself and refuses with 409 if the real count differs.
// 409 also when any run is active. Returns 202 + the initial snapshot; progress
// then streams over /api/events with phase "purging".
func (s *Server) handlePurgeDiscarded(c *gin.Context) {
	var req struct {
		ExpectedCount int `json:"expectedCount"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}
	if req.ExpectedCount <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "expectedCount must be positive"})
		return
	}
	if err := s.catCfg.Validate(); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "purge needs S3 to be configured: " + err.Error()})
		return
	}

	snap, err := s.coord.StartPurge(req.ExpectedCount)
	switch {
	case errors.Is(err, worker.ErrRunActive), errors.Is(err, worker.ErrPurgeCountMismatch):
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
	case err != nil:
		s.log.Printf("start purge: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to start purge"})
	default:
		s.log.Printf("purge started (%d photos)", req.ExpectedCount)
		c.JSON(http.StatusAccepted, snap)
	}
}
