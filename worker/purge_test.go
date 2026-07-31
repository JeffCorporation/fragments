package worker

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"fragments/catalog"
)

// stubS3 answers DeleteObjects like an S3-compatible endpoint (path-style),
// recording the received keys and reporting the configured ones as failed.
// When gate is non-nil the first call signals firstCall then blocks until gate
// is closed, letting a test act between two purge batches deterministically.
type stubS3 struct {
	mu        sync.Mutex
	received  []string
	calls     int
	failKeys  map[string]bool
	gate      chan struct{}
	firstCall chan struct{}
}

func (s *stubS3) handler(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || !r.URL.Query().Has("delete") {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL)
			http.Error(w, "unexpected", http.StatusBadRequest)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read delete body: %v", err)
		}
		var req struct {
			Objects []struct {
				Key string `xml:"Key"`
			} `xml:"Object"`
		}
		if err := xml.Unmarshal(body, &req); err != nil {
			t.Errorf("parse delete body: %v", err)
		}

		s.mu.Lock()
		first := s.calls == 0
		s.mu.Unlock()
		if first && s.gate != nil {
			close(s.firstCall)
			<-s.gate
		}

		out := `<?xml version="1.0" encoding="UTF-8"?><DeleteResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/">`
		s.mu.Lock()
		s.calls++
		for _, o := range req.Objects {
			s.received = append(s.received, o.Key)
			if s.failKeys[o.Key] {
				out += fmt.Sprintf(`<Error><Key>%s</Key><Code>InternalError</Code><Message>boom</Message></Error>`, o.Key)
			}
		}
		s.mu.Unlock()
		out += `</DeleteResult>`
		w.Header().Set("Content-Type", "application/xml")
		_, _ = w.Write([]byte(out))
	}
}

