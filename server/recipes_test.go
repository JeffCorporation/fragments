package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"fragments/catalog"
)

// newRecipeTestServer builds a real Server over a temp catalog store. Building
// the router also proves the recipes routes (static schema/export/import next
// to :id) register without a gin tree conflict.
func newRecipeTestServer(t *testing.T) (*Server, *catalog.Store) {
	t.Helper()
	dir := t.TempDir()
	store, err := catalog.OpenStore(filepath.Join(dir, "catalog.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	cfg := Config{
		Password: "pw", Secret: []byte("test-secret"), ThumbDir: dir,
		SessionTTL: time.Hour,
	}
	return New(cfg, &catalog.Config{ThumbDir: dir}, store, nil, nil), store
}

// do performs an authenticated request (session cookie + CSRF for mutations).
func do(t *testing.T, s *Server, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	var rdr *strings.Reader
	if body != "" {
		rdr = strings.NewReader(body)
	} else {
		rdr = strings.NewReader("")
	}
	req := httptest.NewRequest(method, path, rdr)
	token := s.auth.issue(time.Now().Add(time.Hour))
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
	req.Header.Set("X-CSRF-Token", s.auth.csrfFor(token))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	s.engine.ServeHTTP(w, req)
	return w
}

func TestRecipesHTTPFlow(t *testing.T) {
	s, _ := newRecipeTestServer(t)

	// Schema serves the canonical vocabulary for the editor.
	w := do(t, s, "GET", "/api/recipes/schema", "")
	if w.Code != http.StatusOK {
		t.Fatalf("schema: %d %s", w.Code, w.Body.String())
	}
	var schema map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &schema); err != nil {
		t.Fatalf("schema json: %v", err)
	}
	for _, k := range []string{"filmSimulations", "whiteBalances", "bounds", "defaults", "monochromeSimulations"} {
		if _, ok := schema[k]; !ok {
			t.Errorf("schema misses %s", k)
		}
	}

	// Create with full editor fields.
	create := `{"name":"Kodachrome 64","author":"Ritchie Roesch","authorUrl":"https://fujixweekly.com/k64","source":"Fuji X Weekly",
		"fields":{"filmSimulation":"Classic Chrome","dynamicRange":"DR200","dRangePriority":"Off",
		"highlightTone":1,"shadowTone":-1,"color":1,"sharpness":0,"noiseReduction":-2,"clarity":0,
		"grainEffect":"Weak","grainSize":"Small","colorChrome":"Off","colorChromeFXBlue":"Off",
		"whiteBalance":"Daylight","wbShiftRed":2,"wbShiftBlue":-3,"monochromaticWC":0,"monochromaticMG":0}}`
	w = do(t, s, "POST", "/api/recipes", create)
	if w.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	var created catalog.Recipe
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatalf("create json: %v", err)
	}
	if created.Hash == "" || created.Incomplete {
		t.Fatalf("created recipe not complete: %+v", created)
	}

	// Same fields, other name → 409 with the existing name.
	dup := strings.Replace(create, "Kodachrome 64", "Copie", 1)
	w = do(t, s, "POST", "/api/recipes", dup)
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "Kodachrome 64") {
		t.Fatalf("dup fields: %d %s; want 409 naming the existing recipe", w.Code, w.Body.String())
	}

	// Unknown vocabulary → 400 (free text must never reach the fingerprint).
	bad := `{"name":"Casse","fields":{"filmSimulation":"classic chrome"}}`
	w = do(t, s, "POST", "/api/recipes", bad)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("bad vocab: %d %s; want 400", w.Code, w.Body.String())
	}

	// Editor paths require COMPLETE fields ("complète par construction"): only
	// the import may create documentary, hash-less cards.
	w = do(t, s, "POST", "/api/recipes", `{"name":"Partielle éditeur","fields":{"filmSimulation":"Acros"}}`)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "manquants") {
		t.Fatalf("incomplete editor create: %d %s; want 400 naming missing fields", w.Code, w.Body.String())
	}

	// javascript: URL → 400.
	w = do(t, s, "POST", "/api/recipes", `{"name":"XSS","authorUrl":"javascript:alert(1)","fields":{}}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("js url: %d %s; want 400", w.Code, w.Body.String())
	}

	// List + get.
	w = do(t, s, "GET", "/api/recipes", "")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "Kodachrome 64") {
		t.Fatalf("list: %d %s", w.Code, w.Body.String())
	}
	w = do(t, s, "GET", "/api/recipes/1", "")
	if w.Code != http.StatusOK {
		t.Fatalf("get: %d %s", w.Code, w.Body.String())
	}

	// Import: one new, one hash-dup (kept existing), one incomplete, one bad
	// URL (imported without the link), one invalid (error).
	importBody := `[
		{"name":"Nouvelle","fields":{"filmSimulation":"Provia/Standard","dynamicRange":"Auto","highlightTone":0,"shadowTone":0,"color":0,"sharpness":0,"noiseReduction":0,"clarity":0,"grainEffect":"Off","grainSize":"Off","colorChrome":"Off","colorChromeFXBlue":"Off","whiteBalance":"Auto","wbShiftRed":0,"wbShiftBlue":0}},
		{"name":"Doublon","fields":{"filmSimulation":"Classic Chrome","dynamicRange":"DR200","highlightTone":1,"shadowTone":-1,"color":1,"sharpness":0,"noiseReduction":-2,"clarity":0,"grainEffect":"Weak","grainSize":"Small","colorChrome":"Off","colorChromeFXBlue":"Off","whiteBalance":"Daylight","wbShiftRed":2,"wbShiftBlue":-3}},
		{"name":"Partielle","authorUrl":"ftp://nope","fields":{"filmSimulation":"Acros"}},
		{"name":"Cassée","fields":{"filmSimulation":"Kodak Gold"}}
	]`
	w = do(t, s, "POST", "/api/recipes/import", importBody)
	if w.Code != http.StatusOK {
		t.Fatalf("import: %d %s", w.Code, w.Body.String())
	}
	var report struct {
		Imported, Incomplete, Skipped, Errors int
		Items                                 []struct{ Name, Status, Message string }
	}
	if err := json.Unmarshal(w.Body.Bytes(), &report); err != nil {
		t.Fatalf("report json: %v", err)
	}
	if report.Imported != 1 || report.Incomplete != 1 || report.Skipped != 1 || report.Errors != 1 {
		t.Fatalf("report = %+v", report)
	}
	for _, it := range report.Items {
		if it.Name == "Doublon" && !strings.Contains(it.Message, "Kodachrome 64") {
			t.Errorf("hash-dup message must name the kept recipe: %q", it.Message)
		}
		if it.Name == "Partielle" && it.Status != "incomplete" {
			t.Errorf("Partielle status = %s; want incomplete", it.Status)
		}
	}

	// Export round-trips the import format.
	w = do(t, s, "GET", "/api/recipes/export", "")
	if w.Code != http.StatusOK {
		t.Fatalf("export: %d", w.Code)
	}
	if cd := w.Header().Get("Content-Disposition"); !strings.Contains(cd, "recettes.json") {
		t.Errorf("export Content-Disposition = %q", cd)
	}
	var exported []struct {
		Name   string               `json:"name"`
		Fields catalog.RecipeFields `json:"fields"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &exported); err != nil {
		t.Fatalf("export json: %v", err)
	}
	if len(exported) != 3 { // Kodachrome 64, Nouvelle, Partielle
		t.Fatalf("exported %d recipes; want 3", len(exported))
	}

	// Update and delete.
	w = do(t, s, "PATCH", "/api/recipes/1", strings.Replace(create, "Kodachrome 64", "Kodachrome 64 v2", 1))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "Kodachrome 64 v2") {
		t.Fatalf("patch: %d %s", w.Code, w.Body.String())
	}
	w = do(t, s, "DELETE", "/api/recipes/1", "")
	if w.Code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", w.Code, w.Body.String())
	}
	w = do(t, s, "GET", "/api/recipes/1", "")
	if w.Code != http.StatusNotFound {
		t.Fatalf("get after delete: %d", w.Code)
	}
}

// The recipe filter reaches the gallery listing.
func TestPhotosRecipeFilterParam(t *testing.T) {
	s, store := newRecipeTestServer(t)

	fields := catalog.DefaultRecipeFields()
	hash := fields.Fingerprint()
	p := &catalog.Photo{KeyBase: "F/A", Folder: "F", Name: "A",
		JPEG: catalog.ObjectRef{Key: "F/A.JPG", Size: 1, ETag: "e"}}
	p.Meta.RecipeHash = hash
	if err := store.Upsert(p, time.Now()); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	q := &catalog.Photo{KeyBase: "F/B", Folder: "F", Name: "B",
		JPEG: catalog.ObjectRef{Key: "F/B.JPG", Size: 1, ETag: "e2"}}
	if err := store.Upsert(q, time.Now()); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	w := do(t, s, "GET", "/api/photos?recipe="+hash, "")
	if w.Code != http.StatusOK {
		t.Fatalf("photos: %d %s", w.Code, w.Body.String())
	}
	var page struct{ Total int }
	if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil {
		t.Fatalf("page json: %v", err)
	}
	if page.Total != 1 {
		t.Errorf("recipe-filtered total = %d; want 1", page.Total)
	}
}
