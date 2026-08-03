package server

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"fragments/catalog"
)

// recipeBody is the JSON document of the editor (create and full update) and
// of one import-file entry. Fields are validated against the canonical
// vocabulary — the fingerprint is only matchable if the editor produces
// exactly the decoder's vocabulary, so nothing rendering-related is free text.
type recipeBody struct {
	Name      string               `json:"name"`
	Fields    catalog.RecipeFields `json:"fields"`
	Notes     string               `json:"notes,omitempty"`
	Author    string               `json:"author,omitempty"`
	AuthorURL string               `json:"authorUrl,omitempty"`
	Source    string               `json:"source,omitempty"`
}

// validRecipeURL accepts only absolute http/https URLs, so an imported recipe
// file can't slip a javascript: link into the UI.
func validRecipeURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}

// checkRecipeTexts bounds the free-text fields (SQLite happily stores a 900 KB
// name otherwise). Returns "" when everything fits.
func checkRecipeTexts(b *recipeBody) string {
	limits := []struct {
		name  string
		value string
		max   int
	}{
		{"name", b.Name, 200},
		{"author", b.Author, 200},
		{"source", b.Source, 200},
		{"authorUrl", b.AuthorURL, 500},
		{"notes", b.Notes, 10000},
	}
	for _, l := range limits {
		if len(l.value) > l.max {
			return "le champ " + l.name + " est trop long (maximum " + strconv.Itoa(l.max) + " caractères)"
		}
	}
	return ""
}

// checkRecipeBody normalizes and validates an editor payload, writing the 400
// itself. ok=false means the response is already sent. The editor paths (POST
// and PATCH) also require COMPLETE fields — a recipe from the editor is
// "complète par construction" (import is the only legal source of incomplete
// cards), and this structurally prevents a PATCH from nulling the hash of a
// paired recipe.
func (s *Server) checkRecipeBody(c *gin.Context, body *recipeBody) bool {
	body.Name = strings.TrimSpace(body.Name)
	if body.Name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "le nom de la recette est requis"})
		return false
	}
	if msg := checkRecipeTexts(body); msg != "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": msg})
		return false
	}
	if err := body.Fields.Validate(); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return false
	}
	if missing := body.Fields.MissingFields(); len(missing) > 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "champs de rendu manquants : " + strings.Join(missing, ", ")})
		return false
	}
	if body.AuthorURL != "" && !validRecipeURL(body.AuthorURL) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "authorUrl doit être une URL http(s)"})
		return false
	}
	return true
}

// writeRecipeConflict maps the store's uniqueness sentinels to a 409 carrying
// enough context for the UI to propose renaming the existing recipe.
func (s *Server) writeRecipeConflict(c *gin.Context, err error, fields catalog.RecipeFields) bool {
	switch {
	case errors.Is(err, catalog.ErrRecipeNameTaken):
		c.JSON(http.StatusConflict, gin.H{"error": "une recette porte déjà ce nom", "conflict": "name"})
		return true
	case errors.Is(err, catalog.ErrRecipeHashTaken):
		existing, _, lookupErr := s.store.RecipeNameByHash(fields.Fingerprint())
		if lookupErr != nil {
			s.log.Printf("recipe conflict lookup: %v", lookupErr)
		}
		msg := "une recette existante a exactement les mêmes réglages"
		if existing != "" {
			msg += " : « " + existing + " » — renommez-la plutôt"
		}
		c.JSON(http.StatusConflict, gin.H{
			"error":    msg,
			"conflict": "hash", "existingName": existing,
		})
		return true
	}
	return false
}

// handleListRecipes: GET /api/recipes
func (s *Server) handleListRecipes(c *gin.Context) {
	recipes, err := s.store.ListRecipes()
	if err != nil {
		s.log.Printf("list recipes: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list recipes"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"recipes": recipes})
}

