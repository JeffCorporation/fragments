package catalog

import (
	"bytes"
	"encoding/binary"
	"fmt"
)

// fujiMakerNote holds the decoded scalar tags of a Fujifilm maker note IFD.
type fujiMakerNote struct {
	ints map[uint16]uint32 // SHORT/LONG scalar values (first element)
	strs map[uint16]string // ASCII values
}

// parseFujiMakerNote decodes the Fujifilm maker note. The block begins with the
// ASCII magic "FUJIFILM" followed by a little-endian 4-byte offset (relative to
// the magic) to a standard TIFF IFD. All values/offsets in the IFD are also
// relative to the magic. We only need scalar SHORT/LONG/ASCII tags, which is
// enough for the film simulation and a useful dump of Fuji settings.
func parseFujiMakerNote(raw []byte) (*fujiMakerNote, error) {
	idx := bytes.Index(raw, []byte("FUJIFILM"))
	if idx < 0 {
		return nil, fmt.Errorf("not a fujifilm maker note")
	}
	b := raw[idx:]
	if len(b) < 12 {
		return nil, fmt.Errorf("maker note too short")
	}
	le := binary.LittleEndian
	ifdOff := le.Uint32(b[8:12])
	if uint64(ifdOff)+2 > uint64(len(b)) { // widen to avoid 32-bit int overflow
		return nil, fmt.Errorf("bad ifd offset")
	}
	count := int(le.Uint16(b[ifdOff:]))
	entryBase := int(ifdOff) + 2

	fn := &fujiMakerNote{ints: map[uint16]uint32{}, strs: map[uint16]string{}}
	for i := 0; i < count; i++ {
		off := entryBase + i*12
		if off+12 > len(b) {
			break
		}
		tagID := le.Uint16(b[off:])
		typ := le.Uint16(b[off+2:])
		cnt := le.Uint32(b[off+4:])
		valField := b[off+8 : off+12]

		switch typ {
		case 3: // SHORT
			fn.ints[tagID] = uint32(le.Uint16(valField))
		case 4: // LONG
			fn.ints[tagID] = le.Uint32(valField)
		case 2: // ASCII
			fn.strs[tagID] = readASCII(b, le, valField, cnt)
		}
	}
	return fn, nil
}

// readASCII reads a (possibly out-of-line) ASCII value.
func readASCII(b []byte, le binary.ByteOrder, valField []byte, cnt uint32) string {
	var data []byte
	if cnt <= 4 {
		data = valField[:cnt]
	} else {
		o := le.Uint32(valField)
		if uint64(o)+uint64(cnt) > uint64(len(b)) { // widen to avoid 32-bit int overflow
			return ""
		}
		data = b[o : o+cnt]
	}
	return string(bytes.TrimRight(data, "\x00"))
}

// Fujifilm maker note tag IDs we surface by name.
const (
	fujiVersion           = 0x0000
	fujiSerial            = 0x0010
	fujiQuality           = 0x1000
	fujiSharpness         = 0x1001
	fujiWhiteBalance      = 0x1002
	fujiSaturation        = 0x1003 // also encodes B&W / Acros film sims
	fujiNoiseReduction    = 0x100e
	fujiClarity           = 0x100f
	fujiDynamicRange      = 0x1400
	fujiFilmMode          = 0x1401 // color film simulations
	fujiDynamicRangeSet   = 0x1402
	fujiDevDynamicRange   = 0x140b
	fujiGrainEffect       = 0x1047
	fujiColorChromeEffect = 0x1048
	fujiRating            = 0x1431
)

// filmModeNames maps FilmMode (0x1401) values to Fujifilm film simulations
// (ExifTool's FujiFilm FilmMode table).
var filmModeNames = map[uint32]string{
	0x000: "Provia/Standard",
	0x100: "Studio Portrait",
	0x110: "Studio Portrait Enhanced Saturation",
	0x120: "Astia/Soft",
	0x130: "Studio Portrait Increased Sharpness",
	0x200: "Fujichrome/Velvia",
	0x300: "Studio Portrait Ex",
	0x400: "Velvia/Vivid",
	0x500: "Pro Neg. Std",
	0x501: "Pro Neg. Hi",
	0x600: "Classic Chrome",
	0x700: "Eterna/Cinema",
	0x800: "Classic Negative",
	0x900: "Bleach Bypass/Eterna Bleach Bypass",
	0xa00: "Nostalgic Neg",
	0xb00: "Reala ACE",
}

// saturationFilmSim maps Saturation (0x1003) values that actually denote a
// monochrome film simulation rather than a saturation level.
var saturationFilmSim = map[uint32]string{
	0x300: "Monochrome",
	0x301: "Monochrome + R Filter",
	0x302: "Monochrome + Ye Filter",
	0x303: "Monochrome + G Filter",
	0x310: "Sepia",
	0x500: "Acros",
	0x501: "Acros + R Filter",
	0x502: "Acros + Ye Filter",
	0x503: "Acros + G Filter",
}

// filmSimulation derives a human-readable film simulation name. B&W/Acros are
// encoded in Saturation (0x1003); everything else in FilmMode (0x1401).
func (fn *fujiMakerNote) filmSimulation() string {
	if sat, ok := fn.ints[fujiSaturation]; ok {
		if name, ok := saturationFilmSim[sat]; ok {
			return name
		}
	}
	if fm, ok := fn.ints[fujiFilmMode]; ok {
		if name, ok := filmModeNames[fm]; ok {
			return name
		}
		return fmt.Sprintf("FilmMode(0x%x)", fm)
	}
	return ""
}

var fujiTagNames = map[uint16]string{
	fujiVersion:           "Version",
	fujiSerial:            "InternalSerialNumber",
	fujiQuality:           "Quality",
	fujiSharpness:         "Sharpness",
	fujiWhiteBalance:      "WhiteBalance",
	fujiSaturation:        "Saturation",
	fujiNoiseReduction:    "NoiseReduction",
	fujiClarity:           "Clarity",
	fujiDynamicRange:      "DynamicRange",
	fujiFilmMode:          "FilmMode",
	fujiDynamicRangeSet:   "DynamicRangeSetting",
	fujiDevDynamicRange:   "DevelopmentDynamicRange",
	fujiGrainEffect:       "GrainEffect",
	fujiColorChromeEffect: "ColorChromeEffect",
	fujiRating:            "Rating",
}

// named returns the recognized Fuji tags keyed by human name, for the raw dump.
func (fn *fujiMakerNote) named() map[string]any {
	out := map[string]any{}
	for id, name := range fujiTagNames {
		if v, ok := fn.ints[id]; ok {
			out[name] = v
		}
		if v, ok := fn.strs[id]; ok {
			out[name] = v
		}
	}
	if fs := fn.filmSimulation(); fs != "" {
		out["FilmSimulation"] = fs
	}
	return out
}
