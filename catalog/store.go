package catalog

import (
	"database/sql"
	"fmt"
	"time"

	_ "modernc.org/sqlite" // pure-Go SQLite driver, registers "sqlite"
)

// Store is the SQLite-backed catalog.
type Store struct {
	db *sql.DB
}

const schema = `
CREATE TABLE IF NOT EXISTS photos (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    key_base        TEXT NOT NULL UNIQUE,   -- "100_FUJI/DSCF0960"
    folder          TEXT NOT NULL,
    name            TEXT NOT NULL,

    jpeg_key        TEXT NOT NULL,
    jpeg_size       INTEGER NOT NULL,
    jpeg_etag       TEXT NOT NULL,
    raf_key         TEXT,
    raf_size        INTEGER,
    raf_etag        TEXT,

    thumb_path      TEXT,

    taken_at        TIMESTAMP,
    camera_make     TEXT,
    camera_model    TEXT,
    lens_model      TEXT,
    iso             INTEGER,
    f_number        REAL,
    exposure_time   TEXT,
    exposure_sec    REAL,
    focal_length    REAL,
    focal_length_35 INTEGER,
    width           INTEGER,
    height          INTEGER,
    orientation     INTEGER,
    gps_lat         REAL,
    gps_lon         REAL,
    film_simulation TEXT,
    exif_json       TEXT,

    -- selection workflow (set later by the UI; never overwritten by recatalog).
    -- Mutually exclusive states: rating>0 = kept, decision='discard' = rejected,
    -- neither = undecided.
    decision        TEXT,                   -- NULL | 'discard'
    rating          INTEGER,

    cataloged_at    TIMESTAMP NOT NULL,
    updated_at      TIMESTAMP NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_photos_folder  ON photos(folder);
CREATE INDEX IF NOT EXISTS idx_photos_taken   ON photos(taken_at);
CREATE INDEX IF NOT EXISTS idx_photos_decision ON photos(decision);
`

// migrations are applied in order on top of the base schema. Each must be
// idempotent (IF NOT EXISTS, additive) so an existing populated catalog.db
// upgrades in place with no data loss. PRAGMA user_version records how many
// have run; new versioned changes (e.g. the albums tables) append here.
var migrations = []string{
	// v1: composite index for stable keyset pagination of the gallery,
	// ordered by (taken_at, id). The legacy single-column idx_photos_taken
	// stays for ad-hoc queries.
	`CREATE INDEX IF NOT EXISTS idx_photos_taken_id ON photos(taken_at, id);`,

	// v2: simple albums for selection/export. album_photos has an ordered
	// membership (position) and references photos(id) — stable across recatalog
	// (upsert keeps id). foreign_keys=ON makes both deletes cascade.
	`CREATE TABLE IF NOT EXISTS albums (
	    id         INTEGER PRIMARY KEY AUTOINCREMENT,
	    name       TEXT NOT NULL,
	    created_at TIMESTAMP NOT NULL
	);
	CREATE TABLE IF NOT EXISTS album_photos (
	    album_id INTEGER NOT NULL REFERENCES albums(id)  ON DELETE CASCADE,
	    photo_id INTEGER NOT NULL REFERENCES photos(id)  ON DELETE CASCADE,
	    position INTEGER NOT NULL DEFAULT 0,
	    PRIMARY KEY (album_id, photo_id)
	);
	CREATE INDEX IF NOT EXISTS idx_album_photos_album ON album_photos(album_id, position);`,
}

// migrate brings the database up to len(migrations) using PRAGMA user_version.
func migrate(db *sql.DB) error {
	var version int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		return fmt.Errorf("read user_version: %w", err)
	}
	for i := version; i < len(migrations); i++ {
		if _, err := db.Exec(migrations[i]); err != nil {
			return fmt.Errorf("apply migration v%d: %w", i+1, err)
		}
	}
	if version < len(migrations) {
		// PRAGMA does not accept bound parameters; len(migrations) is trusted.
		if _, err := db.Exec(fmt.Sprintf(`PRAGMA user_version=%d`, len(migrations))); err != nil {
			return fmt.Errorf("set user_version: %w", err)
		}
	}
	return nil
}

// OpenStore opens (creating if needed) the SQLite catalog at path, applies the
// base schema and any pending migrations.
func OpenStore(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	// The web server adds concurrent gallery readers and writers (the worker
	// pool, plus rating/album edits) on top of the original CLI. modernc.org/
	// sqlite does NOT serialize concurrent writers, so we cap the pool at a
	// single connection — correct and simple for a single-user app — and route
	// every write through one writer goroutine (see the worker package). WAL
	// keeps the file consistent; busy_timeout is a backstop for transient locks
	// (e.g. a checkpoint). Relax to a read pool + dedicated writer if one user
	// ever becomes many.
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`PRAGMA journal_mode=WAL; PRAGMA foreign_keys=ON; PRAGMA busy_timeout=5000;`); err != nil {
		db.Close()
		return nil, fmt.Errorf("set pragmas: %w", err)
	}
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("apply schema: %w", err)
	}
	if err := migrate(db); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

// Close closes the underlying database.
func (s *Store) Close() error { return s.db.Close() }