// handleRecipeSchema: GET /api/recipes/schema — the canonical vocabulary,
// bounds and defaults the editor builds its constrained controls from. Served
// by the backend so the decoder, the validation and the form can never drift.
func (s *Server) handleRecipeSchema(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"filmSimulations":         catalog.FilmSimulationNames,
		"monochromeSimulations":   catalog.MonochromeSimulations,
		"dynamicRanges":           catalog.DynamicRangeNames,
		"dRangePriorities":        catalog.DRangePriorityNames,
		"strengths":               catalog.StrengthNames, // grain, color chrome, FX blue
		"grainSizes":              catalog.GrainSizeNames,
		"whiteBalances":           catalog.WhiteBalanceListNames,
		"bounds": gin.H{
			"tone":             gin.H{"min": -2, "max": 4, "step": 0.5},
			"color":            gin.H{"min": -4, "max": 4, "step": 1},
			"sharpness":        gin.H{"min": -4, "max": 4, "step": 1},
			"noiseReduction":   gin.H{"min": -4, "max": 4, "step": 1},
			"clarity":          gin.H{"min": -5, "max": 5, "step": 1},
			"wbShift":          gin.H{"min": -9, "max": 9, "step": 1},
			"monochromatic":    gin.H{"min": -18, "max": 18, "step": 1},
			"colorTemperature": gin.H{"min": 2500, "max": 10000, "step": 100},
		},
		"defaults": catalog.DefaultRecipeFields(),
	})
}

// handleGetRecipe: GET /api/recipes/:id
func (s *Server) handleGetRecipe(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}
	r, err := s.store.GetRecipe(id)
	if err != nil {
		s.log.Printf("get recipe: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load recipe"})
		return
	}
	if r == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "recipe not found"})
		return
	}
	c.JSON(http.StatusOK, r)
}

// handleCreateRecipe: POST /api/recipes {name, fields, notes?, author?, authorUrl?, source?}
func (s *Server) handleCreateRecipe(c *gin.Context) {
	var body recipeBody
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}
	if !s.checkRecipeBody(c, &body) {
		return
	}
	r, err := s.store.CreateRecipe(body.Name, body.Fields, body.Notes, body.Author, body.AuthorURL, body.Source)
	if err != nil {
		if s.writeRecipeConflict(c, err, body.Fields) {
			return
		}
		s.log.Printf("create recipe: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create recipe"})
		return
	}
	c.JSON(http.StatusCreated, r)
}

// handleUpdateRecipe: PATCH /api/recipes/:id — full editor document. Editing a
// rendering field recomputes the fingerprint and re-pairs the photos.
func (s *Server) handleUpdateRecipe(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}
	var body recipeBody
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}
	if !s.checkRecipeBody(c, &body) {
		return
	}
	r, err := s.store.UpdateRecipe(id, body.Name, body.Fields, body.Notes, body.Author, body.AuthorURL, body.Source)
	if err != nil {
		if errors.Is(err, catalog.ErrRecipeNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "recipe not found"})
			return
		}
		if s.writeRecipeConflict(c, err, body.Fields) {
			return
		}
		s.log.Printf("update recipe: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update recipe"})
		return
	}
	c.JSON(http.StatusOK, r)
}

// handleDeleteRecipe: DELETE /api/recipes/:id — the matching photos keep their
// fingerprint and simply become anonymous again.
func (s *Server) handleDeleteRecipe(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}
	deleted, err := s.store.DeleteRecipe(id)
	if err != nil {
		s.log.Printf("delete recipe: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete recipe"})
		return
	}
	if !deleted {
		c.JSON(http.StatusNotFound, gin.H{"error": "recipe not found"})
		return
	}
	c.Status(http.StatusNoContent)
}

// importItem mirrors one line of the import report shown to the user.
type importItem struct {
	Name    string `json:"name"`
	Status  string `json:"status"` // imported | incomplete | skipped | error
	Message string `json:"message,omitempty"`
}

