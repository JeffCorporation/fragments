package catalog

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
)

// RecipeVersion tags every stored fingerprint with the field-list revision
// that produced it. Adding a field to the fingerprint changes every hash; the
// version says which photos rows still carry the old one and need a recompute.
const RecipeVersion = 1

// RecipeFields is the canonical, camera-menu vocabulary of a Fujifilm recipe:
// exactly the rendering fields, in the JSON shape of the import/export file.
// Every field is a pointer so an imported recipe can be PARTIAL (a documentary
// card with no matchable fingerprint); the decoder and the editor always
// produce complete values. Shooting facts (ISO, exposure compensation) are
// deliberately absent — they go in the recipe's free-form notes.
type RecipeFields struct {
	FilmSimulation    *string  `json:"filmSimulation,omitempty"`
	DynamicRange      *string  `json:"dynamicRange,omitempty"`   // "Auto" | "DR100" | "DR200" | "DR400"
	DRangePriority    *string  `json:"dRangePriority,omitempty"` // "Off" | "Auto" | "Weak" | "Strong"
	HighlightTone     *float64 `json:"highlightTone,omitempty"`  // -2..+4, half steps
	ShadowTone        *float64 `json:"shadowTone,omitempty"`     // -2..+4, half steps
	Color             *int     `json:"color,omitempty"`          // -4..+4 (0 on monochrome sims)
	Sharpness         *int     `json:"sharpness,omitempty"`      // -4..+4
	NoiseReduction    *int     `json:"noiseReduction,omitempty"` // -4..+4
	Clarity           *int     `json:"clarity,omitempty"`        // -5..+5
	GrainEffect       *string  `json:"grainEffect,omitempty"`    // Off | Weak | Strong
	GrainSize         *string  `json:"grainSize,omitempty"`      // Off | Small | Large
	ColorChrome       *string  `json:"colorChrome,omitempty"`    // Off | Weak | Strong
	ColorChromeFXBlue *string  `json:"colorChromeFXBlue,omitempty"`
	WhiteBalance      *string  `json:"whiteBalance,omitempty"`
	ColorTemperature  *int     `json:"colorTemperature,omitempty"` // Kelvin, only when WhiteBalance = "Kelvin"
	WBShiftRed        *int     `json:"wbShiftRed,omitempty"`       // -9..+9
	WBShiftBlue       *int     `json:"wbShiftBlue,omitempty"`      // -9..+9
	MonochromaticWC   *int     `json:"monochromaticWC,omitempty"`  // -18..+18, B&W/Acros only
	MonochromaticMG   *int     `json:"monochromaticMG,omitempty"`  // -18..+18, B&W/Acros only
}

// ---- canonical vocabulary ----
// The editor, the import validation and the decoder all speak these exact
// strings: a fingerprint is only matchable if every producer hashes the same
// vocabulary, so none of this is free text.

// FilmSimulationNames is the ordered canonical list (color sims first, then
// the monochrome family that lives in the Saturation tag).
var FilmSimulationNames = []string{
	"Provia/Standard",
	"Velvia/Vivid",
	"Astia/Soft",
	"Classic Chrome",
	"Reala ACE",
	"Pro Neg. Hi",
	"Pro Neg. Std",
	"Classic Negative",
	"Nostalgic Neg",
	"Eterna/Cinema",
	"Bleach Bypass/Eterna Bleach Bypass",
	"Fujichrome/Velvia",
	"Studio Portrait",
	"Studio Portrait Enhanced Saturation",
	"Studio Portrait Increased Sharpness",
	"Studio Portrait Ex",
	"Acros",
	"Acros + Ye Filter",
	"Acros + R Filter",
	"Acros + G Filter",
	"Monochrome",
	"Monochrome + Ye Filter",
	"Monochrome + R Filter",
	"Monochrome + G Filter",
	"Sepia",
}

// MonochromeSimulations are the sims on which the monochromatic color (WC/MG)
// applies and the Color scale does not.
var MonochromeSimulations = []string{
	"Acros", "Acros + Ye Filter", "Acros + R Filter", "Acros + G Filter",
	"Monochrome", "Monochrome + Ye Filter", "Monochrome + R Filter", "Monochrome + G Filter",
	"Sepia",
}

var (
	DynamicRangeNames   = []string{"Auto", "DR100", "DR200", "DR400"}
	DRangePriorityNames = []string{"Off", "Auto", "Weak", "Strong"}
	StrengthNames       = []string{"Off", "Weak", "Strong"}
	GrainSizeNames      = []string{"Off", "Small", "Large"}
)