// ExistingETags returns the JPEG and RAW-sibling ETags recorded for keyBase, if
// a row exists. rafETag is "" when no sibling was recorded.
func (s *Store) ExistingETags(keyBase string) (jpegETag, rafETag string, found bool, err error) {
	var raf sql.NullString
	row := s.db.QueryRow(`SELECT jpeg_etag, raf_etag FROM photos WHERE key_base = ?`, keyBase)
	switch err := row.Scan(&jpegETag, &raf); err {
	case nil:
		return jpegETag, raf.String, true, nil
	case sql.ErrNoRows:
		return "", "", false, nil
	default:
		return "", "", false, err
	}
}

// Upsert inserts or updates the row for p. User-owned columns (decision,
// rating) are never touched. now is passed in so callers control timestamps.
func (s *Store) Upsert(p *Photo, now time.Time) error {
	m := p.Meta
	var (
		rafKey, rafETag any
		rafSize         any
		takenAt         any
		gpsLat, gpsLon  any
	)
	if p.RAF != nil {
		rafKey, rafSize, rafETag = p.RAF.Key, p.RAF.Size, p.RAF.ETag
	}
	if !m.TakenAt.IsZero() {
		// Store the camera's local wall-clock as ISO-8601 (SQLite-parseable).
		takenAt = m.TakenAt.Format("2006-01-02 15:04:05")
	}
	// Processing timestamps are true instants: store as UTC ISO-8601.
	nowStr := now.UTC().Format("2006-01-02 15:04:05")
	if m.GPSLat != nil {
		gpsLat = *m.GPSLat
	}
	if m.GPSLon != nil {
		gpsLon = *m.GPSLon
	}

	_, err := s.db.Exec(`
INSERT INTO photos (
    key_base, folder, name,
    jpeg_key, jpeg_size, jpeg_etag, raf_key, raf_size, raf_etag,
    thumb_path,
    taken_at, camera_make, camera_model, lens_model, iso, f_number,
    exposure_time, exposure_sec, focal_length, focal_length_35,
    width, height, orientation, gps_lat, gps_lon, film_simulation, exif_json,
    cataloged_at, updated_at
) VALUES (
    ?, ?, ?,
    ?, ?, ?, ?, ?, ?,
    ?,
    ?, ?, ?, ?, ?, ?,
    ?, ?, ?, ?,
    ?, ?, ?, ?, ?, ?, ?,
    ?, ?
)
ON CONFLICT(key_base) DO UPDATE SET
    folder=excluded.folder, name=excluded.name,
    jpeg_key=excluded.jpeg_key, jpeg_size=excluded.jpeg_size, jpeg_etag=excluded.jpeg_etag,
    raf_key=excluded.raf_key, raf_size=excluded.raf_size, raf_etag=excluded.raf_etag,
    thumb_path=excluded.thumb_path,
    taken_at=excluded.taken_at, camera_make=excluded.camera_make, camera_model=excluded.camera_model,
    lens_model=excluded.lens_model, iso=excluded.iso, f_number=excluded.f_number,
    exposure_time=excluded.exposure_time, exposure_sec=excluded.exposure_sec,
    focal_length=excluded.focal_length, focal_length_35=excluded.focal_length_35,
    width=excluded.width, height=excluded.height, orientation=excluded.orientation,
    gps_lat=excluded.gps_lat, gps_lon=excluded.gps_lon,
    film_simulation=excluded.film_simulation, exif_json=excluded.exif_json,
    updated_at=excluded.updated_at
`,
		p.KeyBase, p.Folder, p.Name,
		p.JPEG.Key, p.JPEG.Size, p.JPEG.ETag, rafKey, rafSize, rafETag,
		nullIfEmpty(p.ThumbPath),
		takenAt, nullIfEmpty(m.CameraMake), nullIfEmpty(m.CameraModel), nullIfEmpty(m.LensModel),
		zeroToNull(m.ISO), zeroToNullF(m.FNumber),
		nullIfEmpty(m.ExposureTime), zeroToNullF(m.ExposureSec),
		zeroToNullF(m.FocalLength), zeroToNull(m.FocalLength35),
		zeroToNull(m.Width), zeroToNull(m.Height), zeroToNull(m.Orientation),
		gpsLat, gpsLon, nullIfEmpty(m.FilmSimulation), nullIfEmpty(m.RawJSON),
		nowStr, nowStr,
	)
	if err != nil {
		return fmt.Errorf("upsert %s: %w", p.KeyBase, err)
	}
	return nil
}

// Backup writes a consistent snapshot of the database to destPath using
// VACUUM INTO (WAL-safe, single statement, pure Go). destPath must not already
// exist. The thumbnails directory is backed up separately (plain file copy).
func (s *Store) Backup(destPath string) error {
	if _, err := s.db.Exec(`VACUUM INTO ?`, destPath); err != nil {
		return fmt.Errorf("backup to %s: %w", destPath, err)
	}
	return nil
}

// Count returns the number of cataloged photos.
func (s *Store) Count() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM photos`).Scan(&n)
	return n, err
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func zeroToNull(i int) any {
	if i == 0 {
		return nil
	}
	return i
}

func zeroToNullF(f float64) any {
	if f == 0 {
		return nil
	}
	return f
}