// newPurgeEnv builds a real store + cataloger over t.TempDir() and a coordinator
// whose bucket points at the stub.
func newPurgeEnv(t *testing.T, stub *stubS3) (*Coordinator, *catalog.Store, *catalog.Config) {
	t.Helper()
	ts := httptest.NewServer(stub.handler(t))
	t.Cleanup(ts.Close)

	dir := t.TempDir()
	cfg := &catalog.Config{
		AccessKeyID: "test", SecretAccessKey: "test", Bucket: "bucket",
		Region: "test", Endpoint: ts.URL, UsePathStyle: true,
		DataDir: dir, DBPath: filepath.Join(dir, "catalog.db"),
		ThumbDir: filepath.Join(dir, "thumbs"),
	}
	store, err := catalog.OpenStore(cfg.DBPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	coord := NewCoordinator(store, catalog.NewCataloger(cfg, store), NewHub(), t.Logf, 1)
	return coord, store, cfg
}

// addPhoto inserts one catalogued photo (with an on-disk thumbnail) and
// optionally marks it discarded.
func addPhoto(t *testing.T, store *catalog.Store, cfg *catalog.Config, keyBase string, rafSize int64, discard bool) {
	t.Helper()
	p := &catalog.Photo{
		KeyBase: keyBase, Folder: "F", Name: filepath.Base(keyBase),
		JPEG: catalog.ObjectRef{Key: keyBase + ".JPG", Size: 1000, ETag: "e-" + keyBase},
	}
	if rafSize > 0 {
		p.RAF = &catalog.ObjectRef{Key: keyBase + ".RAF", Size: rafSize, ETag: "r-" + keyBase}
	}
	thumb := filepath.Join(cfg.ThumbDir, filepath.FromSlash(keyBase)+".jpg")
	if err := os.MkdirAll(filepath.Dir(thumb), 0o755); err != nil {
		t.Fatalf("mkdir thumb: %v", err)
	}
	if err := os.WriteFile(thumb, []byte("x"), 0o644); err != nil {
		t.Fatalf("write thumb: %v", err)
	}
	p.ThumbPath = thumb
	if err := store.Upsert(p, time.Now()); err != nil {
		t.Fatalf("upsert %s: %v", keyBase, err)
	}
	if discard {
		if _, err := store.SetDecision(keyBase, "discard"); err != nil {
			t.Fatalf("discard %s: %v", keyBase, err)
		}
	}
}

func waitIdle(t *testing.T, coord *Coordinator) Snapshot {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	coord.WaitIdle(ctx)
	if ctx.Err() != nil {
		t.Fatal("purge did not finish in time")
	}
	return coord.Snapshot()
}

func thumbExists(cfg *catalog.Config, keyBase string) bool {
	_, err := os.Stat(filepath.Join(cfg.ThumbDir, filepath.FromSlash(keyBase)+".jpg"))
	return err == nil
}

func TestStartPurgeCountMismatch(t *testing.T) {
	stub := &stubS3{}
	coord, store, cfg := newPurgeEnv(t, stub)
	addPhoto(t, store, cfg, "F/A", 3000, true)
	addPhoto(t, store, cfg, "F/B", 0, true)

	_, err := coord.StartPurge(3)
	if !errors.Is(err, ErrPurgeCountMismatch) {
		t.Fatalf("want ErrPurgeCountMismatch, got %v", err)
	}
	if n, _ := store.Count(); n != 2 {
		t.Fatalf("mismatch must delete nothing, %d rows left", n)
	}
	if stub.calls != 0 {
		t.Fatalf("mismatch must not touch S3, got %d calls", stub.calls)
	}
}

func TestPurgeErasesDiscarded(t *testing.T) {
	stub := &stubS3{}
	coord, store, cfg := newPurgeEnv(t, stub)
	addPhoto(t, store, cfg, "F/A", 3000, true) // JPEG + RAF
	addPhoto(t, store, cfg, "F/B", 0, true)    // JPEG only
	addPhoto(t, store, cfg, "F/C", 0, false)   // kept

	// A discarded photo sitting in an album must go too (cascade).
	album, err := store.CreateAlbum("trip")
	if err != nil {
		t.Fatalf("create album: %v", err)
	}
	for _, kb := range []string{"F/A", "F/C"} {
		if _, err := store.AddPhotoToAlbum(album.ID, kb); err != nil {
			t.Fatalf("add %s to album: %v", kb, err)
		}
	}
	if sum, err := store.DiscardedSummary(); err != nil || sum.Count != 2 || sum.Objects != 3 || sum.Bytes != 5000 || sum.InAlbums != 1 {
		t.Fatalf("summary = %+v, err %v; want count 2, objects 3, bytes 5000, inAlbums 1", sum, err)
	}

	if _, err := coord.StartPurge(2); err != nil {
		t.Fatalf("start purge: %v", err)
	}
	snap := waitIdle(t, coord)

	if snap.Phase != "done" || snap.Active {
		t.Fatalf("snapshot = %+v; want phase done, inactive", snap)
	}
	if snap.Processed != 2 || snap.Failed != 0 || snap.Total != 2 {
		t.Fatalf("counters = %+v; want 2 processed, 0 failed", snap)
	}
	if snap.BytesFreed != 5000 {
		t.Fatalf("bytesFreed = %d; want 5000", snap.BytesFreed)
	}
	if n, _ := store.Count(); n != 1 {
		t.Fatalf("%d rows left; want 1 (the kept photo)", n)
	}
	if thumbExists(cfg, "F/A") || thumbExists(cfg, "F/B") {
		t.Fatal("purged thumbnails must be removed")
	}
	if !thumbExists(cfg, "F/C") {
		t.Fatal("kept photo's thumbnail must survive")
	}
	if _, items, err := store.GetAlbum(album.ID); err != nil || len(items) != 1 || items[0].KeyBase != "F/C" {
		t.Fatalf("album items = %v (err %v); want only F/C", items, err)
	}
	stub.mu.Lock()
	defer stub.mu.Unlock()
	if len(stub.received) != 3 {
		t.Fatalf("S3 got %d keys (%v); want 3", len(stub.received), stub.received)
	}
}

// TestPurgeSparesPhotoUnrejectedMidRun pins the per-batch re-check: a photo
// un-rejected (lightbox X) while an earlier batch is being erased must survive.
// The stub blocks batch 1's DeleteObjects until the test has un-rejected a
// batch-2 photo, making the interleaving deterministic.
func TestPurgeSparesPhotoUnrejectedMidRun(t *testing.T) {
	stub := &stubS3{gate: make(chan struct{}), firstCall: make(chan struct{})}
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(stub.gate) }) }
	defer release() // never leave the stub blocked if an assertion bails early

	coord, store, cfg := newPurgeEnv(t, stub)
	// purgeBatchSize+1 photos → two batches; the last key_base lands in batch 2.
	total := purgeBatchSize + 1
	for i := 0; i < total; i++ {
		addPhoto(t, store, cfg, fmt.Sprintf("F/P%03d", i), 0, true)
	}
	spared := fmt.Sprintf("F/P%03d", total-1)

	if _, err := coord.StartPurge(total); err != nil {
		t.Fatalf("start purge: %v", err)
	}
	select {
	case <-stub.firstCall:
	case <-time.After(10 * time.Second):
		t.Fatal("purge never reached S3")
	}
	// Batch 1 is inside its S3 call: keep the last photo after all.
	if _, err := store.SetDecision(spared, "none"); err != nil {
		t.Fatalf("un-reject %s: %v", spared, err)
	}
	release()
	snap := waitIdle(t, coord)

	if snap.Processed != total-1 || snap.Skipped != 1 || snap.Failed != 0 {
		t.Fatalf("counters = %+v; want %d processed, 1 skipped", snap, total-1)
	}
	if n, _ := store.Count(); n != 1 {
		t.Fatalf("%d rows left; want 1 (the spared photo)", n)
	}
	if !thumbExists(cfg, spared) {
		t.Fatal("spared photo's thumbnail must survive")
	}
	stub.mu.Lock()
	defer stub.mu.Unlock()
	for _, k := range stub.received {
		if k == spared+".JPG" {
			t.Fatal("spared photo's S3 key must never be sent for deletion")
		}
	}
}

func TestPurgeKeepsPhotoWhenS3DeleteFails(t *testing.T) {
	stub := &stubS3{failKeys: map[string]bool{"F/A.RAF": true}}
	coord, store, cfg := newPurgeEnv(t, stub)
	addPhoto(t, store, cfg, "F/A", 3000, true)
	addPhoto(t, store, cfg, "F/B", 0, true)

	if _, err := coord.StartPurge(2); err != nil {
		t.Fatalf("start purge: %v", err)
	}
	snap := waitIdle(t, coord)

	if snap.Processed != 1 || snap.Failed != 1 {
		t.Fatalf("counters = %+v; want 1 processed, 1 failed", snap)
	}
	if snap.BytesFreed != 1000 {
		t.Fatalf("bytesFreed = %d; want 1000 (only F/B)", snap.BytesFreed)
	}
	// The failed photo keeps its row (still discarded) and its thumbnail: the
	// next purge retries it.
	if n, _ := store.Count(); n != 1 {
		t.Fatalf("%d rows left; want 1 (the failed photo)", n)
	}
	if !thumbExists(cfg, "F/A") {
		t.Fatal("failed photo's thumbnail must survive")
	}
	if thumbExists(cfg, "F/B") {
		t.Fatal("purged photo's thumbnail must be removed")
	}
	sum, err := store.DiscardedSummary()
	if err != nil || sum.Count != 1 {
		t.Fatalf("summary after purge = %+v (err %v); want the failed photo still discarded", sum, err)
	}
}
