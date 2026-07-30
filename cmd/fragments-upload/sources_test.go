package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestFrenchAgo(t *testing.T) {
	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		ago  time.Duration
		want string
	}{
		{30 * time.Second, "à l'instant"},
		{5 * time.Minute, "il y a 5 min"},
		{3 * time.Hour, "il y a 3 h"},
		{30 * time.Hour, "hier"},
		{5 * 24 * time.Hour, "il y a 5 j"},
		{70 * 24 * time.Hour, "il y a 2 mois"},
		{400 * 24 * time.Hour, "il y a 1 an"},
		{800 * 24 * time.Hour, "il y a 2 ans"},
	}
	for _, c := range cases {
		if got := frenchAgo(now.Add(-c.ago), now); got != c.want {
			t.Errorf("frenchAgo(-%v) = %q, want %q", c.ago, got, c.want)
		}
	}
	if got := frenchAgo(time.Time{}, now); got != "date inconnue" {
		t.Errorf("frenchAgo(zero) = %q", got)
	}
}

func TestFrenchSize(t *testing.T) {
	cases := []struct {
		b    int64
		want string
	}{
		{0, "0 octet"},
		{1, "1 octet"},
		{999, "999 octets"},
		{1500, "1,5 Ko"},
		{26_400_000_000, "26,4 Go"},
	}
	for _, c := range cases {
		if got := frenchSize(c.b); got != c.want {
			t.Errorf("frenchSize(%d) = %q, want %q", c.b, got, c.want)
		}
	}
}

func TestFrenchCount(t *testing.T) {
	cases := []struct {
		n    int
		want string
	}{
		{1, "1 fichier"},
		{748, "748 fichiers"},
		{1234, "1 234 fichiers"},
		{1234567, "1 234 567 fichiers"},
	}
	for _, c := range cases {
		if got := frenchCount(c.n); got != c.want {
			t.Errorf("frenchCount(%d) = %q, want %q", c.n, got, c.want)
		}
	}
}

func TestPrettyVolumeName(t *testing.T) {
	cases := []struct{ in, want string }{
		{"3431-3733", "3431-3733"},
		{"mtp:host=SAMSUNG_SAMSUNG_Android_RF8M33ZDXHV", "SAMSUNG SAMSUNG Android RF8M33ZDXHV (MTP)"},
		{"gphoto2:host=Fujifilm_X-T5", "Fujifilm X-T5 (PTP)"},
		{"mtp:host=", "mtp:host="},
	}
	for _, c := range cases {
		if got := prettyVolumeName(c.in); got != c.want {
			t.Errorf("prettyVolumeName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestFindDCIMAndStats builds a fake volume layout and checks discovery depth,
// hidden-dir skipping and stats filtering in one pass.
func TestFindDCIMAndStats(t *testing.T) {
	vol := t.TempDir()
	dcim := filepath.Join(vol, "Internal storage", "DCIM", "100_FUJI")
	if err := os.MkdirAll(dcim, 0o755); err != nil {
		t.Fatal(err)
	}
	// Hidden dir with a DCIM inside must be ignored.
	if err := os.MkdirAll(filepath.Join(vol, ".Trash", "DCIM"), 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name string, size int, mtime time.Time) {
		path := filepath.Join(dcim, name)
		if err := os.WriteFile(path, make([]byte, size), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, mtime, mtime); err != nil {
			t.Fatal(err)
		}
	}
	newest := time.Date(2026, 7, 29, 10, 0, 0, 0, time.Local)
	write("DSCF0001.JPG", 100, newest.Add(-time.Hour))
	write("DSCF0001.RAF", 200, newest)
	write("DSCF0002.jpg", 50, newest.Add(-2*time.Hour)) // lower case counts too
	write("FUJI0001.MOV", 400, newest.Add(-3*time.Hour))
	write("notes.txt", 999, newest) // not a media file

	found := findDCIM(vol, dcimSearchDepth)
	if len(found) != 1 || found[0] != filepath.Join(vol, "Internal storage", "DCIM") {
		t.Fatalf("findDCIM = %v, want the single real DCIM", found)
	}

	files, size, lastMod := statsOf(found[0])
	if files != 4 {
		t.Errorf("files = %d, want 4", files)
	}
	if size != 750 {
		t.Errorf("size = %d, want 750", size)
	}
	if !lastMod.Equal(newest) {
		t.Errorf("lastMod = %v, want %v", lastMod, newest)
	}

	// Too shallow a search must not find it.
	if got := findDCIM(vol, 1); len(got) != 0 {
		t.Errorf("findDCIM depth 1 = %v, want none", got)
	}
}
