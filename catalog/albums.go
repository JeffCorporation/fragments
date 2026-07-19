package catalog

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// ErrPhotoNotFound is returned when an album operation references a key_base
// that has no catalog row.
var ErrPhotoNotFound = errors.New("photo not found")

// Album is a simple ordered collection of captures, used to group selections for
// a later export.
type Album struct {
	ID            int64     `json:"id"`
	Name          string    `json:"name"`
	CreatedAt     time.Time `json:"createdAt"`
	PhotoCount    int       `json:"photoCount"`
	CoverThumbURL string    `json:"coverThumbUrl"` // first photo's thumb ("" if empty)
}

// photoID resolves a key_base to its stable photos.id.
func (s *Store) photoID(keyBase string) (int64, bool, error) {
	var id int64
	switch err := s.db.QueryRow(`SELECT id FROM photos WHERE key_base = ?`, keyBase).Scan(&id); err {
	case nil:
		return id, true, nil
	case sql.ErrNoRows:
		return 0, false, nil
	default:
		return 0, false, err
	}
}

// CreateAlbum inserts a new, empty album.
func (s *Store) CreateAlbum(name string) (*Album, error) {
	now := time.Now().UTC()
	res, err := s.db.Exec(`INSERT INTO albums(name, created_at) VALUES(?, ?)`, name, now.Format(takenAtLayout))
	if err != nil {
		return nil, fmt.Errorf("create album: %w", err)
	}
	id, _ := res.LastInsertId()
	return &Album{ID: id, Name: name, CreatedAt: now}, nil
}

// ListAlbums returns all albums (newest first) with their photo count and a
// cover thumbnail (the first photo by position).
func (s *Store) ListAlbums() ([]Album, error) {
	rows, err := s.db.Query(`
SELECT a.id, a.name, CAST(a.created_at AS TEXT),
       (SELECT COUNT(*) FROM album_photos ap WHERE ap.album_id = a.id),
       COALESCE((SELECT p.key_base FROM album_photos ap JOIN photos p ON p.id = ap.photo_id
                 WHERE ap.album_id = a.id ORDER BY ap.position, ap.photo_id LIMIT 1), '')
FROM albums a
ORDER BY a.created_at DESC, a.id DESC`)
	if err != nil {
		return nil, fmt.Errorf("list albums: %w", err)
	}
	defer rows.Close()

	albums := make([]Album, 0)
	for rows.Next() {
		var (
			a         Album
			createdAt sql.NullString
			cover     string
		)
		if err := rows.Scan(&a.ID, &a.Name, &createdAt, &a.PhotoCount, &cover); err != nil {
			return nil, fmt.Errorf("scan album: %w", err)
		}
		if createdAt.Valid {
			if t := parseTakenAt(createdAt.String); t != nil {
				a.CreatedAt = *t
			}
		}
		if cover != "" {
			a.CoverThumbURL = "/thumbs/" + cover + ".jpg"
		}
		albums = append(albums, a)
	}
	return albums, rows.Err()
}

