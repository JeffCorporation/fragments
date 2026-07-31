package catalog

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func newTestCataloger(t *testing.T) (*Cataloger, *Store, *Config) {
	t.Helper()
	dir := t.TempDir()
	cfg := &Config{
		DataDir: dir, DBPath: filepath.Join(dir, "catalog.db"),
		ThumbDir: filepath.Join(dir, "thumbs"),
	}
	store, err := OpenStore(cfg.DBPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	cat := NewCataloger(cfg, store)
	cat.Logf = t.Logf
	return cat, store, cfg
}

// addRow catalogs one photo with a thumbnail file on disk. etag "" defaults to
// a bucket-shaped ETag; pass a "local-…" one to simulate a local-dir scan row.
func addRow(t *testing.T, cat *Cataloger, store *Store, keyBase, etag string) {
	t.Helper()
	if etag == "" {
		etag = "e-" + keyBase
	}
	p := &Photo{
		KeyBase: keyBase, Folder: filepath.Dir(keyBase), Name: filepath.Base(keyBase),
		JPEG: ObjectRef{Key: keyBase + ".JPG", Size: 1000, ETag: etag},
	}
	thumb := cat.thumbPath(keyBase)
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
}

// presenceOf builds the key-base set S3Source would return for a listing.
func presenceOf(keyBases ...string) map[string]struct{} {
	present := make(map[string]struct{}, len(keyBases))
	for _, kb := range keyBases {
		present[kb] = struct{}{}
	}
	return present
}

func rowThumbExists(cat *Cataloger, keyBase string) bool {
	_, err := os.Stat(cat.thumbPath(keyBase))
	return err == nil
}

func TestReconcileMissingScopedToPrefix(t *testing.T) {
	cat, store, _ := newTestCataloger(t)
	addRow(t, cat, store, "F/A", "")
	addRow(t, cat, store, "F/B", "")
	addRow(t, cat, store, "G/C", "")

	// Listing scoped to F/: B is gone from the bucket, C is out of scope.
	removed, err := cat.ReconcileMissing(presenceOf("F/A"), "F/")
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if removed != 1 {
		t.Fatalf("removed = %d; want 1 (F/B only)", removed)
	}
	if n, _ := store.Count(); n != 2 {
		t.Fatalf("%d rows left; want 2 (F/A and out-of-scope G/C)", n)
	}
	if rowThumbExists(cat, "F/B") {
		t.Fatal("removed row's thumbnail must be deleted")
	}
	if !rowThumbExists(cat, "F/A") || !rowThumbExists(cat, "G/C") {
		t.Fatal("kept rows must keep their thumbnails")
	}

	// Unscoped listing: now G/C is in scope and gone.
	removed, err = cat.ReconcileMissing(presenceOf("F/A"), "")
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if removed != 1 {
		t.Fatalf("removed = %d; want 1 (G/C)", removed)
	}
	if n, _ := store.Count(); n != 1 {
		t.Fatalf("%d rows left; want 1 (F/A)", n)
	}
}

func TestReconcileMissingEmptyListingIsNoop(t *testing.T) {
	cat, store, _ := newTestCataloger(t)
	addRow(t, cat, store, "F/A", "")

	removed, err := cat.ReconcileMissing(nil, "")
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if removed != 0 {
		t.Fatalf("removed = %d; an empty listing must never delete anything", removed)
	}
	if n, _ := store.Count(); n != 1 {
		t.Fatalf("%d rows left; want 1", n)
	}
}

func TestReconcileMissingAllPresent(t *testing.T) {
	cat, store, _ := newTestCataloger(t)
	addRow(t, cat, store, "F/A", "")
	addRow(t, cat, store, "F/B", "")

	removed, err := cat.ReconcileMissing(presenceOf("F/A", "F/B"), "")
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if removed != 0 {
		t.Fatalf("removed = %d; want 0", removed)
	}
	if n, _ := store.Count(); n != 2 {
		t.Fatalf("%d rows left; want 2", n)
	}
}

// A base whose JPEG left the bucket but whose RAF is still there is present in
// the listing set (RAW-only bases included): its row must survive — an
// original still exists, and a partially-failed purge relies on the row to
// retry the RAF.
func TestReconcileMissingSparesRawOnlyBase(t *testing.T) {
	cat, store, _ := newTestCataloger(t)
	addRow(t, cat, store, "F/A", "")
	addRow(t, cat, store, "F/B", "") // JPEG gone, RAF still listed
	addRow(t, cat, store, "F/D", "") // fully gone

	removed, err := cat.ReconcileMissing(presenceOf("F/A", "F/B"), "")
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if removed != 1 {
		t.Fatalf("removed = %d; want 1 (F/D only)", removed)
	}
	if n, _ := store.Count(); n != 2 {
		t.Fatalf("%d rows left; want 2 (F/A, F/B)", n)
	}
	if !rowThumbExists(cat, "F/B") {
		t.Fatal("spared RAW-only base must keep its thumbnail")
	}
}

// Rows written by a local-dir scan (synthetic "local-…" ETag) never correspond
// to bucket objects: an S3 scan must not reconcile them away.
func TestReconcileMissingSparesLocalRows(t *testing.T) {
	cat, store, _ := newTestCataloger(t)
	addRow(t, cat, store, "sample/L", "local-1000-123456789")
	addRow(t, cat, store, "F/A", "")

	removed, err := cat.ReconcileMissing(presenceOf("F/A"), "")
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if removed != 0 {
		t.Fatalf("removed = %d; the local-origin row must be spared", removed)
	}
	if n, _ := store.Count(); n != 2 {
		t.Fatalf("%d rows left; want 2", n)
	}
}
