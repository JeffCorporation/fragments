package catalog

import (
	"encoding/binary"
	"testing"
)

// mnEntry is one synthetic maker note IFD entry.
type mnEntry struct {
	tag  uint16
	typ  uint16
	ints []int64
	str  string
}

var tiffTestSizes = map[uint16]int{1: 1, 3: 2, 4: 4, 6: 1, 8: 2, 9: 4}

// buildMakerNote assembles a little-endian Fujifilm maker note ("FUJIFILM"
// magic, IFD pointer 0x0C, out-of-line offsets relative to the magic — the
// exact container the X100VI fixtures exhibit).
func buildMakerNote(t *testing.T, entries []mnEntry) []byte {
	t.Helper()
	le := binary.LittleEndian
	n := len(entries)
	buf := make([]byte, 0, 512)
	buf = append(buf, []byte("FUJIFILM")...)
	buf = le.AppendUint32(buf, 12)
	buf = le.AppendUint16(buf, uint16(n))
	dataOff := 12 + 2 + 12*n
	var tail []byte
	for _, e := range entries {
		buf = le.AppendUint16(buf, e.tag)
		buf = le.AppendUint16(buf, e.typ)
		var raw []byte
		if e.typ == 2 {
			raw = append([]byte(e.str), 0)
			buf = le.AppendUint32(buf, uint32(len(raw)))
		} else {
			sz, ok := tiffTestSizes[e.typ]
			if !ok {
				t.Fatalf("unsupported test type %d", e.typ)
			}
			for _, v := range e.ints {
				switch sz {
				case 1:
					raw = append(raw, byte(v))
				case 2:
					raw = le.AppendUint16(raw, uint16(v))
				case 4:
					raw = le.AppendUint32(raw, uint32(v))
				}
			}
			buf = le.AppendUint32(buf, uint32(len(e.ints)))
		}
		if len(raw) <= 4 {
			v := make([]byte, 4)
			copy(v, raw)
			buf = append(buf, v...)
		} else {
			buf = le.AppendUint32(buf, uint32(dataOff+len(tail)))
			tail = append(tail, raw...)
		}
	}
	return append(buf, tail...)
}

// fixtureDSCF5230 rebuilds the raw recipe tags of the first X100VI ground-truth
// fixture (fujifilm-exif.md, Appendix A).
func fixtureDSCF5230(t *testing.T) []byte {
	return buildMakerNote(t, []mnEntry{
		{tag: 0x1001, typ: 3, ints: []int64{3}},        // Sharpness → 0
		{tag: 0x1002, typ: 3, ints: []int64{0xFF0}},    // WhiteBalance → Kelvin
		{tag: 0x1003, typ: 3, ints: []int64{0xE0}},     // Saturation → +4
		{tag: 0x1005, typ: 3, ints: []int64{4700}},     // ColorTemperature
		{tag: 0x100A, typ: 9, ints: []int64{80, -40}},  // WBFineTune → R+4 / B-2 (out-of-line pair)
		{tag: 0x100B, typ: 3, ints: []int64{0x100}},    // legacy NR — not a recipe field
		{tag: 0x100E, typ: 3, ints: []int64{0x2E0}},    // NoiseReduction → -4
		{tag: 0x100F, typ: 9, ints: []int64{0}},        // Clarity → 0
		{tag: 0x1040, typ: 9, ints: []int64{0}},        // ShadowTone → 0
		{tag: 0x1041, typ: 9, ints: []int64{32}},       // HighlightTone → -2
		{tag: 0x1047, typ: 9, ints: []int64{64}},       // GrainEffect → Strong
		{tag: 0x1048, typ: 9, ints: []int64{64}},       // ColorChrome → Strong
		{tag: 0x104C, typ: 3, ints: []int64{16}},       // GrainSize → Small
		{tag: 0x104E, typ: 9, ints: []int64{0}},        // FXBlue → Off
		{tag: 0x1400, typ: 3, ints: []int64{1}},        // DynamicRange → Standard
		{tag: 0x1401, typ: 3, ints: []int64{0x800}},    // FilmMode → Classic Negative
		{tag: 0x1402, typ: 3, ints: []int64{1}},        // DynamicRangeSetting → Manual
		{tag: 0x1403, typ: 3, ints: []int64{200}},      // DevelopmentDynamicRange → DR200
		{tag: 0x1447, typ: 2, str: "X100VI_0100"},      // FujiModel (ASCII, out-of-line)
	})
}

