package catalog

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"

	goexif "github.com/dsoprea/go-exif/v3"
	exifcommon "github.com/dsoprea/go-exif/v3/common"
)

// ExtractMetadata parses EXIF from JPEG bytes. Standard tags come from
// dsoprea/go-exif; the Fujifilm film simulation is decoded from the raw maker
// note (which no pure-Go library names for Fuji). A photo without EXIF is not
// an error: the returned Metadata is simply mostly zero.
func ExtractMetadata(jpegData []byte) (Metadata, error) {
	var m Metadata

	rawExif, err := goexif.SearchAndExtractExif(jpegData)
	if err != nil {
		if err == goexif.ErrNoExif {
			return m, nil
		}
		return m, fmt.Errorf("locate exif: %w", err)
	}

	tags, _, err := goexif.GetFlatExifData(rawExif, nil)
	if err != nil {
		return m, fmt.Errorf("parse exif: %w", err)
	}

	exifDump := map[string]any{}
	gps := gpsParts{}
	var makerNote []byte
	var dtoRaw, offsetRaw string

	for i := range tags {
		t := &tags[i]
		// Skip the thumbnail IFD entirely: it duplicates root-IFD tags
		// (Orientation, resolution, ...) describing the embedded thumbnail, and
		// since it comes last in the flat list it would otherwise overwrite the
		// real values ("last tag wins" below).
		if t.IfdPath == "IFD1" || strings.HasPrefix(t.IfdPath, "IFD1/") {
			continue
		}
		if t.TagName != "" && t.Formatted != "" {
			exifDump[t.TagName] = t.Formatted
		}
		switch t.TagName {
		case "DateTimeOriginal":
			dtoRaw = asString(t.Value)
		case "OffsetTimeOriginal":
			offsetRaw = asString(t.Value)
		case "OffsetTime":
			if offsetRaw == "" {
				offsetRaw = asString(t.Value)
			}
		case "Make":
			m.CameraMake = asString(t.Value)
		case "Model":
			m.CameraModel = asString(t.Value)
		case "LensModel":
			m.LensModel = asString(t.Value)
		case "ISOSpeedRatings", "PhotographicSensitivity":
			m.ISO = firstInt(t.Value)
		case "FNumber":
			m.FNumber = firstRatF(t.Value)
		case "ExposureTime":
			if n, d, ok := firstRat(t.Value); ok && d != 0 {
				m.ExposureSec = float64(n) / float64(d)
				m.ExposureTime = formatExposure(n, d)
			}
		case "FocalLength":
			m.FocalLength = firstRatF(t.Value)
		case "FocalLengthIn35mmFilm":
			m.FocalLength35 = firstInt(t.Value)
		case "PixelXDimension":
			m.Width = firstInt(t.Value)
		case "PixelYDimension":
			m.Height = firstInt(t.Value)
		case "Orientation":
			m.Orientation = firstInt(t.Value)
		case "GPSLatitude":
			gps.lat = t.Value
		case "GPSLatitudeRef":
			gps.latRef = asString(t.Value)
		case "GPSLongitude":
			gps.lon = t.Value
		case "GPSLongitudeRef":
			gps.lonRef = asString(t.Value)
		case "MakerNote":
			if len(t.ValueBytes) > 0 {
				makerNote = t.ValueBytes
			}
		}
	}

	m.TakenAt = parseEXIFTime(dtoRaw, offsetRaw)

	// Fall back to ImageWidth/Length if the Exif IFD pixel dims were absent.
	if m.Width == 0 {
		if v, ok := exifDump["ImageWidth"]; ok {
			m.Width = atoiSafe(fmt.Sprint(v))
		}
	}
	if m.Height == 0 {
		if v, ok := exifDump["ImageLength"]; ok {
			m.Height = atoiSafe(fmt.Sprint(v))
		}
	}

	if lat, lon, ok := gps.decimal(); ok {
		m.GPSLat, m.GPSLon = &lat, &lon
	}

	fujiDump := map[string]any{}
	if len(makerNote) > 0 {
		if fuji, err := parseFujiMakerNote(makerNote); err == nil && fuji != nil {
			m.FilmSimulation = fuji.filmSimulation()
			fujiDump = fuji.named()
		}
	}

	raw, _ := json.Marshal(map[string]any{"exif": exifDump, "fuji": fujiDump})
	m.RawJSON = string(raw)
	return m, nil
}

