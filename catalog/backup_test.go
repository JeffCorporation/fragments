package catalog

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// backupStub answers PutObject, HeadObject, GetObject, ListObjectsV2 and
// DeleteObjects like an S3-compatible endpoint (path-style). Objects uploaded
// through PUT get deterministic, strictly increasing LastModified times.
type backupStub struct {
	mu      sync.Mutex
	objects map[string]stubBackupObj
	paths   []string // "METHOD /bucket/key" of every request received
	seq     int
}

type stubBackupObj struct {
	data []byte
	mod  time.Time
}

var stubEpoch = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func newBackupStub() *backupStub {
	return &backupStub{objects: map[string]stubBackupObj{}}
}

// put seeds an object directly, bypassing HTTP.
func (s *backupStub) put(key string, data []byte, mod time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.objects[key] = stubBackupObj{data: data, mod: mod}
}

func (s *backupStub) keys() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for k := range s.objects {
		out = append(out, k)
	}
	return out
}

func (s *backupStub) handler(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.paths = append(s.paths, r.Method+" "+r.URL.Path)
		s.mu.Unlock()

		// Path-style: /<bucket>/<key>; bucket-level requests have no key part.
		parts := strings.SplitN(strings.TrimPrefix(r.URL.Path, "/"), "/", 2)
		key := ""
		if len(parts) == 2 {
			key = parts[1]
		}

		switch {
		case r.Method == http.MethodGet && r.URL.Query().Get("list-type") == "2":
			prefix := r.URL.Query().Get("prefix")
			out := `<?xml version="1.0" encoding="UTF-8"?><ListBucketResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/"><Name>bucket</Name><IsTruncated>false</IsTruncated>`
			s.mu.Lock()
			for k, o := range s.objects {
				if !strings.HasPrefix(k, prefix) {
					continue
				}
				out += fmt.Sprintf(`<Contents><Key>%s</Key><Size>%d</Size><LastModified>%s</LastModified><ETag>"x"</ETag></Contents>`,
					k, len(o.data), o.mod.UTC().Format("2006-01-02T15:04:05.000Z"))
			}
			s.mu.Unlock()
			out += `</ListBucketResult>`
			w.Header().Set("Content-Type", "application/xml")
			_, _ = w.Write([]byte(out))

		case r.Method == http.MethodPut:
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Errorf("read put body: %v", err)
			}
			s.mu.Lock()
			s.seq++
			s.objects[key] = stubBackupObj{data: body, mod: stubEpoch.Add(time.Duration(s.seq) * time.Minute)}
			s.mu.Unlock()

		case r.Method == http.MethodHead:
			s.mu.Lock()
			o, ok := s.objects[key]
			s.mu.Unlock()
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			w.Header().Set("Content-Length", fmt.Sprint(len(o.data)))

		case r.Method == http.MethodGet:
			s.mu.Lock()
			o, ok := s.objects[key]
			s.mu.Unlock()
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			_, _ = w.Write(o.data)

		case r.Method == http.MethodPost && r.URL.Query().Has("delete"):
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
			for _, o := range req.Objects {
				delete(s.objects, o.Key)
			}
			s.mu.Unlock()
			w.Header().Set("Content-Type", "application/xml")
			_, _ = w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?><DeleteResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/"></DeleteResult>`))

		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL)
			http.Error(w, "unexpected", http.StatusBadRequest)
		}
	}
}

// newBackupEnv wires a stub server, a Config over t.TempDir() and its Bucket.
func newBackupEnv(t *testing.T) (*backupStub, *Config, *Bucket) {
	t.Helper()
	stub := newBackupStub()
	ts := httptest.NewServer(stub.handler(t))
	t.Cleanup(ts.Close)

	dir := t.TempDir()
	cfg := &Config{
		AccessKeyID: "test", SecretAccessKey: "test",
		Bucket: "bucket", BackupBucket: "bucket",
		Region: "test", Endpoint: ts.URL, UsePathStyle: true,
		DataDir: dir, DBPath: filepath.Join(dir, "catalog.db"),
	}
	bucket, err := NewBackupBucket(context.Background(), cfg)
	if err != nil {
		t.Fatalf("new bucket: %v", err)
	}
	return stub, cfg, bucket
}