// fixtureDSCF5358 rebuilds the second ground-truth fixture.
func fixtureDSCF5358(t *testing.T) []byte {
	return buildMakerNote(t, []mnEntry{
		{tag: 0x1001, typ: 3, ints: []int64{3}},       // Sharpness → 0
		{tag: 0x1002, typ: 3, ints: []int64{0xFF0}},   // Kelvin
		{tag: 0x1003, typ: 3, ints: []int64{0xC0}},    // Saturation → +3
		{tag: 0x1005, typ: 3, ints: []int64{5600}},    // ColorTemperature
		{tag: 0x100A, typ: 9, ints: []int64{20, 20}},  // → R+1 / B+1
		{tag: 0x100E, typ: 3, ints: []int64{0x2E0}},   // → -4
		{tag: 0x100F, typ: 9, ints: []int64{2000}},    // Clarity → +2
		{tag: 0x1040, typ: 9, ints: []int64{-16}},     // ShadowTone → +1 (sign inverted)
		{tag: 0x1041, typ: 9, ints: []int64{32}},      // HighlightTone → -2
		{tag: 0x1047, typ: 9, ints: []int64{32}},      // GrainEffect → Weak
		{tag: 0x1048, typ: 9, ints: []int64{64}},      // ColorChrome → Strong
		{tag: 0x104C, typ: 3, ints: []int64{32}},      // GrainSize → Large
		{tag: 0x104E, typ: 9, ints: []int64{0}},       // FXBlue → Off
		{tag: 0x100B, typ: 3, ints: []int64{0x100}},   // legacy NR — not a recipe field
		{tag: 0x1400, typ: 3, ints: []int64{1}},       // Standard
		{tag: 0x1401, typ: 3, ints: []int64{0x600}},   // Classic Chrome
		{tag: 0x1402, typ: 3, ints: []int64{1}},       // Manual
		{tag: 0x1403, typ: 3, ints: []int64{200}},     // DR200
		{tag: 0x1447, typ: 2, str: "X100VI_0100"},     // FujiModel (ASCII, out-of-line)
	})
}

func mustParse(t *testing.T, raw []byte) *fujiMakerNote {
	t.Helper()
	fn, err := parseFujiMakerNote(raw)
	if err != nil {
		t.Fatalf("parse maker note: %v", err)
	}
	return fn
}

func TestDecodeFixtureDSCF5230(t *testing.T) {
	fn := mustParse(t, fixtureDSCF5230(t))

	if got := fn.filmSimulation(); got != "Classic Negative" {
		t.Errorf("film simulation = %q; want Classic Negative", got)
	}
	r := fn.recipeFields()
	if r == nil {
		t.Fatal("recipeFields = nil")
	}
	checks := []struct {
		name string
		got  any
		want any
	}{
		{"filmSimulation", *r.FilmSimulation, "Classic Negative"},
		{"dynamicRange", *r.DynamicRange, "DR200"},
		{"dRangePriority", *r.DRangePriority, "Off"},
		{"highlightTone", *r.HighlightTone, -2.0},
		{"shadowTone", *r.ShadowTone, 0.0},
		{"color", *r.Color, 4},
		{"sharpness", *r.Sharpness, 0},
		{"noiseReduction", *r.NoiseReduction, -4},
		{"clarity", *r.Clarity, 0},
		{"grainEffect", *r.GrainEffect, "Strong"},
		{"grainSize", *r.GrainSize, "Small"},
		{"colorChrome", *r.ColorChrome, "Strong"},
		{"colorChromeFXBlue", *r.ColorChromeFXBlue, "Off"},
		{"whiteBalance", *r.WhiteBalance, "Kelvin"},
		{"colorTemperature", *r.ColorTemperature, 4700},
		{"wbShiftRed", *r.WBShiftRed, 4},
		{"wbShiftBlue", *r.WBShiftBlue, -2},
		{"monochromaticWC", *r.MonochromaticWC, 0},
		{"monochromaticMG", *r.MonochromaticMG, 0},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %v; want %v", c.name, c.got, c.want)
		}
	}
	if fp := r.Fingerprint(); fp == "" {
		t.Error("fingerprint must not be empty for a complete decode")
	}
}

