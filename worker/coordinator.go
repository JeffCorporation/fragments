package worker

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"fragments/catalog"
)

// ErrRunActive is returned by Start when a run is already in progress.
var ErrRunActive = errors.New("a catalog run is already in progress")

const (
	// DefaultWorkers is the pool size when neither the API request nor the
	// FRAGMENTS_WORKERS env var specifies one.
	DefaultWorkers = 2
	maxWorkers     = 32
	maxErrorsKept  = 50
)

// Coordinator owns the single allowed catalog run and the live status. Exactly
// one run may be active at a time. All status mutation happens in one goroutine
// (statusLoop) and all DB writes in another (the writer goroutine), so there are
// no shared-counter races and SQLite sees a single writer.
type Coordinator struct {
	store      *catalog.Store
	cat        *catalog.Cataloger
	hub        *Hub
	logf       func(string, ...any)
	defWorkers int // effective default concurrency, surfaced in the snapshot

	mu      sync.Mutex // guards running, cancel, snap
	running bool
	cancel  context.CancelFunc
	snap    Snapshot
}

func NewCoordinator(store *catalog.Store, cat *catalog.Cataloger, hub *Hub, logf func(string, ...any), defaultWorkers int) *Coordinator {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	if defaultWorkers <= 0 {
		defaultWorkers = DefaultWorkers
	}
	return &Coordinator{
		store: store, cat: cat, hub: hub, logf: logf, defWorkers: defaultWorkers,
		snap: Snapshot{Phase: "idle", Workers: []WorkerStatus{}, Errors: []ItemError{}, DefaultWorkers: defaultWorkers},
	}
}

// Hub exposes the SSE hub for the HTTP layer.
func (c *Coordinator) Hub() *Hub { return c.hub }

// Snapshot returns a deep copy of the current status (safe for concurrent HTTP
// reads).
func (c *Coordinator) Snapshot() Snapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cloneLocked()
}

func (c *Coordinator) cloneLocked() Snapshot {
	s := c.snap
	// Use make(...,0,len) (not append to a nil slice) so empty slices stay
	// non-nil and marshal as [] rather than null — the SPA reads .length on them.
	s.Workers = append(make([]WorkerStatus, 0, len(c.snap.Workers)), c.snap.Workers...)
	s.Errors = append(make([]ItemError, 0, len(c.snap.Errors)), c.snap.Errors...)
	return s
}

// Start begins a run, returning the initial snapshot. It returns ErrRunActive
// (with the in-progress snapshot) if a run is already underway.
func (c *Coordinator) Start(opts RunOptions) (Snapshot, error) {
	c.mu.Lock()
	if c.running {
		s := c.cloneLocked()
		c.mu.Unlock()
		return s, ErrRunActive
	}
	n := opts.Workers
	if n <= 0 {
		n = c.defWorkers
	}
	if n > maxWorkers {
		n = maxWorkers
	}
	ctx, cancel := context.WithCancel(context.Background())
	now := time.Now()
	c.running = true
	c.cancel = cancel
	c.snap = Snapshot{
		Active: true, Phase: "listing", StartedAt: &now,
		Workers: make([]WorkerStatus, n), Errors: []ItemError{},
		DefaultWorkers: c.defWorkers,
	}
	for i := range c.snap.Workers {
		c.snap.Workers[i].ID = i
	}
	s := c.cloneLocked()
	c.mu.Unlock()

	go c.run(ctx, opts, n)
	return s, nil
}

// Cancel requests cancellation of the active run (no-op if idle). In-flight
// fetches abort; already-completed photos are still written so the DB stays
// consistent, and a re-run resumes for free via ETag idempotency.
func (c *Coordinator) Cancel() {
	c.mu.Lock()
	if c.cancel != nil {
		c.cancel()
	}
	c.mu.Unlock()
}