// handleImportRecipes: POST /api/recipes/import — body is the recipe file (a
// JSON array in the export format). Each entry is validated with the same
// rules as the editor and hashed with the same canonical function as the
// cataloger, so importing a file instantly labels the matching photos.
// Conflicts keep the EXISTING recipe (and its credit) and are reported, never
// silently overwritten.
func (s *Server) handleImportRecipes(c *gin.Context) {
	var entries []recipeBody
	if err := c.ShouldBindJSON(&entries); err != nil {
		// The global body cap (limitRequestBody) surfaces here as a read error:
		// distinguish "too big" from "not JSON" or the user hunts a phantom typo.
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "fichier trop volumineux (limite 1 Mo)"})
			return
		}
		c.JSON(http.StatusBadRequest, gin.H{"error": "fichier invalide : un tableau JSON de recettes est attendu"})
		return
	}
	// Each entry is one INSERT on the single-connection pool: an unbounded
	// array (tens of thousands fit in 1 Mo) would block every other request
	// for seconds. No real recipe file comes near this cap.
	if len(entries) > 1000 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "trop de recettes dans le fichier (maximum 1000)"})
		return
	}

	report := struct {
		Imported   int          `json:"imported"`
		Incomplete int          `json:"incomplete"`
		Skipped    int          `json:"skipped"`
		Errors     int          `json:"errors"`
		Items      []importItem `json:"items"`
	}{Items: make([]importItem, 0, len(entries))}

	add := func(name, status, msg string) {
		report.Items = append(report.Items, importItem{Name: name, Status: status, Message: msg})
	}

	for _, e := range entries {
		e.Name = strings.TrimSpace(e.Name)
		if e.Name == "" {
			report.Errors++
			add("(sans nom)", "error", "nom manquant")
			continue
		}
		if msg := checkRecipeTexts(&e); msg != "" {
			report.Errors++
			add(e.Name, "error", msg)
			continue
		}
		if err := e.Fields.Validate(); err != nil {
			report.Errors++
			add(e.Name, "error", err.Error())
			continue
		}
		var warning string
		if e.AuthorURL != "" && !validRecipeURL(e.AuthorURL) {
			// A rejected URL doesn't reject the recipe: imported without the
			// link, with a warning in the report.
			warning = "lien auteur ignoré (URL non http/https)"
			e.AuthorURL = ""
		}

		_, err := s.store.CreateRecipe(e.Name, e.Fields, e.Notes, e.Author, e.AuthorURL, e.Source)
		switch {
		case errors.Is(err, catalog.ErrRecipeNameTaken):
			report.Skipped++
			add(e.Name, "skipped", "une recette porte déjà ce nom — recette existante conservée")
		case errors.Is(err, catalog.ErrRecipeHashTaken):
			existing, _, lookupErr := s.store.RecipeNameByHash(e.Fields.Fingerprint())
			if lookupErr != nil {
				s.log.Printf("import conflict lookup: %v", lookupErr)
			}
			report.Skipped++
			add(e.Name, "skipped", "mêmes réglages que « "+existing+" » — recette et crédit existants conservés")
		case err != nil:
			s.log.Printf("import recipe %q: %v", e.Name, err)
			report.Errors++
			add(e.Name, "error", "échec de l'enregistrement")
		case !e.Fields.Complete():
			report.Incomplete++
			msg := "incomplète (non appariée) : champs manquants — " + strings.Join(e.Fields.MissingFields(), ", ")
			if warning != "" {
				msg += " ; " + warning
			}
			add(e.Name, "incomplete", msg)
		default:
			report.Imported++
			add(e.Name, "imported", warning)
		}
	}
	c.JSON(http.StatusOK, report)
}

// handleExportRecipes: GET /api/recipes/export — the whole library in the
// import format (round-trip clean, credit included).
func (s *Server) handleExportRecipes(c *gin.Context) {
	recipes, err := s.store.ListRecipes()
	if err != nil {
		s.log.Printf("export recipes: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to export recipes"})
		return
	}
	out := make([]recipeBody, len(recipes))
	for i, r := range recipes {
		out[i] = recipeBody{
			Name: r.Name, Fields: r.Fields, Notes: r.Notes,
			Author: r.Author, AuthorURL: r.AuthorURL, Source: r.Source,
		}
	}
	c.Header("Content-Disposition", `attachment; filename="recettes.json"`)
	c.JSON(http.StatusOK, out)
}
