package catalog

import (
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// PhotoListItem is the narrow gallery DTO. It deliberately EXCLUDES exif_json
// (the full raw EXIF dump) so the hot list path stays small. Width/Height are
// the UPRIGHT thumbnail dimensions (swapped for EXIF orientations 5-8, which
// rotate the image 90°) so the frontend can lay out the justified grid using
// them directly; they are 0 when EXIF carried no dimensions.
type PhotoListItem struct {
	KeyBase        string     `json:"keyBase"`
	Name           string     `json:"name"`
	Folder         string     `json:"folder"`
	TakenAt        *time.Time `json:"takenAt"`
	Width          int        `json:"width"`
	Height         int        `json:"height"`
	CameraModel    string     `json:"cameraModel"`
	LensModel      string     `json:"lensModel"`
	ISO            int        `json:"iso"`
	FNumber        float64    `json:"fNumber"`
	ExposureTime   string     `json:"exposureTime"`
	FocalLength    float64    `json:"focalLength"`
	FilmSimulation string     `json:"filmSimulation"`
	Rating         int        `json:"rating"`
	Decision       string     `json:"decision"`
	ThumbURL       string     `json:"thumbUrl"`
}

// PhotoDetail is the full single-photo payload, including the raw EXIF JSON and
// the original S3 keys (for a future export / full-res view).
type PhotoDetail struct {
	PhotoListItem
	JPEGKey  string   `json:"jpegKey"`
	RAFKey   string   `json:"rafKey"`
	GPSLat   *float64 `json:"gpsLat"`
	GPSLon   *float64 `json:"gpsLon"`
	ExifJSON string   `json:"exifJson"`
}

// PhotoFilter narrows the gallery listing. Zero-value fields are ignored.
type PhotoFilter struct {
	Folder         string // exact folder match
	FilmSimulation string // exact film simulation match
	CameraModel    string // exact camera model match
	MinRating      int    // rating >= MinRating (0 = no filter)
	Decision       string // "keep" (rated) | "discard" | "none" (undecided) | "" (any)
}

// PhotoPage is one keyset page of gallery items. NextCursor is "" when the last
// page has been reached.
type PhotoPage struct {
	Items      []PhotoListItem `json:"items"`
	NextCursor string          `json:"nextCursor"`
}

// listColumns is the explicit narrow column set for the gallery list. id is
// appended last for cursor construction; exif_json is never selected here.
//
// taken_at is wrapped in CAST(... AS TEXT) on purpose: modernc.org/sqlite
// auto-converts TIMESTAMP columns to time.Time on scan (yielding an RFC3339
// string), but the value was stored in space-separated form. The CAST returns
// the RAW stored text instead, so the keyset cursor compares like-for-like with
// the column in SQL regardless of the exact textual format.
const listColumns = `key_base, name, folder, CAST(taken_at AS TEXT),
	COALESCE(width,0), COALESCE(height,0), COALESCE(orientation,0),
	COALESCE(camera_model,''), COALESCE(lens_model,''), COALESCE(iso,0),
	COALESCE(f_number,0), COALESCE(exposure_time,''), COALESCE(focal_length,0),
	COALESCE(film_simulation,''), COALESCE(rating,0), COALESCE(decision,''), id`

const (
	defaultPageLimit = 60
	maxPageLimit     = 200
	takenAtLayout    = "2006-01-02 15:04:05"
)

// ListPhotos returns one keyset page of gallery items, newest first. Ordering is
// (taken_at DESC, id DESC) with NULL taken_at sorted last and broken by id, so
// infinite scroll never skips or duplicates a row. Pass the previous page's
// NextCursor to fetch the next page; an empty cursor starts from the top.
func (s *Store) ListPhotos(filter PhotoFilter, cursor string, limit int) (*PhotoPage, error) {
	if limit <= 0 {
		limit = defaultPageLimit
	}
	if limit > maxPageLimit {
		limit = maxPageLimit
	}

	var where []string
	var args []any

	if filter.Folder != "" {
		where = append(where, "folder = ?")
		args = append(args, filter.Folder)
	}
	if filter.FilmSimulation != "" {
		where = append(where, "film_simulation = ?")
		args = append(args, filter.FilmSimulation)
	}
	if filter.CameraModel != "" {
		where = append(where, "camera_model = ?")
		args = append(args, filter.CameraModel)
	}
	if filter.MinRating > 0 {
		where = append(where, "COALESCE(rating,0) >= ?")
		args = append(args, filter.MinRating)
	}
	// Tri-state selection (mutually exclusive): a rating means "kept", a
	// 'discard' means rejected, neither means undecided. SetRating/SetDecision
	// keep these exclusive, so the gallery filter can read them off each column.
	switch filter.Decision {
	case "keep": // "À garder" → has a rating
		where = append(where, "COALESCE(rating,0) > 0")
	case "discard": // "À jeter"
		where = append(where, "decision = 'discard'")
	case "none": // "Non décidé" → no rating and not discarded
		where = append(where, "COALESCE(rating,0) = 0 AND decision IS NULL")
	}

	if cursor != "" {
		isNull, ct, cid, err := decodeCursor(cursor)
		if err != nil {
			return nil, err
		}
		if isNull {
			// Cursor already in the NULL-taken_at tail: only smaller ids remain.
			where = append(where, "(taken_at IS NULL AND id < ?)")
			args = append(args, cid)
		} else {
			// Remaining rows: non-null rows ordered after the cursor, then the
			// entire NULL tail.
			where = append(where, "((taken_at IS NOT NULL AND (taken_at < ? OR (taken_at = ? AND id < ?))) OR taken_at IS NULL)")
			args = append(args, ct, ct, cid)
		}
	}

	q := "SELECT " + listColumns + " FROM photos"
	if len(where) > 0 {
		q += " WHERE " + strings.Join(where, " AND ")
	}
	q += " ORDER BY taken_at IS NULL ASC, taken_at DESC, id DESC LIMIT ?"
	args = append(args, limit+1) // fetch one extra to detect a further page

	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, fmt.Errorf("list photos: %w", err)
	}
	defer rows.Close()

	page := &PhotoPage{Items: make([]PhotoListItem, 0, limit)}
	var lastTaken sql.NullString
	var lastID int64
	for rows.Next() {
		item, takenAt, id, err := scanListItem(rows)
		if err != nil {
			return nil, err
		}
		if len(page.Items) == limit {
			// The extra row proves there is another page; encode the cursor
			// from the last KEPT row and stop.
			page.NextCursor = encodeCursor(lastTaken, lastID)
			break
		}
		page.Items = append(page.Items, item)
		lastTaken, lastID = takenAt, id
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list photos: %w", err)
	}
	return page, nil
}

