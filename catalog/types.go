// Package catalog catalogs photos stored on an S3-compatible bucket. For each
// capture it pairs the JPEG and RAW siblings, downloads only the JPEG to
// extract EXIF metadata (including Fujifilm film simulations when present) and
// render a thumbnail, and records everything in a local SQLite database.
// Processing is sequential and idempotent: a capture whose JPEG ETag is
// unchanged is skipped.
package catalog

import "time"

// ObjectRef points at a single object stored in the bucket.
type ObjectRef struct {
	Key  string // full key, e.g. "100_FUJI/DSCF0960.JPG"
	Size int64
	ETag string // S3 ETag (quotes stripped)
}

// Photo is one capture: the JPEG plus its optional RAF sibling, identified by a
// stable KeyBase (the key without extension, e.g. "100_FUJI/DSCF0960").
type Photo struct {
	KeyBase string // pairing key, unique per capture
	Folder  string // e.g. "100_FUJI"
	Name    string // e.g. "DSCF0960"

	JPEG ObjectRef
	RAF  *ObjectRef // nil if no RAF sibling exists

	ThumbPath string   // local path of the generated thumbnail (set after processing)
	Meta      Metadata // EXIF extracted from the JPEG
}

// Metadata is the subset of EXIF we surface as queryable columns. The full raw
// EXIF tag dump is preserved in RawJSON so nothing is lost for future use.
type Metadata struct {
	TakenAt        time.Time // DateTimeOriginal (zero if absent)
	CameraMake     string
	CameraModel    string
	LensModel      string
	ISO            int
	FNumber        float64 // aperture, e.g. 2.8
	ExposureTime   string  // human form, e.g. "1/250"
	ExposureSec    float64 // exposure in seconds
	FocalLength    float64 // mm
	FocalLength35  int     // 35mm-equivalent mm (0 if absent)
	Width          int
	Height         int
	Orientation    int      // EXIF orientation (1..8), 0 if absent
	GPSLat         *float64 // nil if no GPS
	GPSLon         *float64
	FilmSimulation string // Fujifilm film simulation, e.g. "Classic Chrome" ("" if absent)
	RawJSON        string // full EXIF tag dump as JSON, for future use
}
