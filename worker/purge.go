package worker

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"fragments/catalog"
)

// ErrPurgeCountMismatch is returned by StartPurge when the number of discarded
// photos no longer matches what the client displayed: another tab changed a
// decision between display and confirmation, so nothing is deleted.
var ErrPurgeCountMismatch = errors.New("discard count changed")

// purgeBatchSize is how many photos each purge round erases. At most 2 S3 keys
// per photo keeps every DeleteObjects call under the S3 1000-key cap, and each
// DB delete transaction stays short on the single SQLite connection.
const purgeBatchSize = 200

// StartPurge begins permanently erasing every photo marked decision='discard'
// (S3 originals, then thumbnail, then catalog row), returning the initial
// snapshot. The discard list is recomputed here, server-side; expected is the
// count the client displayed and any mismatch aborts with
// ErrPurgeCountMismatch before anything is deleted. A purge shares the
// coordinator's single-run lock with catalog runs — ErrRunActive if either is
// underway — which also preserves the one-SQLite-writer discipline.
func (c *Coordinator) StartPurge(expected int) (Snapshot, error) {
	c.mu.Lock()
	if c.running {
		s := c.cloneLocked()
		c.mu.Unlock()
		return s, ErrRunActive
	}
	photos, err := c.store.ListDiscarded()
	if err != nil {
		s := c.cloneLocked()
		c.mu.Unlock()
		return s, err
	}
	if len(photos) != expected {
		s := c.cloneLocked()
		c.mu.Unlock()
		return s, fmt.Errorf("%w: expected %d, found %d", ErrPurgeCountMismatch, expected, len(photos))
	}

	ctx, cancel := context.WithCancel(context.Background())
	now := time.Now()
	c.running = true
	c.cancel = cancel
	c.snap = Snapshot{
		Active: true, Phase: "purging", Total: len(photos), StartedAt: &now,
		Workers: []WorkerStatus{}, Errors: []ItemError{},
		DefaultWorkers: c.defWorkers,
	}
	s := c.cloneLocked()
	c.mu.Unlock()

	go c.purge(ctx, photos)
	return s, nil
}

