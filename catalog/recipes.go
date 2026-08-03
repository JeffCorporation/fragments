package catalog

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ErrRecipeNameTaken / ErrRecipeHashTaken mark uniqueness conflicts (as opposed
// to internal store failures) so the HTTP layer can map them to 409 vs 500.
// A hash conflict means another recipe already owns the exact same rendering
// fields — the fix is renaming/editing that one, never storing a duplicate.
var (
	ErrRecipeNameTaken = errors.New("recipe name already used")
	ErrRecipeHashTaken = errors.New("recipe fingerprint already used")
	ErrRecipeNotFound  = errors.New("recipe not found")
)

// Recipe is one library entry: named canonical rendering fields plus credit.
// Hash is "" for an incomplete (documentary, unmatched) recipe. PhotoCount and
// CoverThumbURL are derived from the photos sharing the fingerprint.
type Recipe struct {
	ID            int64        `json:"id"`
	Name          string       `json:"name"`
	Hash          string       `json:"hash"`
	Fields        RecipeFields `json:"fields"`
	Notes         string       `json:"notes,omitempty"`
	Author        string       `json:"author,omitempty"`
	AuthorURL     string       `json:"authorUrl,omitempty"`
	Source        string       `json:"source,omitempty"`
	CreatedAt     time.Time    `json:"createdAt"`
	PhotoCount    int          `json:"photoCount"`
	CoverThumbURL string       `json:"coverThumbUrl"`
	Incomplete    bool         `json:"incomplete"`
	MissingFields []string     `json:"missingFields,omitempty"`
}

// recipeColumns is the shared column set; the two subqueries derive the photo
// count and the cover (most recent matching photo). A NULL hash matches no
// photo (r.hash = NULL is never true), which is exactly right for incomplete
// recipes.
const recipeColumns = `r.id, r.name, COALESCE(r.hash,''), r.fields_json,
	COALESCE(r.notes,''), COALESCE(r.author,''), COALESCE(r.author_url,''), COALESCE(r.source,''),
	CAST(r.created_at AS TEXT),
	(SELECT COUNT(*) FROM photos p WHERE p.recipe_hash = r.hash),
	COALESCE((SELECT p.key_base FROM photos p WHERE p.recipe_hash = r.hash
	          ORDER BY p.taken_at DESC, p.id DESC LIMIT 1), '')`

func scanRecipe(row interface{ Scan(...any) error }) (*Recipe, error) {
	var (
		r          Recipe
		fieldsJSON string
		createdAt  sql.NullString
		cover      string
	)
	if err := row.Scan(&r.ID, &r.Name, &r.Hash, &fieldsJSON,
		&r.Notes, &r.Author, &r.AuthorURL, &r.Source,
		&createdAt, &r.PhotoCount, &cover); err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(fieldsJSON), &r.Fields); err != nil {
		return nil, fmt.Errorf("recipe %d fields: %w", r.ID, err)
	}
	if createdAt.Valid {
		if t := parseTakenAt(createdAt.String); t != nil {
			r.CreatedAt = *t
		}
	}
	if cover != "" {
		r.CoverThumbURL = "/thumbs/" + cover + ".jpg"
	}
	r.Incomplete = r.Hash == ""
	if r.Incomplete {
		r.MissingFields = r.Fields.MissingFields()
	}
	return &r, nil
}

// ListRecipes returns the whole named library, alphabetically.
func (s *Store) ListRecipes() ([]Recipe, error) {
	rows, err := s.db.Query(`SELECT ` + recipeColumns + ` FROM recipes r ORDER BY r.name COLLATE NOCASE`)
	if err != nil {
		return nil, fmt.Errorf("list recipes: %w", err)
	}
	defer rows.Close()

	recipes := make([]Recipe, 0)
	for rows.Next() {
		r, err := scanRecipe(rows)
		if err != nil {
			return nil, fmt.Errorf("list recipes: %w", err)
		}
		recipes = append(recipes, *r)
	}
	return recipes, rows.Err()
}