func TestDecodeFixtureDSCF5358(t *testing.T) {
	fn := mustParse(t, fixtureDSCF5358(t))

	r := fn.recipeFields()
	if r == nil {
		t.Fatal("recipeFields = nil")
	}
	checks := []struct {
		name string
		got  any
		want any
	}{
		{"filmSimulation", *r.FilmSimulation, "Classic Chrome"},
		{"dynamicRange", *r.DynamicRange, "DR200"},
		{"highlightTone", *r.HighlightTone, -2.0},
		{"shadowTone", *r.ShadowTone, 1.0},
		{"color", *r.Color, 3},
		{"clarity", *r.Clarity, 2},
		{"grainEffect", *r.GrainEffect, "Weak"},
		{"grainSize", *r.GrainSize, "Large"},
		{"colorTemperature", *r.ColorTemperature, 5600},
		{"wbShiftRed", *r.WBShiftRed, 1},
		{"wbShiftBlue", *r.WBShiftBlue, 1},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %v; want %v", c.name, c.got, c.want)
		}
	}

	named := fn.named()
	labels := map[string]any{
		"Sharpness":            "0",
		"WhiteBalance":         "Kelvin",
		"ColorTemperature":     "5600 K",
		"WhiteBalanceFineTune": "R+1 / B+1",
		"NoiseReduction":       "-4",
		"Clarity":              "+2",
		"ShadowTone":           "+1",
		"HighlightTone":        "-2",
		"GrainEffect":          "Weak",
		"GrainEffectSize":      "Large",
		"ColorChromeEffect":    "Strong",
		"ColorChromeFXBlue":    "Off",
		"DynamicRange":         "Standard",
		"FilmMode":             "Classic Chrome",
		"DynamicRangeSetting":  "Manual",
		"DevelopmentDynamicRange": 200,
		"Saturation":           "+3",
		"FilmSimulation":       "Classic Chrome",
	}
	for k, want := range labels {
		if got, ok := named[k]; !ok || got != want {
			t.Errorf("named[%s] = %v (present=%v); want %v", k, got, ok, want)
		}
	}
}

// The monochromatic color tags only exist on B&W/Acros shots; both plausible
// on-wire types (SBYTE per ExifTool, SLONG as a defensive alternative) must
// decode to the same signed values.
func TestDecodeAcrosMonochromaticColor(t *testing.T) {
	for _, typ := range []uint16{6, 9} {
		fn := mustParse(t, buildMakerNote(t, []mnEntry{
			{tag: 0x1003, typ: 3, ints: []int64{0x500}}, // Acros
			{tag: 0x1049, typ: typ, ints: []int64{-2}},  // WC cool
			{tag: 0x104B, typ: typ, ints: []int64{3}},   // MG green
		}))
		if got := fn.filmSimulation(); got != "Acros" {
			t.Fatalf("type %d: film simulation = %q; want Acros", typ, got)
		}
		r := fn.recipeFields()
		if r == nil {
			t.Fatalf("type %d: recipeFields = nil", typ)
		}
		if *r.MonochromaticWC != -2 || *r.MonochromaticMG != 3 {
			t.Errorf("type %d: WC/MG = %d/%d; want -2/3", typ, *r.MonochromaticWC, *r.MonochromaticMG)
		}
		if *r.Color != 0 {
			t.Errorf("type %d: color = %d on a mono sim; want 0", typ, *r.Color)
		}
		named := fn.named()
		if named["MonochromaticColorWC"] != "-2" || named["MonochromaticColorMG"] != "+3" {
			t.Errorf("type %d: named WC/MG = %v/%v", typ, named["MonochromaticColorWC"], named["MonochromaticColorMG"])
		}
		if named["Saturation"] != "Acros" {
			t.Errorf("type %d: named Saturation = %v; want Acros", typ, named["Saturation"])
		}
	}
}

// Half-step tones (±8 raw) must survive as fractional UI values.
func TestDecodeHalfStepTones(t *testing.T) {
	fn := mustParse(t, buildMakerNote(t, []mnEntry{
		{tag: 0x1401, typ: 3, ints: []int64{0x000}}, // Provia
		{tag: 0x1040, typ: 9, ints: []int64{-8}},    // ShadowTone → +0.5
		{tag: 0x1041, typ: 9, ints: []int64{24}},    // HighlightTone → -1.5
	}))
	r := fn.recipeFields()
	if *r.ShadowTone != 0.5 || *r.HighlightTone != -1.5 {
		t.Errorf("tones = H%v S%v; want H-1.5 S0.5", *r.HighlightTone, *r.ShadowTone)
	}
	named := fn.named()
	if named["ShadowTone"] != "+0.5" || named["HighlightTone"] != "-1.5" {
		t.Errorf("named tones = H%v S%v; want H-1.5 S+0.5", named["HighlightTone"], named["ShadowTone"])
	}
}