// GetPhoto returns the full detail (incl. raw EXIF) for one capture, or
// (nil, nil) if no row matches keyBase.
func (s *Store) GetPhoto(keyBase string) (*PhotoDetail, error) {
	q := "SELECT " + listColumns + ", jpeg_key, COALESCE(raf_key,''), gps_lat, gps_lon, COALESCE(exif_json,'') FROM photos WHERE key_base = ?"
	row := s.db.QueryRow(q, keyBase)

	var (
		d           PhotoDetail
		takenAt     sql.NullString
		orientation int
		id          int64
		gpsLat      sql.NullFloat64
		gpsLon      sql.NullFloat64
	)
	err := row.Scan(
		&d.KeyBase, &d.Name, &d.Folder, &takenAt,
		&d.Width, &d.Height, &orientation,
		&d.CameraModel, &d.LensModel, &d.ISO,
		&d.FNumber, &d.ExposureTime, &d.FocalLength,
		&d.FilmSimulation, &d.Rating, &d.Decision, &id,
		&d.JPEGKey, &d.RAFKey, &gpsLat, &gpsLon, &d.ExifJSON,
	)
	switch err {
	case nil:
	case sql.ErrNoRows:
		return nil, nil
	default:
		return nil, fmt.Errorf("get photo %s: %w", keyBase, err)
	}

	applyDerived(&d.PhotoListItem, takenAt, orientation)
	if gpsLat.Valid {
		d.GPSLat = &gpsLat.Float64
	}
	if gpsLon.Valid {
		d.GPSLon = &gpsLon.Float64
	}
	return &d, nil
}

// scanListItem scans one gallery row (the listColumns set) into a PhotoListItem,
// also returning the raw taken_at and id needed for cursors.
func scanListItem(rows *sql.Rows) (PhotoListItem, sql.NullString, int64, error) {
	var (
		it          PhotoListItem
		takenAt     sql.NullString
		orientation int
		id          int64
	)
	if err := rows.Scan(
		&it.KeyBase, &it.Name, &it.Folder, &takenAt,
		&it.Width, &it.Height, &orientation,
		&it.CameraModel, &it.LensModel, &it.ISO,
		&it.FNumber, &it.ExposureTime, &it.FocalLength,
		&it.FilmSimulation, &it.Rating, &it.Decision, &id,
	); err != nil {
		return it, takenAt, id, fmt.Errorf("scan photo: %w", err)
	}
	applyDerived(&it, takenAt, orientation)
	return it, takenAt, id, nil
}