// WhiteBalanceListNames is the ordered canonical list for the editor select
// (same strings as the decoder's whiteBalanceNames table).
var WhiteBalanceListNames = []string{
	"Auto",
	"Auto (white priority)",
	"Auto (ambiance priority)",
	"Daylight",
	"Cloudy",
	"Daylight Fluorescent",
	"Day White Fluorescent",
	"White Fluorescent",
	"Warm White Fluorescent",
	"Living Room Warm White Fluorescent",
	"Incandescent",
	"Flash",
	"Underwater",
	"Kelvin",
	"Custom",
	"Custom2",
	"Custom3",
	"Custom4",
	"Custom5",
}

// IsMonochromeSim reports whether the film simulation is in the B&W/Acros
// family (WC/MG apply, Color does not).
func IsMonochromeSim(sim string) bool {
	for _, m := range MonochromeSimulations {
		if sim == m {
			return true
		}
	}
	return false
}

func inList(v string, list []string) bool {
	for _, s := range list {
		if v == s {
			return true
		}
	}
	return false
}

// DefaultRecipeFields returns the out-of-the-box camera settings. The editor
// starts from these so a recipe created there is complete by construction even
// when the published source omits half the parameters.
func DefaultRecipeFields() RecipeFields {
	return RecipeFields{
		FilmSimulation:    ptr("Provia/Standard"),
		DynamicRange:      ptr("Auto"),
		DRangePriority:    ptr("Off"),
		HighlightTone:     ptr(0.0),
		ShadowTone:        ptr(0.0),
		Color:             ptr(0),
		Sharpness:         ptr(0),
		NoiseReduction:    ptr(0),
		Clarity:           ptr(0),
		GrainEffect:       ptr("Off"),
		GrainSize:         ptr("Off"),
		ColorChrome:       ptr("Off"),
		ColorChromeFXBlue: ptr("Off"),
		WhiteBalance:      ptr("Auto"),
		WBShiftRed:        ptr(0),
		WBShiftBlue:       ptr(0),
		MonochromaticWC:   ptr(0),
		MonochromaticMG:   ptr(0),
	}
}

func ptr[T any](v T) *T { return &v }

// applyNeutralDefaults fills the fields whose absence conventionally means
// "neutral" — D-Range Priority Off, monochromatic color 0 (features.md: absent
// on a color recipe, they count as neutral), grain size Off when the grain
// effect is Off — without touching anything the author actually has to state.
func (f *RecipeFields) applyNeutralDefaults() {
	if f.DRangePriority == nil {
		f.DRangePriority = ptr("Off")
	}
	if f.MonochromaticWC == nil {
		f.MonochromaticWC = ptr(0)
	}
	if f.MonochromaticMG == nil {
		f.MonochromaticMG = ptr(0)
	}
	if f.GrainSize == nil && f.GrainEffect != nil && *f.GrainEffect == "Off" {
		f.GrainSize = ptr("Off")
	}
	if f.FilmSimulation != nil && IsMonochromeSim(*f.FilmSimulation) && f.Color == nil {
		f.Color = ptr(0)
	}
}

// MissingFields lists (in canonical JSON names) the fields still needed for a
// matchable fingerprint, after neutral defaults. Empty means complete.
func (f *RecipeFields) MissingFields() []string {
	g := *f
	g.applyNeutralDefaults()
	var missing []string
	req := func(name string, present bool) {
		if !present {
			missing = append(missing, name)
		}
	}
	req("filmSimulation", g.FilmSimulation != nil)
	req("dynamicRange", g.DynamicRange != nil)
	req("highlightTone", g.HighlightTone != nil)
	req("shadowTone", g.ShadowTone != nil)
	req("color", g.Color != nil)
	req("sharpness", g.Sharpness != nil)
	req("noiseReduction", g.NoiseReduction != nil)
	req("clarity", g.Clarity != nil)
	req("grainEffect", g.GrainEffect != nil)
	req("grainSize", g.GrainSize != nil)
	req("colorChrome", g.ColorChrome != nil)
	req("colorChromeFXBlue", g.ColorChromeFXBlue != nil)
	req("whiteBalance", g.WhiteBalance != nil)
	req("wbShiftRed", g.WBShiftRed != nil)
	req("wbShiftBlue", g.WBShiftBlue != nil)
	if g.WhiteBalance != nil && *g.WhiteBalance == "Kelvin" {
		req("colorTemperature", g.ColorTemperature != nil)
	}
	return missing
}

// Complete reports whether the fields can produce a matchable fingerprint.
func (f *RecipeFields) Complete() bool { return len(f.MissingFields()) == 0 }