// newBackupStore opens a store at cfg.DBPath holding n photos.
func newBackupStore(t *testing.T, cfg *Config, n int) *Store {
	t.Helper()
	store, err := OpenStore(cfg.DBPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	for i := 0; i < n; i++ {
		p := &Photo{
			KeyBase: fmt.Sprintf("F/IMG%04d", i), Folder: "F", Name: fmt.Sprintf("IMG%04d", i),
			JPEG: ObjectRef{Key: fmt.Sprintf("F/IMG%04d.JPG", i), Size: 1000, ETag: fmt.Sprint(i)},
		}
		if err := store.Upsert(p, time.Now()); err != nil {
			t.Fatalf("upsert: %v", err)
		}
	}
	return store
}

func TestBackupKey(t *testing.T) {
	now := time.Date(2026, 8, 3, 15, 30, 0, 0, time.UTC)
	key, err := BackupKey("", now)
	if err != nil || key != "backups/catalog-20260803-153000.db" {
		t.Errorf("default name: got %q, %v", key, err)
	}
	key, err = BackupKey("avant-migration.db", now)
	if err != nil || key != "backups/avant-migration.db" {
		t.Errorf("custom name: got %q, %v", key, err)
	}
	for _, bad := range []string{"../x", "a/b", "..", `a\b`, "/abs"} {
		if _, err := BackupKey(bad, now); err == nil {
			t.Errorf("BackupKey(%q): want error, got nil", bad)
		}
	}
}

func TestUploadBackup(t *testing.T) {
	stub, cfg, bucket := newBackupEnv(t)
	store := newBackupStore(t, cfg, 1)

	key := "backups/catalog-20260803-153000.db"
	size, err := UploadBackup(context.Background(), store, bucket, cfg.DataDir, key)
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	stub.mu.Lock()
	obj, ok := stub.objects[key]
	stub.mu.Unlock()
	if !ok {
		t.Fatalf("object %s not uploaded; have %v", key, stub.keys())
	}
	if !bytes.HasPrefix(obj.data, []byte("SQLite format 3\x00")) {
		t.Errorf("uploaded bytes are not a SQLite DB (start: %q)", obj.data[:16])
	}
	if size != int64(len(obj.data)) {
		t.Errorf("size = %d, uploaded %d bytes", size, len(obj.data))
	}
	leftovers, _ := filepath.Glob(filepath.Join(cfg.DataDir, ".backup-*"))
	if len(leftovers) > 0 {
		t.Errorf("temp files left behind: %v", leftovers)
	}
}

func TestUploadBackupRefusesExistingKey(t *testing.T) {
	stub, cfg, bucket := newBackupEnv(t)
	store := newBackupStore(t, cfg, 1)

	key := "backups/catalog-20260803-153000.db"
	stub.put(key, []byte("already there"), stubEpoch)
	_, err := UploadBackup(context.Background(), store, bucket, cfg.DataDir, key)
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("want 'already exists' error, got %v", err)
	}
}

// seedBackup makes a real catalog snapshot with n photos and loads it into the
// stub at key.
func seedBackup(t *testing.T, stub *backupStub, cfg *Config, key string, n int) {
	t.Helper()
	store := newBackupStore(t, cfg, n)
	snap := filepath.Join(t.TempDir(), "snap.db")
	if err := store.Backup(snap); err != nil {
		t.Fatalf("vacuum: %v", err)
	}
	data, err := os.ReadFile(snap)
	if err != nil {
		t.Fatalf("read snapshot: %v", err)
	}
	stub.put(key, data, stubEpoch)
}

func TestRestoreBackupRoundTrip(t *testing.T) {
	stub, cfg, bucket := newBackupEnv(t)
	const key = "backups/catalog-20260803-153000.db"
	seedBackup(t, stub, cfg, key, 3)

	// Fresh-machine path: destination dir has no catalog, no -force needed.
	destDir := t.TempDir()
	dbPath := filepath.Join(destDir, "catalog.db")
	if err := RestoreBackup(context.Background(), bucket, key, dbPath, false); err != nil {
		t.Fatalf("restore: %v", err)
	}
	restored, err := OpenStore(dbPath)
	if err != nil {
		t.Fatalf("open restored: %v", err)
	}
	defer restored.Close()
	if n, _ := restored.Count(); n != 3 {
		t.Errorf("restored count = %d, want 3", n)
	}
}

func TestRestoreRefusesClobberWithoutForce(t *testing.T) {
	stub, cfg, bucket := newBackupEnv(t)
	const key = "backups/catalog-20260803-153000.db"
	seedBackup(t, stub, cfg, key, 3) // also creates the live DB at cfg.DBPath

	err := RestoreBackup(context.Background(), bucket, key, cfg.DBPath, false)
	if err == nil || !strings.Contains(err.Error(), "-force") {
		t.Fatalf("want error mentioning -force, got %v", err)
	}

	// Stale sidecars must not survive next to the restored file.
	for _, side := range []string{"-wal", "-shm"} {
		if werr := os.WriteFile(cfg.DBPath+side, []byte("stale"), 0o644); werr != nil {
			t.Fatal(werr)
		}
	}
	if err := RestoreBackup(context.Background(), bucket, key, cfg.DBPath, true); err != nil {
		t.Fatalf("restore -force: %v", err)
	}
	if _, err := os.Stat(cfg.DBPath + ".pre-restore"); err != nil {
		t.Errorf("previous catalog not kept: %v", err)
	}
	for _, side := range []string{"-wal", "-shm"} {
		if _, err := os.Stat(cfg.DBPath + side); !os.IsNotExist(err) {
			t.Errorf("stale sidecar %s still present", side)
		}
	}
	restored, err := OpenStore(cfg.DBPath)
	if err != nil {
		t.Fatalf("open restored: %v", err)
	}
	defer restored.Close()
	if n, _ := restored.Count(); n != 3 {
		t.Errorf("restored count = %d, want 3", n)
	}
}

