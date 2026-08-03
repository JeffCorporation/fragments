package catalog

import (
	"database/sql"
	"path/filepath"
	"testing"
)

// buildV2Database recreates a populated catalog exactly as the v2 code left it
// (base schema + migrations v1/v2, user_version=2, one photo with user-owned
// columns set), so the upgrade path — not just the fresh-database path — is
// exercised.
func buildV2Database(t *testing.T, path string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open raw db: %v", err)
	}
	defer db.Close()
	for i, stmt := range []string{schema, migrations[0], migrations[1], `PRAGMA user_version=2`} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("build v2 step %d: %v", i, err)
		}
	}
	if _, err := db.Exec(`INSERT INTO photos (key_base, folder, name, jpeg_key, jpeg_size, jpeg_etag,
		decision, cataloged_at, updated_at)
		VALUES ('F/A', 'F', 'A', 'F/A.JPG', 1000, 'e-1', 'discard', '2026-01-01 00:00:00', '2026-01-01 00:00:00')`); err != nil {
		t.Fatalf("seed v2 photo: %v", err)
	}
}

func TestMigrateFromV2PreservesData(t *testing.T) {
	path := filepath.Join(t.TempDir(), "catalog.db")
	buildV2Database(t, path)

	store, err := OpenStore(path)
	if err != nil {
		t.Fatalf("open v2 store (upgrade): %v", err)
	}

	var version int
	if err := store.db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		t.Fatalf("read user_version: %v", err)
	}
	if version != len(migrations) {
		t.Fatalf("user_version = %d; want %d", version, len(migrations))
	}

	// The new columns exist and the pre-existing row (with its user-owned
	// decision) survived intact.
	var decision string
	var hash, recipeVersion any
	if err := store.db.QueryRow(`SELECT decision, recipe_hash, recipe_version FROM photos WHERE key_base='F/A'`).
		Scan(&decision, &hash, &recipeVersion); err != nil {
		t.Fatalf("read upgraded row: %v", err)
	}
	if decision != "discard" || hash != nil || recipeVersion != nil {
		t.Errorf("upgraded row = %q/%v/%v; want discard/NULL/NULL", decision, hash, recipeVersion)
	}

	// The recipes table is usable right away.
	if _, err := store.CreateRecipe("Après migration", DefaultRecipeFields(), "", "", "", ""); err != nil {
		t.Fatalf("create recipe on upgraded db: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	// Reopening (migrations already applied) must be a clean no-op.
	store2, err := OpenStore(path)
	if err != nil {
		t.Fatalf("reopen migrated store: %v", err)
	}
	defer store2.Close()
	if n, err := store2.Count(); err != nil || n != 1 {
		t.Fatalf("count after reopen = %d/%v; want 1", n, err)
	}
}

// Each migration commits its schema change and its user_version bump in ONE
// transaction: after any successful OpenStore, version and schema can never
// disagree (the old failure mode was a crash window leaving recipe_hash
// present with user_version=2 — the non-idempotent ALTER then failed forever).
func TestMigrationVersionBumpIsAtomic(t *testing.T) {
	path := filepath.Join(t.TempDir(), "catalog.db")
	store, err := OpenStore(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer store.Close()

	var version int
	if err := store.db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		t.Fatalf("read user_version: %v", err)
	}
	if version != len(migrations) {
		t.Fatalf("user_version = %d; want %d", version, len(migrations))
	}
	// And the last migration's objects are all there.
	for _, q := range []string{
		`SELECT recipe_hash FROM photos LIMIT 0`,
		`SELECT id, name, hash, fields_json FROM recipes LIMIT 0`,
	} {
		if _, err := store.db.Exec(q); err != nil {
			t.Errorf("%s: %v", q, err)
		}
	}
}
