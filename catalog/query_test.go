package catalog

import "testing"

// seedRated catalogs five photos: two rated (4 and 2 stars), one discarded,
// two undecided.
func seedRated(t *testing.T) *Store {
	t.Helper()
	cat, store, _ := newTestCataloger(t)
	for _, kb := range []string{"F/A", "F/B", "F/C", "F/D", "F/E"} {
		addRow(t, cat, store, kb, "")
	}
	mustSet := func(ok bool, err error, what string) {
		t.Helper()
		if err != nil || !ok {
			t.Fatalf("%s: ok=%v err=%v", what, ok, err)
		}
	}
	ok, err := store.SetRating("F/A", 4)
	mustSet(ok, err, "rate F/A")
	ok, err = store.SetRating("F/B", 2)
	mustSet(ok, err, "rate F/B")
	ok, err = store.SetDecision("F/C", "discard")
	mustSet(ok, err, "discard F/C")
	return store
}

func TestListPhotosTotalAcrossPages(t *testing.T) {
	store := seedRated(t)

	var got int
	cursor := ""
	for page := 0; ; page++ {
		p, err := store.ListPhotos(PhotoFilter{}, cursor, 2)
		if err != nil {
			t.Fatalf("page %d: %v", page, err)
		}
		// The total is cursor-independent: every page reports the full count.
		if p.Total != 5 {
			t.Fatalf("page %d: Total = %d; want 5", page, p.Total)
		}
		got += len(p.Items)
		if p.NextCursor == "" {
			break
		}
		cursor = p.NextCursor
	}
	if got != 5 {
		t.Fatalf("walked %d items across pages; want Total (5)", got)
	}
}

func TestListPhotosTotalFiltered(t *testing.T) {
	store := seedRated(t)

	cases := []struct {
		name   string
		filter PhotoFilter
		want   int
	}{
		{"minRating", PhotoFilter{MinRating: 3}, 1},
		{"keep", PhotoFilter{Decision: "keep"}, 2},
		{"discard", PhotoFilter{Decision: "discard"}, 1},
		{"none", PhotoFilter{Decision: "none"}, 2},
	}
	for _, tc := range cases {
		p, err := store.ListPhotos(tc.filter, "", 10)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if p.Total != tc.want {
			t.Errorf("%s: Total = %d; want %d", tc.name, p.Total, tc.want)
		}
		if len(p.Items) != tc.want {
			t.Errorf("%s: %d items; want %d", tc.name, len(p.Items), tc.want)
		}
	}
}