func TestRestoreRejectsGarbage(t *testing.T) {
	stub, cfg, bucket := newBackupEnv(t)
	const key = "backups/garbage.db"
	stub.put(key, []byte("this is definitely not a sqlite database"), stubEpoch)
	newBackupStore(t, cfg, 2) // live DB that must stay untouched

	err := RestoreBackup(context.Background(), bucket, key, cfg.DBPath, true)
	if err == nil || !strings.Contains(err.Error(), "verification") {
		t.Fatalf("want verification error, got %v", err)
	}
	if _, err := os.Stat(cfg.DBPath); err != nil {
		t.Errorf("live DB was touched: %v", err)
	}
	leftovers, _ := filepath.Glob(filepath.Join(cfg.DataDir, ".restore-*"))
	if len(leftovers) > 0 {
		t.Errorf("temp files left behind: %v", leftovers)
	}
}

func TestRestoreRejectsNewerUserVersion(t *testing.T) {
	stub, cfg, bucket := newBackupEnv(t)
	store := newBackupStore(t, cfg, 1)
	snap := filepath.Join(t.TempDir(), "snap.db")
	if err := store.Backup(snap); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", snap)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`PRAGMA user_version=99`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	data, _ := os.ReadFile(snap)
	const key = "backups/future.db"
	stub.put(key, data, stubEpoch)

	err = RestoreBackup(context.Background(), bucket, key, filepath.Join(t.TempDir(), "catalog.db"), false)
	if err == nil || !strings.Contains(err.Error(), "newer") {
		t.Fatalf("want 'newer version' error, got %v", err)
	}
}

func TestListBackups(t *testing.T) {
	stub, _, bucket := newBackupEnv(t)
	stub.put("backups/b.db", []byte("2"), stubEpoch.Add(2*time.Hour))
	stub.put("backups/a.db", []byte("3"), stubEpoch.Add(3*time.Hour))
	stub.put("backups/c.db", []byte("1"), stubEpoch.Add(1*time.Hour))
	stub.put("not-a-backup.JPG", []byte("x"), stubEpoch) // outside the prefix

	got, err := ListBackups(context.Background(), bucket)
	if err != nil {
		t.Fatal(err)
	}
	var keys []string
	for _, o := range got {
		keys = append(keys, o.Key)
	}
	want := []string{"backups/c.db", "backups/b.db", "backups/a.db"}
	if fmt.Sprint(keys) != fmt.Sprint(want) {
		t.Errorf("order = %v, want %v (oldest first)", keys, want)
	}
}

func TestPruneBackups(t *testing.T) {
	stub, _, bucket := newBackupEnv(t)
	for i := 1; i <= 5; i++ {
		stub.put(fmt.Sprintf("backups/catalog-2026010%d-000000.db", i), []byte("x"),
			stubEpoch.Add(time.Duration(i)*time.Hour))
	}
	stub.put("backups/manual.db", []byte("x"), stubEpoch) // oldest of all, but named

	// keep=0 and keep >= count are no-ops.
	for _, keep := range []int{0, 5, 10} {
		pruned, err := PruneBackups(context.Background(), bucket, keep)
		if err != nil || len(pruned) != 0 {
			t.Fatalf("keep=%d: got %v, %v; want no-op", keep, pruned, err)
		}
	}

	pruned, err := PruneBackups(context.Background(), bucket, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(pruned) != 3 {
		t.Fatalf("pruned %v, want the 3 oldest timestamped", pruned)
	}
	left := stub.keys()
	for _, want := range []string{"backups/manual.db", "backups/catalog-20260104-000000.db", "backups/catalog-20260105-000000.db"} {
		if !contains(left, want) {
			t.Errorf("%s should have been kept; left: %v", want, left)
		}
	}
	if len(left) != 3 {
		t.Errorf("left = %v, want exactly 3 objects", left)
	}
}

func TestNewBackupBucketOverride(t *testing.T) {
	stub, cfg, _ := newBackupEnv(t)
	cfg.BackupBucket = "vault"
	bucket, err := NewBackupBucket(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	store := newBackupStore(t, cfg, 1)
	if _, err := UploadBackup(context.Background(), store, bucket, cfg.DataDir, "backups/x.db"); err != nil {
		t.Fatalf("upload: %v", err)
	}
	stub.mu.Lock()
	defer stub.mu.Unlock()
	found := false
	for _, p := range stub.paths {
		if strings.HasPrefix(p, "PUT /vault/") {
			found = true
		}
		if strings.Contains(p, "/bucket/") {
			t.Errorf("request hit the photo bucket: %s", p)
		}
	}
	if !found {
		t.Errorf("no PUT against the override bucket; requests: %v", stub.paths)
	}
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}