// WaitIdle blocks until no run is active or ctx is done. Used on graceful
// shutdown (after Cancel) so the writer goroutine drains before the DB closes.
func (c *Coordinator) WaitIdle(ctx context.Context) {
	for {
		c.mu.Lock()
		running := c.running
		c.mu.Unlock()
		if !running {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// ---- internal event plumbing (all consumed by the single statusLoop) ----

type evKind int

const (
	evPhase evKind = iota
	evTotal
	evWorkerStart
	evWorkerIdle
	evItemDone
	evItemFailed
	evSkip
)

type event struct {
	kind     evKind
	workerID int
	keyBase  string
	errMsg   string
	phase    string
	total    int
}

// run executes one catalog run end to end.
func (c *Coordinator) run(ctx context.Context, opts RunOptions, nWorkers int) {
	events := make(chan event, 256)
	statusDone := make(chan struct{})
	go c.statusLoop(events, statusDone)

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
		for i := range c.snap.Workers {
			c.snap.Workers[i].Busy = false
			c.snap.Workers[i].KeyBase = ""
		}
		p, sk, f, tot := c.snap.Processed, c.snap.Skipped, c.snap.Failed, c.snap.Total
		c.mu.Unlock()

		c.broadcast()
		c.logf("run %s: %d processed, %d skipped, %d failed (of %d)", phase, p, sk, f, tot)
	}

	// 1. List the source (S3 or a local dir for offline/testing).
	var (
		photos []catalog.Photo
		fetch  catalog.FetchFunc
		err    error
	)
	if opts.Local != "" {
		photos, fetch, err = c.cat.LocalSource(opts.Local)
	} else {
		photos, fetch, err = c.cat.S3Source(ctx, opts.Prefix)
	}
	if err != nil {
		if ctx.Err() != nil {
			// The user cancelled while the listing was paginating: present it as
			// a normal cancellation, not a failure.
			finish("cancelled", nil)
		} else {
			finish("error", err)
		}
		return
	}
	if opts.Limit > 0 && len(photos) > opts.Limit {
		photos = photos[:opts.Limit]
	}
	events <- event{kind: evTotal, total: len(photos)}
	events <- event{kind: evPhase, phase: "running"}

	jobs := make(chan *catalog.Photo, nWorkers*2)
	results := make(chan *catalog.Photo, nWorkers*2)

	// 2. Writer goroutine: the SOLE database writer. It never uses the run's
	// (cancellable) context, so a cancel can't abort a half-done write — the DB
	// is always left consistent.
	writerDone := make(chan struct{})
	go func() {
		defer close(writerDone)
		for p := range results {
			if err := c.store.Upsert(p, time.Now()); err != nil {
				events <- event{kind: evItemFailed, keyBase: p.KeyBase, errMsg: "store: " + err.Error()}
			} else {
				events <- event{kind: evItemDone, keyBase: p.KeyBase}
			}
		}
	}()

	// 3. Worker goroutines: storeless per-photo work, handing the populated
	// photo to the writer. On cancel, stop pulling new work and let the fetch
	// abort.
	var wg sync.WaitGroup
	for i := 0; i < nWorkers; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for p := range jobs {
				if ctx.Err() != nil {
					break
				}
				events <- event{kind: evWorkerStart, workerID: id, keyBase: p.KeyBase}
				if err := c.cat.ProcessNoStore(ctx, p, fetch); err != nil {
					if ctx.Err() != nil {
						break // cancelled mid-item: don't count it, a re-run redoes it
					}
					events <- event{kind: evItemFailed, workerID: id, keyBase: p.KeyBase, errMsg: err.Error()}
					continue
				}
				results <- p
			}
			events <- event{kind: evWorkerIdle, workerID: id}
		}(i)
	}

	// 4. Producer: list order, apply the same ETag idempotency as the CLI, emit
	// skips, and feed the rest to the workers (with backpressure). On cancel the
	// workers can exit while the producer is still inside a skip-check, so run()
	// must wait for producerDone before closing events (a send would panic).
	producerDone := make(chan struct{})
	go func() {
		defer close(producerDone)
		defer close(jobs)
		for i := range photos {
			if ctx.Err() != nil {
				return
			}
			p := &photos[i]
			skip, err := c.cat.ShouldSkip(p, opts.Force)
			if err != nil {
				events <- event{kind: evItemFailed, keyBase: p.KeyBase, errMsg: "skip-check: " + err.Error()}
				continue
			}
			if skip {
				events <- event{kind: evSkip, keyBase: p.KeyBase}
				continue
			}
			select {
			case jobs <- p:
			case <-ctx.Done():
				return
			}
		}
	}()

	wg.Wait()      // all workers done (jobs drained or cancelled)
	close(results) // no more writes will be sent
	<-writerDone   // writer drained and exited
	<-producerDone // producer exited too — safe to close events in finish()

	phase := "done"
	if ctx.Err() != nil {
		phase = "cancelled"
	}
	finish(phase, nil)
}