// applyDerived fills the parsed TakenAt, the upright Width/Height (swapped for
// orientations 5-8) and the thumbnail URL.
func applyDerived(it *PhotoListItem, takenAt sql.NullString, orientation int) {
	if takenAt.Valid {
		it.TakenAt = parseTakenAt(takenAt.String)
	}
	if orientation >= 5 && orientation <= 8 { // 90°/270° rotations swap the sides
		it.Width, it.Height = it.Height, it.Width
	}
	it.ThumbURL = "/thumbs/" + it.KeyBase + ".jpg"
}

// parseTakenAt best-effort parses the stored timestamp for display. Pagination
// never relies on this (it uses the raw text), so a parse miss just yields a
// null TakenAt in the JSON.
func parseTakenAt(s string) *time.Time {
	for _, layout := range []string{takenAtLayout, time.RFC3339} {
		if t, err := time.Parse(layout, s); err == nil {
			return &t
		}
	}
	return nil
}

// encodeCursor packs (taken_at, id) of the last row into an opaque token.
func encodeCursor(takenAt sql.NullString, id int64) string {
	nullFlag, val := "0", takenAt.String
	if !takenAt.Valid {
		nullFlag, val = "1", ""
	}
	raw := nullFlag + "|" + val + "|" + strconv.FormatInt(id, 10)
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

// nowStamp formats the current instant as the SQLite-stored UTC timestamp.
func nowStamp() string {
	return time.Now().UTC().Format(takenAtLayout)
}

// ErrInvalidRating / ErrInvalidDecision mark client mistakes (as opposed to
// internal store failures) so the HTTP layer can map them to 400 vs 500.
var (
	ErrInvalidRating   = errors.New("rating must be between 0 and 5")
	ErrInvalidDecision = errors.New("decision must be discard or none")
)

// SetRating sets the user rating (0..5; 0 clears it to NULL) for keyBase and
// returns whether a matching photo existed. A rating means the photo is "kept",
// so setting one clears any prior 'discard' (the keep/discard states are
// mutually exclusive). This is a user-owned column the recatalog Upsert never
// touches, so it survives reprocessing.
func (s *Store) SetRating(keyBase string, rating int) (bool, error) {
	if rating < 0 || rating > 5 {
		return false, ErrInvalidRating
	}
	var res sql.Result
	var err error
	if rating > 0 {
		res, err = s.db.Exec(`UPDATE photos SET rating=?, decision=NULL, updated_at=? WHERE key_base=?`, rating, nowStamp(), keyBase)
	} else {
		// 0 → undecided: drop the stars; a rated photo is never discarded, so
		// there is no decision to clear here.
		res, err = s.db.Exec(`UPDATE photos SET rating=NULL, updated_at=? WHERE key_base=?`, nowStamp(), keyBase)
	}
	if err != nil {
		return false, fmt.Errorf("set rating %s: %w", keyBase, err)
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// SetDecision sets the reject decision for keyBase: "discard" rejects the photo
// (and clears any rating — a discarded photo is never "kept"), while ""/"none"
// clears it back to undecided. Returns whether a matching photo existed.
func (s *Store) SetDecision(keyBase, decision string) (bool, error) {
	var res sql.Result
	var err error
	switch decision {
	case "discard":
		res, err = s.db.Exec(`UPDATE photos SET decision='discard', rating=NULL, updated_at=? WHERE key_base=?`, nowStamp(), keyBase)
	case "", "none":
		res, err = s.db.Exec(`UPDATE photos SET decision=NULL, updated_at=? WHERE key_base=?`, nowStamp(), keyBase)
	default:
		return false, ErrInvalidDecision
	}
	if err != nil {
		return false, fmt.Errorf("set decision %s: %w", keyBase, err)
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// decodeCursor reverses encodeCursor. takenAt values contain no '|', so a
// 3-way split is unambiguous.
func decodeCursor(s string) (isNull bool, takenAt string, id int64, err error) {
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return false, "", 0, fmt.Errorf("invalid cursor")
	}
	parts := strings.SplitN(string(b), "|", 3)
	if len(parts) != 3 {
		return false, "", 0, fmt.Errorf("invalid cursor")
	}
	id, err = strconv.ParseInt(parts[2], 10, 64)
	if err != nil {
		return false, "", 0, fmt.Errorf("invalid cursor")
	}
	return parts[0] == "1", parts[1], id, nil
}