// Validate checks every present field against the canonical vocabulary and
// bounds shared by the editor, the import and (implicitly) the decoder.
// Messages are user-facing (the import report shows them verbatim).
func (f *RecipeFields) Validate() error {
	var errs []string
	bad := func(format string, args ...any) { errs = append(errs, fmt.Sprintf(format, args...)) }

	if f.FilmSimulation != nil && !inList(*f.FilmSimulation, FilmSimulationNames) {
		bad("filmSimulation « %s » inconnue", *f.FilmSimulation)
	}
	if f.DynamicRange != nil && !inList(*f.DynamicRange, DynamicRangeNames) {
		bad("dynamicRange « %s » invalide (Auto, DR100, DR200, DR400)", *f.DynamicRange)
	}
	if f.DRangePriority != nil && !inList(*f.DRangePriority, DRangePriorityNames) {
		bad("dRangePriority « %s » invalide (Off, Auto, Weak, Strong)", *f.DRangePriority)
	}
	tone := func(name string, v *float64) {
		if v == nil {
			return
		}
		if *v < -2 || *v > 4 || math.Mod(*v*2, 1) != 0 {
			bad("%s %s hors bornes (-2 à +4 par pas de 0,5)", name, strconv.FormatFloat(*v, 'f', -1, 64))
		}
	}
	tone("highlightTone", f.HighlightTone)
	tone("shadowTone", f.ShadowTone)
	intRange := func(name string, v *int, min, max int) {
		if v != nil && (*v < min || *v > max) {
			bad("%s %d hors bornes (%d à %d)", name, *v, min, max)
		}
	}
	intRange("color", f.Color, -4, 4)
	intRange("sharpness", f.Sharpness, -4, 4)
	intRange("noiseReduction", f.NoiseReduction, -4, 4)
	intRange("clarity", f.Clarity, -5, 5)
	intRange("wbShiftRed", f.WBShiftRed, -9, 9)
	intRange("wbShiftBlue", f.WBShiftBlue, -9, 9)
	intRange("monochromaticWC", f.MonochromaticWC, -18, 18)
	intRange("monochromaticMG", f.MonochromaticMG, -18, 18)
	intRange("colorTemperature", f.ColorTemperature, 2500, 10000)
	enum := func(name string, v *string, list []string) {
		if v != nil && !inList(*v, list) {
			bad("%s « %s » invalide (%s)", name, *v, strings.Join(list, ", "))
		}
	}
	enum("grainEffect", f.GrainEffect, StrengthNames)
	enum("grainSize", f.GrainSize, GrainSizeNames)
	// The camera's grain menu is one combined setting: an active effect always
	// carries Small or Large. Accepting "Off" here would store a fingerprint no
	// body ever produces — silently unmatched forever.
	if f.GrainEffect != nil && *f.GrainEffect != "Off" &&
		f.GrainSize != nil && *f.GrainSize == "Off" {
		bad("grainSize « Off » est incompatible avec grainEffect « %s » (le boîtier impose Small ou Large)", *f.GrainEffect)
	}
	enum("colorChrome", f.ColorChrome, StrengthNames)
	enum("colorChromeFXBlue", f.ColorChromeFXBlue, StrengthNames)
	if f.WhiteBalance != nil && !inList(*f.WhiteBalance, WhiteBalanceListNames) {
		bad("whiteBalance « %s » inconnue", *f.WhiteBalance)
	}

	if len(errs) > 0 {
		return fmt.Errorf("%s", strings.Join(errs, " ; "))
	}
	return nil
}

// normalized returns a copy with the fingerprint equivalences applied, so the
// decoder, the editor and the import hash the same bytes for the same recipe:
//   - monochrome sim → Color is 0 (the scale doesn't exist there);
//   - color sim → WC/MG are 0 (the tags don't exist there);
//   - grain Off → size Off (the camera menu is one combined setting);
//   - WB other than Kelvin → no color temperature.
func (f RecipeFields) normalized() RecipeFields {
	g := f
	g.applyNeutralDefaults()
	if g.FilmSimulation != nil {
		if IsMonochromeSim(*g.FilmSimulation) {
			g.Color = ptr(0)
		} else {
			g.MonochromaticWC = ptr(0)
			g.MonochromaticMG = ptr(0)
		}
	}
	if g.GrainEffect != nil && *g.GrainEffect == "Off" {
		g.GrainSize = ptr("Off")
	}
	if g.WhiteBalance == nil || *g.WhiteBalance != "Kelvin" {
		g.ColorTemperature = nil
	}
	return g
}