// GetAlbum returns an album's metadata and its photos in membership order, or
// (nil, nil, nil) if no album matches id.
func (s *Store) GetAlbum(id int64) (*Album, []PhotoListItem, error) {
	var (
		a         Album
		createdAt sql.NullString
	)
	switch err := s.db.QueryRow(`SELECT id, name, CAST(created_at AS TEXT) FROM albums WHERE id = ?`, id).
		Scan(&a.ID, &a.Name, &createdAt); err {
	case nil:
	case sql.ErrNoRows:
		return nil, nil, nil
	default:
		return nil, nil, fmt.Errorf("get album: %w", err)
	}
	if createdAt.Valid {
		if t := parseTakenAt(createdAt.String); t != nil {
			a.CreatedAt = *t
		}
	}

	rows, err := s.db.Query("SELECT "+listColumns+
		" FROM photos JOIN album_photos ap ON ap.photo_id = photos.id WHERE ap.album_id = ? ORDER BY ap.position, ap.photo_id", id)
	if err != nil {
		return nil, nil, fmt.Errorf("get album photos: %w", err)
	}
	defer rows.Close()

	items := make([]PhotoListItem, 0)
	for rows.Next() {
		it, _, _, err := scanListItem(rows)
		if err != nil {
			return nil, nil, err
		}
		items = append(items, it)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	a.PhotoCount = len(items)
	if a.CoverThumbURL == "" && len(items) > 0 {
		a.CoverThumbURL = items[0].ThumbURL
	}
	return &a, items, nil
}

// ExportItem is one album member's original S3 keys, for export.
type ExportItem struct {
	KeyBase string
	JPEGKey string
	RAFKey  string // "" if the capture has no RAF sibling
}

// AlbumExportItems returns the album name and its members' original S3 keys in
// membership order, or found=false if no album matches id.
func (s *Store) AlbumExportItems(albumID int64) (name string, items []ExportItem, found bool, err error) {
	switch e := s.db.QueryRow(`SELECT name FROM albums WHERE id = ?`, albumID).Scan(&name); e {
	case nil:
	case sql.ErrNoRows:
		return "", nil, false, nil
	default:
		return "", nil, false, e
	}

	rows, err := s.db.Query(`
SELECT photos.key_base, photos.jpeg_key, COALESCE(photos.raf_key, '')
FROM photos JOIN album_photos ap ON ap.photo_id = photos.id
WHERE ap.album_id = ? ORDER BY ap.position, ap.photo_id`, albumID)
	if err != nil {
		return "", nil, false, fmt.Errorf("album export items: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var it ExportItem
		if err := rows.Scan(&it.KeyBase, &it.JPEGKey, &it.RAFKey); err != nil {
			return "", nil, false, err
		}
		items = append(items, it)
	}
	return name, items, true, rows.Err()
}

// AlbumExists reports whether an album id exists.
func (s *Store) AlbumExists(id int64) (bool, error) {
	var one int
	switch err := s.db.QueryRow(`SELECT 1 FROM albums WHERE id = ?`, id).Scan(&one); err {
	case nil:
		return true, nil
	case sql.ErrNoRows:
		return false, nil
	default:
		return false, err
	}
}

// DeleteAlbum removes an album (cascading its membership) and reports whether it
// existed.
func (s *Store) DeleteAlbum(id int64) (bool, error) {
	res, err := s.db.Exec(`DELETE FROM albums WHERE id = ?`, id)
	if err != nil {
		return false, fmt.Errorf("delete album: %w", err)
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// AddPhotoToAlbum appends a photo to the end of an album (idempotent). It
// returns added=false when the photo was already a member, and ErrPhotoNotFound
// if keyBase is unknown.
func (s *Store) AddPhotoToAlbum(albumID int64, keyBase string) (added bool, err error) {
	pid, found, err := s.photoID(keyBase)
	if err != nil {
		return false, err
	}
	if !found {
		return false, ErrPhotoNotFound
	}
	res, err := s.db.Exec(`
INSERT INTO album_photos(album_id, photo_id, position)
VALUES(?, ?, (SELECT COALESCE(MAX(position), -1) + 1 FROM album_photos WHERE album_id = ?))
ON CONFLICT(album_id, photo_id) DO NOTHING`, albumID, pid, albumID)
	if err != nil {
		return false, fmt.Errorf("add photo to album: %w", err)
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// RemovePhotoFromAlbum removes a photo from an album, reporting whether a
// membership row was deleted.
func (s *Store) RemovePhotoFromAlbum(albumID int64, keyBase string) (bool, error) {
	pid, found, err := s.photoID(keyBase)
	if err != nil {
		return false, err
	}
	if !found {
		return false, nil
	}
	res, err := s.db.Exec(`DELETE FROM album_photos WHERE album_id = ? AND photo_id = ?`, albumID, pid)
	if err != nil {
		return false, fmt.Errorf("remove photo from album: %w", err)
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// ReorderAlbum sets album_photos.position to the index of each key_base in the
// given order, in a single transaction. key_bases not in the album are ignored.
func (s *Store) ReorderAlbum(albumID int64, keyBases []string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("reorder album: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // rolled back unless Commit succeeds

	stmt, err := tx.Prepare(`UPDATE album_photos SET position = ?
WHERE album_id = ? AND photo_id = (SELECT id FROM photos WHERE key_base = ?)`)
	if err != nil {
		return fmt.Errorf("reorder album: %w", err)
	}
	defer stmt.Close()

	for i, kb := range keyBases {
		if _, err := stmt.Exec(i, albumID, kb); err != nil {
			return fmt.Errorf("reorder album: %w", err)
		}
	}
	return tx.Commit()
}