// parseEXIFTime parses DateTimeOriginal. EXIF stores a zone-less wall clock; if
// OffsetTimeOriginal (e.g. "-04:00") is present we attach it so the result is a
// correct instant in the camera's local zone. Otherwise the time is treated as
// a naive wall clock (UTC location, same digits). Zero time on failure.
func parseEXIFTime(dt, offset string) time.Time {
	dt = strings.TrimSpace(dt)
	if dt == "" {
		return time.Time{}
	}
	if offset = strings.TrimSpace(offset); offset != "" {
		if t, err := time.Parse("2006:01:02 15:04:05 -07:00", dt+" "+offset); err == nil {
			return t
		}
	}
	if t, err := time.Parse("2006:01:02 15:04:05", dt); err == nil {
		return t
	}
	return time.Time{}
}

// ---- GPS ----

type gpsParts struct {
	lat, lon       any
	latRef, lonRef string
}

func (g gpsParts) decimal() (lat, lon float64, ok bool) {
	la, ok1 := dms(g.lat)
	lo, ok2 := dms(g.lon)
	if !ok1 || !ok2 {
		return 0, 0, false
	}
	if strings.EqualFold(g.latRef, "S") {
		la = -la
	}
	if strings.EqualFold(g.lonRef, "W") {
		lo = -lo
	}
	return la, lo, true
}

// dms converts a 3-rational EXIF GPS coordinate to decimal degrees.
func dms(v any) (float64, bool) {
	rs, ok := v.([]exifcommon.Rational)
	if !ok || len(rs) < 3 {
		return 0, false
	}
	f := func(r exifcommon.Rational) float64 {
		if r.Denominator == 0 {
			return 0
		}
		return float64(r.Numerator) / float64(r.Denominator)
	}
	return f(rs[0]) + f(rs[1])/60 + f(rs[2])/3600, true
}

// ---- value helpers (dsoprea returns slices for most numeric tag types) ----

func asString(v any) string {
	if s, ok := v.(string); ok {
		return strings.TrimRight(strings.TrimSpace(s), "\x00")
	}
	return strings.TrimSpace(fmt.Sprint(v))
}

func firstInt(v any) int {
	switch x := v.(type) {
	case []uint16:
		if len(x) > 0 {
			return int(x[0])
		}
	case []uint32:
		if len(x) > 0 {
			return int(x[0])
		}
	case []int16:
		if len(x) > 0 {
			return int(x[0])
		}
	case []int32:
		if len(x) > 0 {
			return int(x[0])
		}
	case uint16:
		return int(x)
	case uint32:
		return int(x)
	case int:
		return x
	}
	return 0
}

func firstRat(v any) (num, den uint32, ok bool) {
	if rs, ok := v.([]exifcommon.Rational); ok && len(rs) > 0 {
		return rs[0].Numerator, rs[0].Denominator, true
	}
	if rs, ok := v.([]exifcommon.SignedRational); ok && len(rs) > 0 {
		return uint32(rs[0].Numerator), uint32(rs[0].Denominator), true
	}
	return 0, 0, false
}

func firstRatF(v any) float64 {
	n, d, ok := firstRat(v)
	if !ok || d == 0 {
		return 0
	}
	return float64(n) / float64(d)
}

// formatExposure renders a shutter speed the way photographers read it.
func formatExposure(num, den uint32) string {
	if num == 0 {
		return "0"
	}
	secs := float64(num) / float64(den)
	if secs >= 1 {
		return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.1f", secs), "0"), ".") + "s"
	}
	return fmt.Sprintf("1/%d", int(math.Round(float64(den)/float64(num))))
}

// atoiSafe extracts the first run of decimal digits from s (dsoprea Formatted
// values look like "[6240]"), returning 0 if there are none.
func atoiSafe(s string) int {
	n, seen := 0, false
	for _, r := range s {
		if r >= '0' && r <= '9' {
			n = n*10 + int(r-'0')
			seen = true
		} else if seen {
			break
		}
	}
	return n
}