// Fingerprint computes the recipe hash: a short hex digest of the canonical
// serialization (sorted keys, normalized values) of the rendering fields.
// Returns "" when the fields are incomplete (no matchable fingerprint).
func (f *RecipeFields) Fingerprint() string {
	if !f.Complete() {
		return ""
	}
	g := f.normalized()

	num := func(v float64) string {
		if v == 0 {
			return "0" // JSON "-0.0" decodes to negative zero, which FormatFloat renders "-0"
		}
		return strconv.FormatFloat(v, 'f', -1, 64)
	}
	lines := []string{
		"clarity=" + strconv.Itoa(*g.Clarity),
		"color=" + strconv.Itoa(*g.Color),
		"colorChrome=" + *g.ColorChrome,
		"colorChromeFXBlue=" + *g.ColorChromeFXBlue,
		"dRangePriority=" + *g.DRangePriority,
		"dynamicRange=" + *g.DynamicRange,
		"filmSimulation=" + *g.FilmSimulation,
		"grainEffect=" + *g.GrainEffect,
		"grainSize=" + *g.GrainSize,
		"highlightTone=" + num(*g.HighlightTone),
		"monochromaticMG=" + strconv.Itoa(*g.MonochromaticMG),
		"monochromaticWC=" + strconv.Itoa(*g.MonochromaticWC),
		"noiseReduction=" + strconv.Itoa(*g.NoiseReduction),
		"shadowTone=" + num(*g.ShadowTone),
		"sharpness=" + strconv.Itoa(*g.Sharpness),
		"wbShiftBlue=" + strconv.Itoa(*g.WBShiftBlue),
		"wbShiftRed=" + strconv.Itoa(*g.WBShiftRed),
		"whiteBalance=" + *g.WhiteBalance,
	}
	if g.ColorTemperature != nil {
		lines = append(lines, "colorTemperature="+strconv.Itoa(*g.ColorTemperature))
	}
	sort.Strings(lines)

	h := sha256.Sum256([]byte("v" + strconv.Itoa(RecipeVersion) + "\n" + strings.Join(lines, "\n")))
	return hex.EncodeToString(h[:])[:16]
}

// recipeFields decodes the complete recipe of a shot from its maker note, or
// nil when the note carries no film simulation (no usable Fujifilm rendering
// data). Absent tags decode as their neutral default — that is how older
// bodies (and half the published recipes) express "this control doesn't
// exist here" — so the fingerprint never blocks on a missing tag.
func (fn *fujiMakerNote) recipeFields() *RecipeFields {
	sim := fn.filmSimulation()
	if sim == "" {
		return nil
	}
	f := DefaultRecipeFields()
	f.FilmSimulation = ptr(sim)
	f.DynamicRange = ptr(fn.recipeDynamicRange())
	f.DRangePriority = ptr(fn.dRangePriority())
	if v, ok := fn.first(fujiHighlightTone); ok {
		f.HighlightTone = ptr(toneValue(v))
	}
	if v, ok := fn.first(fujiShadowTone); ok {
		f.ShadowTone = ptr(toneValue(v))
	}
	if v, ok := fn.first(fujiSaturation); ok {
		if _, mono := saturationFilmSim[uint32(v)]; !mono {
			if ui, ok := saturationValues[v]; ok {
				f.Color = ptr(ui)
			}
		}
	}
	if v, ok := fn.first(fujiSharpness); ok {
		if ui, ok := sharpnessValues[v]; ok {
			f.Sharpness = ptr(ui)
		}
	}
	if v, ok := fn.first(fujiNoiseReduction); ok {
		if ui, ok := noiseReductionValues[v]; ok {
			f.NoiseReduction = ptr(ui)
		}
	}
	if v, ok := fn.first(fujiClarity); ok {
		f.Clarity = ptr(int(v / 1000))
	}
	if v, ok := fn.first(fujiGrainEffect); ok {
		if s, ok := strengthNames[uint32(v)]; ok {
			f.GrainEffect = ptr(s)
		}
	}
	if v, ok := fn.first(fujiGrainEffectSize); ok {
		if s, ok := grainSizeNames[uint32(v)]; ok {
			f.GrainSize = ptr(s)
		}
	}
	if v, ok := fn.first(fujiColorChromeEffect); ok {
		if s, ok := strengthNames[uint32(v)]; ok {
			f.ColorChrome = ptr(s)
		}
	}
	if v, ok := fn.first(fujiColorChromeFXBlue); ok {
		if s, ok := strengthNames[uint32(v)]; ok {
			f.ColorChromeFXBlue = ptr(s)
		}
	}
	if v, ok := fn.first(fujiWhiteBalance); ok {
		f.WhiteBalance = ptr(enumLabel(whiteBalanceNames, "WhiteBalance", v))
		if *f.WhiteBalance == "Kelvin" {
			if t, ok := fn.first(fujiColorTemperature); ok {
				f.ColorTemperature = ptr(int(t))
			} else {
				f.ColorTemperature = ptr(0)
			}
		}
	}
	if r, b, ok := fn.pair(fujiWBFineTune); ok {
		f.WBShiftRed = ptr(wbShiftValue(r))
		f.WBShiftBlue = ptr(wbShiftValue(b))
	}
	if v, ok := fn.first(fujiBWAdjustment); ok {
		f.MonochromaticWC = ptr(int(v))
	}
	if v, ok := fn.first(fujiBWMagentaGreen); ok {
		f.MonochromaticMG = ptr(int(v))
	}
	return &f
}
