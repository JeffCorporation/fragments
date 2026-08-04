package catalog

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// backupPrefix is where catalog backups live in the (backup) bucket. Photos sit
// at the bucket root in prod (DEST_PREFIX empty), but ListPhotos drops non-photo
// extensions, so the two never mix.
const backupPrefix = "backups/"

// defaultBackupName matches the timestamped names BackupKey generates. Only
// these are eligible for rotation by PruneBackups; backups named via -name are
// deliberately exempt.
var defaultBackupName = regexp.MustCompile(`^catalog-\d{8}-\d{6}\.db$`)

// BackupKey validates name and returns the full object key for it. An empty
// name yields the timestamped default (UTC, so names sort consistently across
// machines in different timezones).
func BackupKey(name string, now time.Time) (string, error) {
	if name == "" {
		name = "catalog-" + now.UTC().Format("20060102-150405") + ".db"
	}
	if strings.Contains(name, "/") || !safeKey(name) {
		return "", fmt.Errorf("invalid backup name %q: must be a bare file name", name)
	}
	return backupPrefix + name, nil
}

// BackupDisplayName trims the storage prefix off a backup key for CLI display.
func BackupDisplayName(key string) string {
	return strings.TrimPrefix(key, backupPrefix)
}

// ListBackups returns every backup in the bucket, oldest first.
func ListBackups(ctx context.Context, b *Bucket) ([]BackupInfo, error) {
	return b.ListBackupObjects(ctx, backupPrefix)
}

// UploadBackup vacuums store into a temporary file under dataDir and uploads it
// to key, refusing to overwrite an existing backup. The temp file lives in
// dataDir because the sqlite process can always write there and dot-prefixed
// names cannot collide with catalog.db or thumbs/. Returns the uploaded size.
func UploadBackup(ctx context.Context, store *Store, b *Bucket, dataDir, key string) (int64, error) {
	// VACUUM INTO refuses an existing destination, so reserve a unique name and
	// remove it before the vacuum. The window between Remove and VACUUM is a
	// TOCTOU in theory; fragments is a single-user tool, so it doesn't matter.
	tmp, err := os.CreateTemp(dataDir, ".backup-*.db")
	if err != nil {
		return 0, err
	}
	tmpPath := tmp.Name()
	tmp.Close()
	os.Remove(tmpPath)
	defer os.Remove(tmpPath)

	if err := store.Backup(tmpPath); err != nil {
		return 0, err
	}

	exists, err := b.ObjectExists(ctx, key)
	if err != nil {
		return 0, err
	}
	if exists {
		return 0, fmt.Errorf("backup %s already exists in bucket", BackupDisplayName(key))
	}
	return b.PutFile(ctx, key, tmpPath)
}

// RestoreBackup downloads the backup at key and installs it as the catalog at
// dbPath. The live DB is only touched after the download completed and the file
// passed verification; the previous catalog (if any) is kept at
// dbPath+".pre-restore". force must be set to replace an existing catalog.
func RestoreBackup(ctx context.Context, b *Bucket, key, dbPath string, force bool) error {
	_, statErr := os.Stat(dbPath)
	oldExists := statErr == nil
	if oldExists && !force {
		return fmt.Errorf("%s already exists; stop fragments serve and pass -force to replace it", dbPath)
	}
	dir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	// Download to a temp file next to dbPath (same filesystem, so the final
	// os.Rename is atomic). Any failure removes the temp and leaves the live
	// DB untouched — a truncated body surfaces as an error from io.Copy.
	body, err := b.OpenObject(ctx, key)
	if err != nil {
		return err
	}
	defer body.Close()
	tmp, err := os.CreateTemp(dir, ".restore-*.db")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	rmTemp := func() {
		os.Remove(tmpPath)
		os.Remove(tmpPath + "-wal")
		os.Remove(tmpPath + "-shm")
	}
	if _, err := io.Copy(tmp, body); err != nil {
		tmp.Close()
		rmTemp()
		return fmt.Errorf("download %s: %w", key, err)
	}
	if err := tmp.Close(); err != nil {
		rmTemp()
		return err
	}

	if _, err := verifyCatalogDB(tmpPath); err != nil {
		rmTemp()
		return fmt.Errorf("backup %s failed verification: %w", BackupDisplayName(key), err)
	}

	if oldExists {
		if err := drainOldDB(dbPath); err != nil {
			rmTemp()
			return err
		}
		// Keep one rolling aside; S3 holds the real history.
		if err := os.Rename(dbPath, dbPath+".pre-restore"); err != nil {
			rmTemp()
			return err
		}
	}
	// A stale -wal next to the restored file would be "recovered" into it by
	// SQLite on next open, corrupting it. Safe to delete: drainOldDB just
	// checkpointed it into the file now parked at .pre-restore.
	os.Remove(dbPath + "-wal")
	os.Remove(dbPath + "-shm")
	if err := os.Rename(tmpPath, dbPath); err != nil {
		rmTemp()
		return err
	}
	return nil
}

