// Package worker runs the catalog pipeline as a managed pool of concurrent
// workers with live status, on top of the existing (sequential) catalog package.
// It reuses catalog.Cataloger.ProcessNoStore for the per-photo work and funnels
// every database write through a single goroutine, so SQLite only ever sees one
// writer. The CLI path stays sequential and untouched.
package worker

import "time"

// WorkerStatus is one worker slot's live state.
type WorkerStatus struct {
	ID          int    `json:"id"`
	Busy        bool   `json:"busy"`
	KeyBase     string `json:"keyBase"`     // current item ("" when idle)
	LastKeyBase string `json:"lastKeyBase"` // last item finished (persists; for a preview thumbnail)
}

// ItemError records a single failed capture.
type ItemError struct {
	KeyBase string `json:"keyBase"`
	Err     string `json:"err"`
}

// Snapshot is the full, self-contained status of the coordinator at one instant.
// The SSE stream and GET /api/status both return this; the frontend replaces its
// state wholesale (no delta merging).
type Snapshot struct {
	Active         bool           `json:"active"`
	Phase          string         `json:"phase"` // idle | listing | running | done | cancelled | error
	Total          int            `json:"total"`
	Processed      int            `json:"processed"`
	Skipped        int            `json:"skipped"`
	Failed         int            `json:"failed"`
	StartedAt      *time.Time     `json:"startedAt"`
	ElapsedSec     float64        `json:"elapsedSec"`
	Rate           float64        `json:"rate"`   // completed photos per second (run average)
	ETASec         float64        `json:"etaSec"` // estimated seconds remaining
	Workers        []WorkerStatus `json:"workers"`
	LastError      string         `json:"lastError"`
	Errors         []ItemError    `json:"errors"`         // most recent failures (capped)
	DefaultWorkers int            `json:"defaultWorkers"` // effective default concurrency (for the UI's pre-filled input)
}

// RunOptions parameterizes one catalog run (decoded from POST /api/run).
type RunOptions struct {
	Prefix  string `json:"prefix"`  // S3 key prefix (S3 mode)
	Limit   int    `json:"limit"`   // process at most N (0 = all)
	Force   bool   `json:"force"`   // reprocess even if unchanged
	Workers int    `json:"workers"` // concurrency (0 = default)
	Local   string `json:"local"`   // catalog this local dir instead of S3 (testing/offline)
}
