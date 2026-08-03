package catalog

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"strconv"
)

// fujiMakerNote holds the decoded tags of a Fujifilm maker note IFD. Integer
// values keep every element and their sign (WhiteBalanceFineTune is a signed
// pair; the tone tags are signed scalars), ASCII values are plain strings.
type fujiMakerNote struct {
	ints map[uint16][]int64 // BYTE/SHORT/LONG/SBYTE/SSHORT/SLONG values, all elements
	strs map[uint16]string  // ASCII values
}

// tiffIntSizes maps the TIFF integer types we decode to their element size.
// Type 2 (ASCII) is handled separately; UNDEFINED/RATIONAL are skipped (no
// recipe field uses them).
var tiffIntSizes = map[uint16]uint32{
	1: 1, // BYTE   (u8)
	3: 2, // SHORT  (u16)
	4: 4, // LONG   (u32)
	6: 1, // SBYTE  (i8)
	8: 2, // SSHORT (i16)
	9: 4, // SLONG  (i32)
}

// parseFujiMakerNote decodes the Fujifilm maker note. The block begins with the
// ASCII magic "FUJIFILM" followed by a little-endian 4-byte offset (relative to
// the magic) to a standard TIFF IFD. All values/offsets in the IFD are also
// relative to the magic, and everything is little-endian regardless of the
// enclosing TIFF byte order (ExifTool hardcodes it for this maker).
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

	fn := &fujiMakerNote{ints: map[uint16][]int64{}, strs: map[uint16]string{}}
	for i := 0; i < count; i++ {
		off := entryBase + i*12
		if off+12 > len(b) {
			break
		}
		tagID := le.Uint16(b[off:])
		typ := le.Uint16(b[off+2:])
		cnt := le.Uint32(b[off+4:])
		valField := b[off+8 : off+12]

		if typ == 2 {
			if cnt > 4096 { // corrupt count guard: real Fuji strings are tens of bytes
				continue
			}
			fn.strs[tagID] = readASCII(b, le, valField, cnt)
			continue
		}
		size, ok := tiffIntSizes[typ]
		if !ok || cnt == 0 || cnt > 64 { // 64 caps corrupt counts; real Fuji tags are 1-2 elements
			continue
		}
		data := valField
		if size*cnt > 4 { // out-of-line, offset relative to the maker note start
			o := le.Uint32(valField)
			if uint64(o)+uint64(size)*uint64(cnt) > uint64(len(b)) {
				continue
			}
			data = b[o : o+size*cnt]
		}
		vals := make([]int64, cnt)
		for j := uint32(0); j < cnt; j++ {
			el := data[j*size:]
			switch typ {
			case 1:
				vals[j] = int64(el[0])
			case 3:
				vals[j] = int64(le.Uint16(el))
			case 4:
				vals[j] = int64(le.Uint32(el))
			case 6:
				vals[j] = int64(int8(el[0]))
			case 8:
				vals[j] = int64(int16(le.Uint16(el)))
			case 9:
				vals[j] = int64(int32(le.Uint32(el)))
			}
		}
		fn.ints[tagID] = vals
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

// first returns the first element of an integer tag.
func (fn *fujiMakerNote) first(tag uint16) (int64, bool) {
	if vs, ok := fn.ints[tag]; ok && len(vs) > 0 {
		return vs[0], true
	}
	return 0, false
}

// pair returns the first two elements of an integer tag (WhiteBalanceFineTune
// is a signed [R, B] pair).
func (fn *fujiMakerNote) pair(tag uint16) (a, b int64, ok bool) {
	if vs, ok := fn.ints[tag]; ok && len(vs) >= 2 {
		return vs[0], vs[1], true
	}
	return 0, 0, false
}

// Fujifilm maker note tag IDs we surface by name.
const (
	fujiVersion             = 0x0000
	fujiSerial              = 0x0010
	fujiQuality             = 0x1000
	fujiSharpness           = 0x1001
	fujiWhiteBalance        = 0x1002
	fujiSaturation          = 0x1003 // also encodes B&W / Acros film sims
	fujiColorTemperature    = 0x1005
	fujiWBFineTune          = 0x100a // signed [R, B] pair, UI = raw / 20
	fujiNoiseReduction      = 0x100e // "High ISO NR", the recipe field (0x100b is legacy)
	fujiClarity             = 0x100f // signed, UI = raw / 1000
	fujiShadowTone          = 0x1040 // signed, UI = -raw / 16
	fujiHighlightTone       = 0x1041 // signed, UI = -raw / 16
	fujiGrainEffect         = 0x1047 // roughness: Off/Weak/Strong
	fujiColorChromeEffect   = 0x1048
	fujiBWAdjustment        = 0x1049 // monochromatic color WC (+ warm / - cool)
	fujiBWMagentaGreen      = 0x104b // monochromatic color MG (+ green / - magenta)
	fujiGrainEffectSize     = 0x104c // Off/Small/Large
	fujiColorChromeFXBlue   = 0x104e
	fujiDynamicRange        = 0x1400
	fujiFilmMode            = 0x1401 // color film simulations
	fujiDynamicRangeSet     = 0x1402
	fujiDevDynamicRange     = 0x1403 // literal 100/200/400, valid when 0x1402 = Manual
	fujiAutoDynamicRange    = 0x140b // percent chosen by DR Auto for THIS shot (not a recipe field)
	fujiRating              = 0x1431
	fujiDRangePriority      = 0x1443 // 0 Auto, 1 Fixed (absent = Off)
	fujiDRangePriorityAuto  = 0x1444 // 1 Weak, 2 Strong, 3 Plus
	fujiDRangePriorityFixed = 0x1445 // 1 Weak, 2 Strong
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

// sharpnessValues maps Sharpness (0x1001) to its UI value. The scale is a
// non-linear enum, NOT an offset scale (ExifTool FujiFilm.pm:126-143).
var sharpnessValues = map[int64]int{
	0x00: -4, 0x01: -3, 0x02: -2, 0x82: -1,
	0x03: 0,
	0x84: 1, 0x04: 2, 0x05: 3, 0x06: 4,
}

// saturationValues maps the non-monochrome Saturation (0x1003) values to the
// UI "Color" scale. Non-monotonic enum (ExifTool FujiFilm.pm:171-197).
var saturationValues = map[int64]int{
	0x000: 0,
	0x080: 1, 0x100: 2, 0x0c0: 3, 0x0e0: 4,
	0x180: -1, 0x400: -2, 0x4c0: -3, 0x4e0: -4,
}

// noiseReductionValues maps NoiseReduction / High ISO NR (0x100e) to its UI
// value (ExifTool FujiFilm.pm:244-259).
var noiseReductionValues = map[int64]int{
	0x000: 0,
	0x180: 1, 0x100: 2, 0x1c0: 3, 0x1e0: 4,
	0x280: -1, 0x200: -2, 0x2c0: -3, 0x2e0: -4,
}

// whiteBalanceNames maps WhiteBalance (0x1002) to its menu name (ExifTool
// FujiFilm.pm:144-169). 0xf00 is Custom and 0xff0 is Kelvin — several public
// parsers swap these; the fixture-verified values are the ones below.
var whiteBalanceNames = map[uint32]string{
	0x000: "Auto",
	0x001: "Auto (white priority)",
	0x002: "Auto (ambiance priority)",
	0x100: "Daylight",
	0x200: "Cloudy",
	0x300: "Daylight Fluorescent",
	0x301: "Day White Fluorescent",
	0x302: "White Fluorescent",
	0x303: "Warm White Fluorescent",
	0x304: "Living Room Warm White Fluorescent",
	0x400: "Incandescent",
	0x500: "Flash",
	0x600: "Underwater",
	0xf00: "Custom",
	0xf01: "Custom2",
	0xf02: "Custom3",
	0xf03: "Custom4",
	0xf04: "Custom5",
	0xff0: "Kelvin",
}

// strengthNames maps the shared Off/Weak/Strong scale of GrainEffect (0x1047),
// ColorChromeEffect (0x1048) and ColorChromeFXBlue (0x104e).
var strengthNames = map[uint32]string{
	0:  "Off",
	32: "Weak",
	64: "Strong",
}

// grainSizeNames maps GrainEffectSize (0x104c). Note the scale differs from
// the roughness one: 16 Small / 32 Large.
var grainSizeNames = map[uint32]string{
	0:  "Off",
	16: "Small",
	32: "Large",
}

// dynamicRangeNames maps DynamicRange (0x1400).
var dynamicRangeNames = map[uint32]string{
	1: "Standard",
	3: "Wide",
}

// dynamicRangeSettingNames maps DynamicRangeSetting (0x1402).
var dynamicRangeSettingNames = map[uint32]string{
	0x0000: "Auto",
	0x0001: "Manual",
	0x0100: "Standard (100%)",
	0x0200: "Wide1 (230%)",
	0x0201: "Wide2 (400%)",
	0x8000: "Film Simulation",
}

// filmSimulation derives a human-readable film simulation name. B&W/Acros are
// encoded in Saturation (0x1003); everything else in FilmMode (0x1401).
func (fn *fujiMakerNote) filmSimulation() string {
	if sat, ok := fn.first(fujiSaturation); ok {
		if name, ok := saturationFilmSim[uint32(sat)]; ok {
			return name
		}
	}
	if fm, ok := fn.first(fujiFilmMode); ok {
		if name, ok := filmModeNames[uint32(fm)]; ok {
			return name
		}
		return fmt.Sprintf("FilmMode(0x%x)", fm)
	}
	return ""
}

// signedLabel renders a UI scale value the way the camera menus print it:
// explicit sign, bare zero ("+2", "0", "-1", "+0.5").
func signedLabel(v float64) string {
	s := strconv.FormatFloat(v, 'f', -1, 64)
	if v > 0 {
		return "+" + s
	}
	return s
}

// toneValue converts a raw HighlightTone/ShadowTone to its UI value: the sign
// is inverted and ±8 raw steps are ±0.5 UI steps (newer bodies have half
// steps, so the value is fractional).
func toneValue(raw int64) float64 { return float64(-raw) / 16 }

// wbShiftValue converts a raw WhiteBalanceFineTune component to its UI value
// (current bodies store UI×20; rounding guards odd raws on older ones).
func wbShiftValue(raw int64) int {
	if raw >= 0 {
		return int((raw + 10) / 20)
	}
	return -int((-raw + 10) / 20)
}

// enumLabel resolves v in table, falling back to "Name(0xNN)" so unknown
// values from newer bodies stay visible instead of being masked.
func enumLabel(table map[uint32]string, name string, v int64) string {
	if s, ok := table[uint32(v)]; ok {
		return s
	}
	return fmt.Sprintf("%s(0x%x)", name, v)
}

// scaleLabel resolves v in a raw→UI integer scale table, with the same
// fallback convention as enumLabel.
func scaleLabel(table map[int64]int, name string, v int64) string {
	if ui, ok := table[v]; ok {
		return signedLabel(float64(ui))
	}
	return fmt.Sprintf("%s(0x%x)", name, v)
}

// named returns the recognized Fuji tags keyed by human name, decoded to the
// labels the camera menus use (the raw ints are useless in a recipe library).
// Unknown enum values keep the "Name(0xNN)" fallback.
func (fn *fujiMakerNote) named() map[string]any {
	out := map[string]any{}

	if v, ok := fn.strs[fujiVersion]; ok {
		out["Version"] = v
	}
	if v, ok := fn.strs[fujiSerial]; ok {
		out["InternalSerialNumber"] = v
	}
	if v, ok := fn.strs[fujiQuality]; ok {
		out["Quality"] = v
	}

	if v, ok := fn.first(fujiSharpness); ok {
		switch v { // §2.1: two non-scale sentinels sit alongside the ±N enum
		case 0x8000:
			out["Sharpness"] = "Film Simulation"
		case 0xffff:
			out["Sharpness"] = "n/a"
		default:
			out["Sharpness"] = scaleLabel(sharpnessValues, "Sharpness", v)
		}
	}
	if v, ok := fn.first(fujiWhiteBalance); ok {
		out["WhiteBalance"] = enumLabel(whiteBalanceNames, "WhiteBalance", v)
	}
	if v, ok := fn.first(fujiSaturation); ok {
		if name, mono := saturationFilmSim[uint32(v)]; mono {
			out["Saturation"] = name
		} else {
			switch v { // §2.3: legacy/bracket values outside the ±N scale
			case 0x200:
				out["Saturation"] = "Low"
			case 0x8000:
				out["Saturation"] = "Film Simulation"
			default:
				out["Saturation"] = scaleLabel(saturationValues, "Saturation", v)
			}
		}
	}
	if v, ok := fn.first(fujiColorTemperature); ok {
		out["ColorTemperature"] = fmt.Sprintf("%d K", v)
	}
	if r, b, ok := fn.pair(fujiWBFineTune); ok {
		out["WhiteBalanceFineTune"] = fmt.Sprintf("R%s / B%s",
			signedLabel(float64(wbShiftValue(r))), signedLabel(float64(wbShiftValue(b))))
	}
	if v, ok := fn.first(fujiNoiseReduction); ok {
		out["NoiseReduction"] = scaleLabel(noiseReductionValues, "NoiseReduction", v)
	}
	if v, ok := fn.first(fujiClarity); ok {
		out["Clarity"] = signedLabel(float64(v) / 1000)
	}
	if v, ok := fn.first(fujiShadowTone); ok {
		out["ShadowTone"] = signedLabel(toneValue(v))
	}
	if v, ok := fn.first(fujiHighlightTone); ok {
		out["HighlightTone"] = signedLabel(toneValue(v))
	}
	if v, ok := fn.first(fujiGrainEffect); ok {
		out["GrainEffect"] = enumLabel(strengthNames, "GrainEffect", v)
	}
	if v, ok := fn.first(fujiGrainEffectSize); ok {
		out["GrainEffectSize"] = enumLabel(grainSizeNames, "GrainEffectSize", v)
	}
	if v, ok := fn.first(fujiColorChromeEffect); ok {
		out["ColorChromeEffect"] = enumLabel(strengthNames, "ColorChromeEffect", v)
	}
	if v, ok := fn.first(fujiColorChromeFXBlue); ok {
		out["ColorChromeFXBlue"] = enumLabel(strengthNames, "ColorChromeFXBlue", v)
	}
	if v, ok := fn.first(fujiBWAdjustment); ok {
		out["MonochromaticColorWC"] = signedLabel(float64(v))
	}
	if v, ok := fn.first(fujiBWMagentaGreen); ok {
		out["MonochromaticColorMG"] = signedLabel(float64(v))
	}
	if v, ok := fn.first(fujiDynamicRange); ok {
		out["DynamicRange"] = enumLabel(dynamicRangeNames, "DynamicRange", v)
	}
	if v, ok := fn.first(fujiFilmMode); ok {
		out["FilmMode"] = enumLabel(filmModeNames, "FilmMode", v)
	}
	if v, ok := fn.first(fujiDynamicRangeSet); ok {
		out["DynamicRangeSetting"] = enumLabel(dynamicRangeSettingNames, "DynamicRangeSetting", v)
	}
	if v, ok := fn.first(fujiDevDynamicRange); ok {
		out["DevelopmentDynamicRange"] = int(v)
	}
	if v, ok := fn.first(fujiAutoDynamicRange); ok {
		out["AutoDynamicRange"] = fmt.Sprintf("%d%%", v)
	}
	if s := fn.dRangePriority(); s != "" && s != "Off" {
		out["DRangePriority"] = s
	}
	if v, ok := fn.first(fujiRating); ok {
		out["Rating"] = int(v)
	}

	if fs := fn.filmSimulation(); fs != "" {
		out["FilmSimulation"] = fs
	}
	return out
}

// dRangePriority folds the three D-Range Priority tags (X-T3 and later) into
// the single menu value: Off (absent), Auto, Weak or Strong. When active, the
// camera drives tone itself, but the recipe fingerprint still needs the state.
func (fn *fujiMakerNote) dRangePriority() string {
	mode, ok := fn.first(fujiDRangePriority)
	if !ok {
		return "Off"
	}
	switch mode {
	case 0: // Auto: 0x1444 records what auto picked per shot; the SETTING is "Auto"
		return "Auto"
	case 1:
		if v, ok := fn.first(fujiDRangePriorityFixed); ok {
			switch v {
			case 1:
				return "Weak"
			case 2:
				return "Strong"
			}
		}
		return "Fixed" // fixed mode without a recorded strength: keep it distinct
	}
	return fmt.Sprintf("DRangePriority(0x%x)", mode)
}

// recipeDynamicRange folds the DR tag pair into the recipe value: the user
// either fixed DR (Manual on current bodies, Standard/Wide on early ones — the
// actual percentage sits in DevelopmentDynamicRange 0x1403) or left the camera
// to choose ("Auto"; what auto picked per shot is 0x140b, a shooting fact that
// stays OUT of the recipe).
func (fn *fujiMakerNote) recipeDynamicRange() string {
	set, ok := fn.first(fujiDynamicRangeSet)
	if !ok {
		return "Auto"
	}
	switch set {
	case 0x0001, 0x0100, 0x0200, 0x0201: // user-fixed DR
		if dev, ok := fn.first(fujiDevDynamicRange); ok && dev > 0 {
			return fmt.Sprintf("DR%d", dev)
		}
		switch set {
		case 0x0100:
			return "DR100" // Standard is 100% by definition
		case 0x0201:
			return "DR400" // Wide2 is 400% by definition
		}
		return "Auto" // Manual or Wide1 (230%, no DR bucket) without a recorded value
	default: // Auto, Film Simulation, unknown
		return "Auto"
	}
}