// GetRecipe returns one recipe, or (nil, nil) if id is unknown.
func (s *Store) GetRecipe(id int64) (*Recipe, error) {
	r, err := scanRecipe(s.db.QueryRow(`SELECT `+recipeColumns+` FROM recipes r WHERE r.id = ?`, id))
	switch {
	case err == nil:
		return r, nil
	case errors.Is(err, sql.ErrNoRows):
		return nil, nil
	default:
		return nil, fmt.Errorf("get recipe %d: %w", id, err)
	}
}

// RecipeNameByHash returns the name of the recipe owning a fingerprint, for
// conflict messages ("same fields as « X »"). found=false when unclaimed.
func (s *Store) RecipeNameByHash(hash string) (name string, found bool, err error) {
	if hash == "" {
		return "", false, nil
	}
	switch err := s.db.QueryRow(`SELECT name FROM recipes WHERE hash = ?`, hash).Scan(&name); err {
	case nil:
		return name, true, nil
	case sql.ErrNoRows:
		return "", false, nil
	default:
		return "", false, err
	}
}

// CreateRecipe inserts a recipe. The fingerprint is recomputed here — never
// trusted from the caller — with the same canonical function as the cataloger,
// so naming a recipe instantly labels every matching photo. Incomplete fields
// are legal (hash NULL, documentary card). Uniqueness conflicts surface as
// ErrRecipeNameTaken / ErrRecipeHashTaken.
func (s *Store) CreateRecipe(name string, fields RecipeFields, notes, author, authorURL, source string) (*Recipe, error) {
	fieldsJSON, err := json.Marshal(fields)
	if err != nil {
		return nil, fmt.Errorf("create recipe: %w", err)
	}
	res, err := s.db.Exec(`INSERT INTO recipes(name, hash, fields_json, notes, author, author_url, source, created_at)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?)`,
		name, nullIfEmpty(fields.Fingerprint()), string(fieldsJSON),
		nullIfEmpty(notes), nullIfEmpty(author), nullIfEmpty(authorURL), nullIfEmpty(source),
		nowStamp())
	if err != nil {
		return nil, recipeConflict(err)
	}
	id, _ := res.LastInsertId()
	return s.GetRecipe(id)
}

// UpdateRecipe replaces a recipe's name, fields, notes and credit, recomputing
// the fingerprint (an edited field re-pairs the photos, the wanted behavior).
func (s *Store) UpdateRecipe(id int64, name string, fields RecipeFields, notes, author, authorURL, source string) (*Recipe, error) {
	fieldsJSON, err := json.Marshal(fields)
	if err != nil {
		return nil, fmt.Errorf("update recipe: %w", err)
	}
	res, err := s.db.Exec(`UPDATE recipes SET name=?, hash=?, fields_json=?, notes=?, author=?, author_url=?, source=?
		WHERE id=?`,
		name, nullIfEmpty(fields.Fingerprint()), string(fieldsJSON),
		nullIfEmpty(notes), nullIfEmpty(author), nullIfEmpty(authorURL), nullIfEmpty(source), id)
	if err != nil {
		return nil, recipeConflict(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, ErrRecipeNotFound
	}
	return s.GetRecipe(id)
}

// DeleteRecipe removes a recipe, reporting whether it existed. The photos keep
// their fingerprint and simply become anonymous again.
func (s *Store) DeleteRecipe(id int64) (bool, error) {
	res, err := s.db.Exec(`DELETE FROM recipes WHERE id = ?`, id)
	if err != nil {
		return false, fmt.Errorf("delete recipe: %w", err)
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// recipeConflict maps SQLite UNIQUE violations on recipes to the sentinel
// errors (the single-connection pool makes check-then-insert racy in theory;
// the constraint is the source of truth).
func recipeConflict(err error) error {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "recipes.name"):
		return ErrRecipeNameTaken
	case strings.Contains(msg, "recipes.hash"):
		return ErrRecipeHashTaken
	default:
		return fmt.Errorf("recipe write: %w", err)
	}
}