// purge erases photos in batches, most destructive step last for each photo:
// S3 objects, then thumbnail, then DB row. A photo whose S3 deletion fails
// keeps its thumbnail and row — still marked 'discard', it is retried by the
// next purge — so a failure always leaves a replayable state rather than an
// orphaned row.
func (c *Coordinator) purge(ctx context.Context, photos []catalog.DiscardedPhoto) {
	events := make(chan event, 256)
	statusDone := make(chan struct{})
	go c.statusLoop(events, statusDone)

	// Emit the phase through the event pipeline (as run() does for "running")
	// so statusLoop broadcasts it immediately: without this, a purge finishing
	// before the first 200ms tick would never show 'purging' to SSE clients,
	// and the SPA — which reacts to the purging→done transition — would
	// neither invalidate the gallery nor toast the outcome.
	events <- event{kind: evPhase, phase: "purging"}

	finish := func(phase string, fatal error) {
		if fatal != nil {
			c.mu.Lock()
			c.snap.LastError = fatal.Error()
			c.mu.Unlock()
		}
		events <- event{kind: evPhase, phase: phase}
		close(events)
		<-statusDone

		c.mu.Lock()
		c.snap.Active = false
		c.running = false
		c.cancel = nil
		p, sp, f, tot, freed := c.snap.Processed, c.snap.Skipped, c.snap.Failed, c.snap.Total, c.snap.BytesFreed
		c.mu.Unlock()

		c.broadcast()
		c.logf("purge %s: %d erased, %d spared, %d failed (of %d), %d bytes freed", phase, p, sp, f, tot, freed)
	}

	bucket, err := c.cat.OpenBucket(ctx)
	if err != nil {
		finish("error", err)
		return
	}

	for start := 0; start < len(photos); start += purgeBatchSize {
		if ctx.Err() != nil {
			finish("cancelled", nil)
			return
		}
		batch := photos[start:min(start+purgeBatchSize, len(photos))]

		// The list was snapshotted at StartPurge, but un-rejecting from the
		// lightbox (PATCH decision) stays possible while the purge runs — and
		// it is THE documented way to spare a photo. Re-check each batch right
		// before touching S3 so a photo un-rejected mid-run survives as long as
		// its batch hasn't been reached; spared photos count as skipped.
		kbs := make([]string, len(batch))
		for i, p := range batch {
			kbs[i] = p.KeyBase
		}
		still, err := c.store.StillDiscarded(kbs)
		if err != nil {
			for _, p := range batch {
				events <- event{kind: evItemFailed, keyBase: p.KeyBase, errMsg: "store: " + err.Error()}
			}
			continue
		}
		live := make([]catalog.DiscardedPhoto, 0, len(batch))
		for _, p := range batch {
			if !still[p.KeyBase] {
				events <- event{kind: evSkip, keyBase: p.KeyBase}
				continue
			}
			live = append(live, p)
		}
		if len(live) == 0 {
			continue
		}

		// 1. S3 originals: JPEG + RAW sibling when present. An object already
		// gone from the bucket is a success (idempotent delete).
		keys := make([]string, 0, len(live)*2)
		for _, p := range live {
			keys = append(keys, p.JPEGKey)
			if p.RAFKey != "" {
				keys = append(keys, p.RAFKey)
			}
		}
		delErrs, err := bucket.DeleteObjects(ctx, keys)
		if err != nil {
			if ctx.Err() != nil {
				finish("cancelled", nil)
				return
			}
			// The whole call failed (network, auth): every photo in this batch
			// stays catalogued and rejected, replayable at the next purge.
			for _, p := range live {
				events <- event{kind: evItemFailed, keyBase: p.KeyBase, errMsg: "s3: " + err.Error()}
			}
			continue
		}
		failedKeys := make(map[string]string, len(delErrs))
		for _, de := range delErrs {
			failedKeys[de.Key] = de.Message
		}

		survivorsGone := make([]catalog.DiscardedPhoto, 0, len(live))
		for _, p := range live {
			if msg, ok := failedKeys[p.JPEGKey]; ok {
				events <- event{kind: evItemFailed, keyBase: p.KeyBase, errMsg: "s3 " + p.JPEGKey + ": " + msg}
				continue
			}
			if p.RAFKey != "" {
				if msg, ok := failedKeys[p.RAFKey]; ok {
					events <- event{kind: evItemFailed, keyBase: p.KeyBase, errMsg: "s3 " + p.RAFKey + ": " + msg}
					continue
				}
			}
			survivorsGone = append(survivorsGone, p)
		}
		if len(survivorsGone) == 0 {
			continue
		}

		// 2. Thumbnails. A missing file is not an error (idempotent); any other
		// failure is logged but never blocks the row deletion — the originals
		// are already gone, keeping the row would orphan it forever.
		for _, p := range survivorsGone {
			c.removeThumb(c.cat.ThumbPath(p.KeyBase))
			if p.ThumbPath != "" {
				c.removeThumb(p.ThumbPath)
			}
		}

		// 3. Catalog rows, cascading album membership. Never the cancellable
		// ctx: the S3 objects of this batch are gone, the rows must go too.
		keyBases := make([]string, len(survivorsGone))
		for i, p := range survivorsGone {
			keyBases[i] = p.KeyBase
		}
		if err := c.store.DeletePhotos(keyBases); err != nil {
			for _, p := range survivorsGone {
				events <- event{kind: evItemFailed, keyBase: p.KeyBase, errMsg: "store: " + err.Error()}
			}
			continue
		}
		for _, p := range survivorsGone {
			events <- event{kind: evItemDone, keyBase: p.KeyBase, bytes: p.JPEGSize + p.RAFSize}
		}
	}

	phase := "done"
	if ctx.Err() != nil {
		phase = "cancelled"
	}
	finish(phase, nil)
}

// removeThumb deletes one thumbnail file, treating "already absent" as success.
func (c *Coordinator) removeThumb(path string) {
	if path == "" {
		return
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		c.logf("purge: remove thumb %s: %v", path, err)
	}
}
