package catalog

import (
	"encoding/json"
	"strings"
	"testing"
)

// The core invariant of the whole feature: a recipe typed in the editor (or
// imported from JSON) must hash exactly like the same recipe decoded from a
// camera file, or pairing silently breaks.
func TestFingerprintEditorMatchesDecoder(t *testing.T) {
	fn := mustParse(t, fixtureDSCF5230(t))
	decoded := fn.recipeFields()
	if decoded == nil {
		t.Fatal("recipeFields = nil")
	}

	// The import-file form of the same recipe: neutral-defaulting fields
	// (dRangePriority, WC/MG) deliberately omitted.
	var typed RecipeFields
	if err := json.Unmarshal([]byte(`{
		"filmSimulation": "Classic Negative", "dynamicRange": "DR200",
		"highlightTone": -2, "shadowTone": 0, "color": 4,
		"sharpness": 0, "noiseReduction": -4, "clarity": 0,
		"grainEffect": "Strong", "grainSize": "Small",
		"colorChrome": "Strong", "colorChromeFXBlue": "Off",
		"whiteBalance": "Kelvin", "colorTemperature": 4700,
		"wbShiftRed": 4, "wbShiftBlue": -2
	}`), &typed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	dfp, tfp := decoded.Fingerprint(), typed.Fingerprint()
	if dfp == "" || tfp == "" {
		t.Fatalf("empty fingerprint (decoded=%q typed=%q)", dfp, tfp)
	}
	if dfp != tfp {
		t.Errorf("decoded fingerprint %s != typed fingerprint %s", dfp, tfp)
	}
}

func TestFingerprintNormalizations(t *testing.T) {
	base := DefaultRecipeFields()

	// Color sim: WC/MG count as neutral whatever a bogus source claims.
	withWC := base
	withWC.MonochromaticWC = ptr(5)
	if base.Fingerprint() != withWC.Fingerprint() {
		t.Error("WC on a color sim must normalize to neutral")
	}

	// Mono sim: the Color scale doesn't exist there.
	acros := base
	acros.FilmSimulation = ptr("Acros")
	acrosColored := acros
	acrosColored.Color = ptr(3)
	if acros.Fingerprint() != acrosColored.Fingerprint() {
		t.Error("Color on a mono sim must normalize to neutral")
	}
	// ...but WC now matters.
	acrosWarm := acros
	acrosWarm.MonochromaticWC = ptr(2)
	if acros.Fingerprint() == acrosWarm.Fingerprint() {
		t.Error("WC on a mono sim must change the fingerprint")
	}

	// Grain Off implies size Off (one combined camera menu).
	offSmall := base
	offSmall.GrainSize = ptr("Small")
	if base.Fingerprint() != offSmall.Fingerprint() {
		t.Error("grain size with grain Off must normalize to Off")
	}
	weakSmall := base
	weakSmall.GrainEffect = ptr("Weak")
	weakSmall.GrainSize = ptr("Small")
	weakLarge := weakSmall
	weakLarge.GrainSize = ptr("Large")
	if weakSmall.Fingerprint() == weakLarge.Fingerprint() {
		t.Error("grain size with grain on must change the fingerprint")
	}

	// Color temperature only exists under Kelvin WB.
	warm := base
	warm.ColorTemperature = ptr(5600)
	if base.Fingerprint() != warm.Fingerprint() {
		t.Error("color temperature without Kelvin WB must be ignored")
	}
	kelvinA, kelvinB := base, base
	kelvinA.WhiteBalance = ptr("Kelvin")
	kelvinA.ColorTemperature = ptr(4700)
	kelvinB.WhiteBalance = ptr("Kelvin")
	kelvinB.ColorTemperature = ptr(5600)
	if kelvinA.Fingerprint() == kelvinB.Fingerprint() {
		t.Error("Kelvin temperature must change the fingerprint")
	}
}

func TestFingerprintIncomplete(t *testing.T) {
	var empty RecipeFields
	if fp := empty.Fingerprint(); fp != "" {
		t.Errorf("empty fields fingerprint = %q; want \"\"", fp)
	}
	missing := empty.MissingFields()
	if len(missing) == 0 {
		t.Fatal("empty fields must report missing fields")
	}

	// Kelvin WB without a temperature is incomplete; any other WB is not.
	partial := DefaultRecipeFields()
	partial.WhiteBalance = ptr("Kelvin")
	partial.ColorTemperature = nil
	if partial.Complete() {
		t.Error("Kelvin without colorTemperature must be incomplete")
	}
	if !strings.Contains(strings.Join(partial.MissingFields(), " "), "colorTemperature") {
		t.Errorf("missing fields %v must name colorTemperature", partial.MissingFields())
	}
}

// JSON "-0.0" decodes to a negative zero; the canonical serialization must not
// distinguish it from 0 or the same recipe hashes twice.
func TestFingerprintNegativeZero(t *testing.T) {
	var typed RecipeFields
	if err := json.Unmarshal([]byte(`{"highlightTone": -0.0, "shadowTone": 0}`), &typed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	a := DefaultRecipeFields()
	a.HighlightTone = typed.HighlightTone
	b := DefaultRecipeFields()
	if a.Fingerprint() != b.Fingerprint() {
		t.Error("negative-zero tone must hash like 0")
	}
}

func TestFingerprintHalfStepSerialization(t *testing.T) {
	a := DefaultRecipeFields()
	a.HighlightTone = ptr(0.5)
	b := DefaultRecipeFields()
	b.HighlightTone = ptr(1.0)
	if a.Fingerprint() == b.Fingerprint() {
		t.Error("half-step tones must hash distinctly")
	}
}

func TestValidate(t *testing.T) {
	ok := DefaultRecipeFields()
	if err := ok.Validate(); err != nil {
		t.Fatalf("defaults must validate: %v", err)
	}

	cases := []struct {
		name   string
		mutate func(*RecipeFields)
	}{
		{"unknown film simulation", func(f *RecipeFields) { f.FilmSimulation = ptr("classic chrome") }},
		{"bad dynamic range", func(f *RecipeFields) { f.DynamicRange = ptr("DR800") }},
		{"tone out of range", func(f *RecipeFields) { f.HighlightTone = ptr(4.5) }},
		{"tone off-step", func(f *RecipeFields) { f.ShadowTone = ptr(0.3) }},
		{"clarity out of range", func(f *RecipeFields) { f.Clarity = ptr(6) }},
		{"wb shift out of range", func(f *RecipeFields) { f.WBShiftRed = ptr(10) }},
		{"unknown grain size", func(f *RecipeFields) { f.GrainSize = ptr("Tiny") }},
		{"unknown white balance", func(f *RecipeFields) { f.WhiteBalance = ptr("Sunny") }},
		{"mono color out of range", func(f *RecipeFields) { f.MonochromaticWC = ptr(19) }},
		{"temperature out of range", func(f *RecipeFields) { f.ColorTemperature = ptr(20000) }},
		{"grain size Off with active effect", func(f *RecipeFields) {
			f.GrainEffect = ptr("Weak") // size stays "Off": no body produces that pair
		}},
	}
	for _, c := range cases {
		f := DefaultRecipeFields()
		c.mutate(&f)
		if err := f.Validate(); err == nil {
			t.Errorf("%s: Validate() = nil; want error", c.name)
		}
	}

	// Partial fields validate too (only PRESENT values are checked).
	var partial RecipeFields
	partial.FilmSimulation = ptr("Classic Chrome")
	if err := partial.Validate(); err != nil {
		t.Errorf("partial fields must validate: %v", err)
	}
}

// ExtractMetadata carries the recipe into the metadata and the fuji dump, so
// recomputes and editor prefill need no re-download.
func TestExtractMetadataCarriesRecipe(t *testing.T) {
	fn := mustParse(t, fixtureDSCF5230(t))
	r := fn.recipeFields()
	dump := fn.named()
	if dump["FilmSimulation"] != "Classic Negative" {
		t.Fatalf("dump FilmSimulation = %v", dump["FilmSimulation"])
	}
	if r.Fingerprint() == "" {
		t.Fatal("fixture must produce a fingerprint")
	}
}