func TestDynamicRangeDecision(t *testing.T) {
	cases := []struct {
		name    string
		entries []mnEntry
		want    string
	}{
		{"auto", []mnEntry{{tag: 0x1402, typ: 3, ints: []int64{0}}, {tag: 0x140B, typ: 3, ints: []int64{200}}}, "Auto"},
		{"manual DR400", []mnEntry{{tag: 0x1402, typ: 3, ints: []int64{1}}, {tag: 0x1403, typ: 3, ints: []int64{400}}}, "DR400"},
		{"absent", nil, "Auto"},
		{"film simulation mode", []mnEntry{{tag: 0x1402, typ: 3, ints: []int64{0x8000}}}, "Auto"},
		{"legacy standard", []mnEntry{{tag: 0x1402, typ: 3, ints: []int64{0x100}}}, "DR100"},
		{"legacy wide2", []mnEntry{{tag: 0x1402, typ: 3, ints: []int64{0x201}}}, "DR400"},
		{"legacy wide1 with dev", []mnEntry{{tag: 0x1402, typ: 3, ints: []int64{0x200}}, {tag: 0x1403, typ: 3, ints: []int64{200}}}, "DR200"},
	}
	for _, c := range cases {
		entries := append([]mnEntry{{tag: 0x1401, typ: 3, ints: []int64{0x600}}}, c.entries...)
		fn := mustParse(t, buildMakerNote(t, entries))
		if got := fn.recipeDynamicRange(); got != c.want {
			t.Errorf("%s: recipeDynamicRange = %q; want %q", c.name, got, c.want)
		}
	}
}

func TestDRangePriority(t *testing.T) {
	cases := []struct {
		name    string
		entries []mnEntry
		want    string
	}{
		{"absent", nil, "Off"},
		{"auto", []mnEntry{{tag: 0x1443, typ: 3, ints: []int64{0}}, {tag: 0x1444, typ: 3, ints: []int64{2}}}, "Auto"},
		{"fixed weak", []mnEntry{{tag: 0x1443, typ: 3, ints: []int64{1}}, {tag: 0x1445, typ: 3, ints: []int64{1}}}, "Weak"},
		{"fixed strong", []mnEntry{{tag: 0x1443, typ: 3, ints: []int64{1}}, {tag: 0x1445, typ: 3, ints: []int64{2}}}, "Strong"},
	}
	for _, c := range cases {
		fn := mustParse(t, buildMakerNote(t, c.entries))
		if got := fn.dRangePriority(); got != c.want {
			t.Errorf("%s: dRangePriority = %q; want %q", c.name, got, c.want)
		}
	}
}

// A maker note without any film simulation (non-Fuji rendering data) yields no
// recipe at all — the photo detail then shows no recipe line.
func TestNoFilmSimulationNoRecipe(t *testing.T) {
	fn := mustParse(t, buildMakerNote(t, []mnEntry{
		{tag: 0x1001, typ: 3, ints: []int64{3}},
	}))
	if r := fn.recipeFields(); r != nil {
		t.Fatalf("recipeFields = %+v; want nil without a film simulation", r)
	}
}

// Unknown enum values must stay visible via the Name(0xNN) fallback, never
// masked (new bodies invent values faster than the tables grow).
func TestUnknownEnumFallback(t *testing.T) {
	fn := mustParse(t, buildMakerNote(t, []mnEntry{
		{tag: 0x1401, typ: 3, ints: []int64{0xC00}},
		{tag: 0x1002, typ: 3, ints: []int64{0x777}},
	}))
	if got := fn.filmSimulation(); got != "FilmMode(0xc00)" {
		t.Errorf("film simulation = %q; want FilmMode(0xc00)", got)
	}
	named := fn.named()
	if named["WhiteBalance"] != "WhiteBalance(0x777)" {
		t.Errorf("named WhiteBalance = %v; want WhiteBalance(0x777)", named["WhiteBalance"])
	}
}
