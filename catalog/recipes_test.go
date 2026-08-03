package catalog

import (
	"errors"
	"path/filepath"
	"testing"
	"time"
)

// addRecipePhoto catalogs one photo carrying a recipe fingerprint.
func addRecipePhoto(t *testing.T, store *Store, keyBase, hash string) {
	t.Helper()
	p := &Photo{
		KeyBase: keyBase, Folder: filepath.Dir(keyBase), Name: filepath.Base(keyBase),
		JPEG: ObjectRef{Key: keyBase + ".JPG", Size: 1000, ETag: "e-" + keyBase},
	}
	p.Meta.RecipeHash = hash
	if err := store.Upsert(p, time.Now()); err != nil {
		t.Fatalf("upsert %s: %v", keyBase, err)
	}
}

func classicChromeFields() RecipeFields {
	f := DefaultRecipeFields()
	f.FilmSimulation = ptr("Classic Chrome")
	f.HighlightTone = ptr(1.0)
	f.ShadowTone = ptr(-1.0)
	return f
}

func TestRecipeCRUDAndPairing(t *testing.T) {
	_, store, _ := newTestCataloger(t)

	fields := classicChromeFields()
	hash := fields.Fingerprint()
	addRecipePhoto(t, store, "F/A", hash)
	addRecipePhoto(t, store, "F/B", hash)
	addRecipePhoto(t, store, "F/C", "") // no recipe

	r, err := store.CreateRecipe("Kodachrome 64", fields, "ISO auto 6400", "Ritchie Roesch", "https://fujixweekly.com/x", "Fuji X Weekly")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if r.Hash != hash {
		t.Fatalf("stored hash %q != fingerprint %q", r.Hash, hash)
	}
	// Naming labels every matching photo at once.
	if r.PhotoCount != 2 {
		t.Errorf("photoCount = %d; want 2", r.PhotoCount)
	}
	if r.CoverThumbURL == "" {
		t.Error("cover thumb must be set when photos match")
	}
	if r.Incomplete {
		t.Error("complete fields must not be flagged incomplete")
	}

	// Gallery filter by fingerprint.
	page, err := store.ListPhotos(PhotoFilter{Recipe: hash}, "", 10)
	if err != nil {
		t.Fatalf("list by recipe: %v", err)
	}
	if page.Total != 2 || len(page.Items) != 2 {
		t.Errorf("recipe filter: total=%d items=%d; want 2/2", page.Total, len(page.Items))
	}

	// Photo detail resolves the recipe name via the shared fingerprint.
	d, err := store.GetPhoto("F/A")
	if err != nil {
		t.Fatalf("get photo: %v", err)
	}
	if d.RecipeHash != hash || d.RecipeName != "Kodachrome 64" || d.RecipeID != r.ID {
		t.Errorf("detail recipe = %q/%q/%d; want %q/Kodachrome 64/%d", d.RecipeHash, d.RecipeName, d.RecipeID, hash, r.ID)
	}
	if d2, _ := store.GetPhoto("F/C"); d2.RecipeHash != "" || d2.RecipeName != "" {
		t.Errorf("photo without recipe reports %q/%q; want empty", d2.RecipeHash, d2.RecipeName)
	}

	// Editing a rendering field re-pairs: the old photos become anonymous.
	edited := classicChromeFields()
	edited.HighlightTone = ptr(2.0)
	r2, err := store.UpdateRecipe(r.ID, "Kodachrome 64", edited, "", "", "", "")
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if r2.Hash == hash {
		t.Fatal("hash must change when a rendering field changes")
	}
	if r2.PhotoCount != 0 {
		t.Errorf("photoCount after re-pair = %d; want 0", r2.PhotoCount)
	}
	if d, _ := store.GetPhoto("F/A"); d.RecipeName != "" {
		t.Errorf("photo still paired to %q after hash change", d.RecipeName)
	}

	// Deleting the recipe leaves the photos' fingerprints untouched.
	if ok, err := store.DeleteRecipe(r.ID); err != nil || !ok {
		t.Fatalf("delete: ok=%v err=%v", ok, err)
	}
	if d, _ := store.GetPhoto("F/A"); d.RecipeHash != hash {
		t.Errorf("photo fingerprint lost on recipe delete: %q", d.RecipeHash)
	}
	if list, _ := store.ListRecipes(); len(list) != 0 {
		t.Errorf("%d recipes left after delete; want 0", len(list))
	}
}