// statusLoop is the only goroutine that mutates snap counters/workers. It
// coalesces updates and broadcasts a fresh snapshot at most ~5x/second, plus
// immediately on phase changes; while a run is active it also ticks the clock.
func (c *Coordinator) statusLoop(events <-chan event, done chan<- struct{}) {
	defer close(done)
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	dirty := false

	for {
		select {
		case ev, ok := <-events:
			if !ok {
				c.broadcast()
				return
			}
			if c.applyEvent(ev) { // phase change → broadcast now
				c.broadcast()
				dirty = false
			} else {
				dirty = true
			}
		case <-ticker.C:
			c.mu.Lock()
			c.recomputeLocked()
			active := c.snap.Active
			c.mu.Unlock()
			if active || dirty {
				c.broadcast()
				dirty = false
			}
		}
	}
}

// applyEvent mutates the snapshot for one event and returns true on a phase
// change. Runs only in statusLoop; takes c.mu only to stay safe against HTTP
// readers.
func (c *Coordinator) applyEvent(ev event) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	phaseChange := false
	switch ev.kind {
	case evPhase:
		c.snap.Phase = ev.phase
		phaseChange = true
	case evTotal:
		c.snap.Total = ev.total
	case evWorkerStart:
		if ev.workerID < len(c.snap.Workers) {
			w := &c.snap.Workers[ev.workerID]
			// Starting the next item means the previous one was handed off (its
			// thumbnail is on disk), so it becomes this worker's last preview.
			if w.KeyBase != "" {
				w.LastKeyBase = w.KeyBase
			}
			w.Busy = true
			w.KeyBase = ev.keyBase
		}
	case evWorkerIdle:
		if ev.workerID < len(c.snap.Workers) {
			w := &c.snap.Workers[ev.workerID]
			if w.KeyBase != "" {
				w.LastKeyBase = w.KeyBase
			}
			w.Busy = false
			w.KeyBase = ""
		}
	case evItemDone:
		c.snap.Processed++
	case evItemFailed:
		c.snap.Failed++
		c.snap.LastError = ev.keyBase + ": " + ev.errMsg
		c.snap.Errors = append(c.snap.Errors, ItemError{KeyBase: ev.keyBase, Err: ev.errMsg})
		if len(c.snap.Errors) > maxErrorsKept {
			c.snap.Errors = c.snap.Errors[len(c.snap.Errors)-maxErrorsKept:]
		}
	case evSkip:
		c.snap.Skipped++
	}
	c.recomputeLocked()
	return phaseChange
}

// recomputeLocked refreshes elapsed/rate/ETA. Caller holds c.mu.
func (c *Coordinator) recomputeLocked() {
	if c.snap.StartedAt == nil {
		return
	}
	elapsed := time.Since(*c.snap.StartedAt).Seconds()
	c.snap.ElapsedSec = elapsed
	done := c.snap.Processed + c.snap.Skipped + c.snap.Failed
	if elapsed > 0 {
		c.snap.Rate = float64(done) / elapsed
	}
	if remaining := c.snap.Total - done; c.snap.Rate > 0 && remaining > 0 {
		c.snap.ETASec = float64(remaining) / c.snap.Rate
	} else {
		c.snap.ETASec = 0
	}
}

func (c *Coordinator) broadcast() {
	c.mu.Lock()
	s := c.cloneLocked()
	c.mu.Unlock()
	b, err := json.Marshal(s)
	if err != nil {
		return
	}
	c.hub.Broadcast(SSEMessage("status", b))
}
