package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"fragments/worker"
)

// handleRun starts a catalog run. POST /api/run with an optional JSON body
// (RunOptions). Returns 202 + the initial snapshot, or 409 + the in-progress
// snapshot if a run is already active.
func (s *Server) handleRun(c *gin.Context) {
	var opts worker.RunOptions
	if c.Request.ContentLength != 0 {
		if err := c.ShouldBindJSON(&opts); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
			return
		}
	}
	if opts.Workers <= 0 {
		opts.Workers = s.cfg.Workers // 0 → the coordinator's own default
	}

	snap, err := s.coord.Start(opts)
	if errors.Is(err, worker.ErrRunActive) {
		c.JSON(http.StatusConflict, snap)
		return
	}
	s.log.Printf("catalog run started (workers=%d prefix=%q local=%q limit=%d force=%v)",
		opts.Workers, opts.Prefix, opts.Local, opts.Limit, opts.Force)
	c.JSON(http.StatusAccepted, snap)
}

// handleRunCancel cancels the active run (no-op if idle).
func (s *Server) handleRunCancel(c *gin.Context) {
	s.coord.Cancel()
	c.Status(http.StatusNoContent)
}

// handleStatus returns the current run snapshot (polling fallback for SSE).
func (s *Server) handleStatus(c *gin.Context) {
	c.JSON(http.StatusOK, s.coord.Snapshot())
}

// handleEvents streams live status as Server-Sent Events: the current snapshot
// on connect, then a "status" event on each change, plus a 15s heartbeat.
func (s *Server) handleEvents(c *gin.Context) {
	w := c.Writer
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("Connection", "keep-alive")
	h.Set("X-Accel-Buffering", "no") // disable proxy buffering (nginx/Caddy)
	w.WriteHeader(http.StatusOK)
	w.Flush()

	// Subscribe BEFORE reading the initial snapshot. A run's terminal
	// (done/cancelled) broadcast is one-shot — once idle, nothing re-broadcasts —
	// so if it fired in the gap between an initial snapshot read and a later
	// Subscribe(), this client would miss it and stay pinned on "running".
	// Subscribing first means any such broadcast is buffered in ch and delivered
	// by the loop below; a duplicate full snapshot is harmless (applied wholesale).
	ch := s.coord.Hub().Subscribe()
	defer s.coord.Hub().Unsubscribe(ch)

	if b, err := json.Marshal(s.coord.Snapshot()); err == nil {
		_, _ = w.Write(worker.SSEMessage("status", b))
		w.Flush()
	}

	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	ctx := c.Request.Context()

	for {
		select {
		case <-ctx.Done(): // client disconnected
			return
		case <-s.done: // server shutting down — release the connection promptly
			return
		case msg, ok := <-ch:
			if !ok {
				return
			}
			if _, err := w.Write(msg); err != nil {
				return
			}
			w.Flush()
		case <-heartbeat.C:
			if _, err := w.Write([]byte(": ping\n\n")); err != nil {
				return
			}
			w.Flush()
		}
	}
}