// verifyCatalogDB proves that path is a healthy fragments catalog before it is
// allowed to replace the live one: integrity_check passes, the schema version
// is one this binary knows, and the photos table is queryable.
func verifyCatalogDB(path string) (photos int, err error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return 0, err
	}
	defer db.Close()

	var result string
	if err := db.QueryRow(`PRAGMA integrity_check`).Scan(&result); err != nil {
		return 0, fmt.Errorf("integrity check: %w", err)
	}
	if result != "ok" {
		return 0, fmt.Errorf("integrity check: %s", result)
	}
	var version int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		return 0, err
	}
	if version > len(migrations) {
		return 0, fmt.Errorf("schema version %d: backup was created by a newer fragments version", version)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM photos`).Scan(&photos); err != nil {
		return 0, fmt.Errorf("not a fragments catalog: %w", err)
	}
	return photos, nil
}

// drainOldDB checkpoints the current catalog's WAL into the main file so its
// sidecars can be deleted, and doubles as a best-effort in-use detector (a
// running fragments serve keeps readers on the DB, which makes a TRUNCATE
// checkpoint report busy). It is not a lock: the authoritative instruction is
// to stop the server before restoring.
func drainOldDB(dbPath string) error {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil // unreadable old DB: restoring over it is the point
	}
	defer db.Close()
	if _, err := db.Exec(`PRAGMA busy_timeout=2000`); err != nil {
		return nil
	}
	var busy, logFrames, checkpointed int
	if err := db.QueryRow(`PRAGMA wal_checkpoint(TRUNCATE)`).Scan(&busy, &logFrames, &checkpointed); err != nil {
		// A corrupt or non-SQLite file can't be checkpointed — proceed; the
		// file survives at .pre-restore for forensics.
		return nil
	}
	if busy != 0 {
		return fmt.Errorf("catalog appears to be in use — stop fragments serve and retry")
	}
	return nil
}

// PruneBackups keeps the `keep` most recent default-named (timestamped) backups
// and deletes the rest. Backups named via -name never match defaultBackupName
// and are left alone — a manual "before-migration" snapshot must not vanish
// silently. keep <= 0 is a no-op. Returns the deleted keys.
func PruneBackups(ctx context.Context, b *Bucket, keep int) ([]string, error) {
	if keep <= 0 {
		return nil, nil
	}
	infos, err := ListBackups(ctx, b)
	if err != nil {
		return nil, err
	}
	var rotatable []BackupInfo // already oldest-first
	for _, o := range infos {
		if defaultBackupName.MatchString(BackupDisplayName(o.Key)) {
			rotatable = append(rotatable, o)
		}
	}
	if len(rotatable) <= keep {
		return nil, nil
	}
	keys := make([]string, 0, len(rotatable)-keep)
	for _, o := range rotatable[:len(rotatable)-keep] {
		keys = append(keys, o.Key)
	}
	failed, err := b.DeleteObjects(ctx, keys)
	if err != nil {
		return nil, err
	}
	if len(failed) > 0 {
		return nil, fmt.Errorf("delete %s: %s", failed[0].Key, failed[0].Message)
	}
	return keys, nil
}