func TestRecipeConflicts(t *testing.T) {
	_, store, _ := newTestCataloger(t)

	fields := classicChromeFields()
	if _, err := store.CreateRecipe("Une", fields, "", "", "", ""); err != nil {
		t.Fatalf("create: %v", err)
	}

	// Same name, other fields → name conflict.
	other := DefaultRecipeFields()
	if _, err := store.CreateRecipe("Une", other, "", "", "", ""); !errors.Is(err, ErrRecipeNameTaken) {
		t.Errorf("same name: err = %v; want ErrRecipeNameTaken", err)
	}
	// Other name, same fields → hash conflict (two names for one fingerprint).
	if _, err := store.CreateRecipe("Deux", fields, "", "", "", ""); !errors.Is(err, ErrRecipeHashTaken) {
		t.Errorf("same fields: err = %v; want ErrRecipeHashTaken", err)
	}
	if name, found, err := store.RecipeNameByHash(fields.Fingerprint()); err != nil || !found || name != "Une" {
		t.Errorf("RecipeNameByHash = %q/%v/%v; want Une/true/nil", name, found, err)
	}

	// Update onto an existing fingerprint is refused the same way.
	r2, err := store.CreateRecipe("Trois", other, "", "", "", "")
	if err != nil {
		t.Fatalf("create Trois: %v", err)
	}
	if _, err := store.UpdateRecipe(r2.ID, "Trois", fields, "", "", "", ""); !errors.Is(err, ErrRecipeHashTaken) {
		t.Errorf("update onto taken fields: err = %v; want ErrRecipeHashTaken", err)
	}
	if _, err := store.UpdateRecipe(9999, "X", other, "", "", "", ""); !errors.Is(err, ErrRecipeNotFound) {
		t.Errorf("update unknown id: err = %v; want ErrRecipeNotFound", err)
	}
}

// Several incomplete recipes coexist (NULL hash under UNIQUE) as documentary,
// unmatched cards.
func TestIncompleteRecipes(t *testing.T) {
	_, store, _ := newTestCataloger(t)

	partial := RecipeFields{FilmSimulation: ptr("Classic Chrome")}
	r1, err := store.CreateRecipe("Incomplète 1", partial, "", "", "", "")
	if err != nil {
		t.Fatalf("create incomplete: %v", err)
	}
	if _, err := store.CreateRecipe("Incomplète 2", RecipeFields{}, "", "", "", ""); err != nil {
		t.Fatalf("second NULL hash must be allowed: %v", err)
	}
	if r1.Hash != "" || !r1.Incomplete || len(r1.MissingFields) == 0 {
		t.Errorf("incomplete card: hash=%q incomplete=%v missing=%v", r1.Hash, r1.Incomplete, r1.MissingFields)
	}
	if r1.PhotoCount != 0 {
		t.Errorf("incomplete recipe photoCount = %d; want 0", r1.PhotoCount)
	}

	// Completing it in the editor produces the fingerprint and pairs photos.
	full := classicChromeFields()
	addRecipePhoto(t, store, "F/Z", full.Fingerprint())
	r2, err := store.UpdateRecipe(r1.ID, "Incomplète 1", full, "", "", "", "")
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	if r2.Hash == "" || r2.Incomplete || r2.PhotoCount != 1 {
		t.Errorf("completed card: hash=%q incomplete=%v count=%d; want hash/false/1", r2.Hash, r2.Incomplete, r2.PhotoCount)
	}
}

// The catalog pipeline stamps recipe_hash/recipe_version through Upsert; a
// photo without Fujifilm data stores NULLs.
func TestUpsertStampsRecipeVersion(t *testing.T) {
	_, store, _ := newTestCataloger(t)
	fields := classicChromeFields()
	addRecipePhoto(t, store, "F/A", fields.Fingerprint())
	addRecipePhoto(t, store, "F/B", "")

	var hash, version any
	if err := store.db.QueryRow(`SELECT recipe_hash, recipe_version FROM photos WHERE key_base='F/A'`).Scan(&hash, &version); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if hash != fields.Fingerprint() || version != int64(RecipeVersion) {
		t.Errorf("F/A stored %v/%v; want %s/%d", hash, version, fields.Fingerprint(), RecipeVersion)
	}
	if err := store.db.QueryRow(`SELECT recipe_hash, recipe_version FROM photos WHERE key_base='F/B'`).Scan(&hash, &version); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if hash != nil || version != nil {
		t.Errorf("F/B stored %v/%v; want NULL/NULL", hash, version)
	}
}
