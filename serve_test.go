package main

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func testServer(t *testing.T) (*server, *Pool) {
	t.Helper()
	p := testPool(t)
	return &server{pool: p, cfgPath: "config.yaml", dir: t.TempDir()}, p
}

func postForm(t *testing.T, h http.Handler, path string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rw := httptest.NewRecorder()
	h.ServeHTTP(rw, req)
	return rw
}

func countActivities(t *testing.T, p *Pool, kind string) int {
	t.Helper()
	var n int
	if err := p.db.QueryRow(`SELECT COUNT(*) FROM activities WHERE kind = ?`, kind).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// Disposition contract: the POST sets the operator field, appends exactly one
// DISPOSITION activity, and a double-submit is a no-op, not an error.
func TestDispositionPostIdempotent(t *testing.T) {
	s, p := testServer(t)
	it := testItem()
	if _, _, err := p.UpsertItem(&it, StatusQuarantine, "old", nil, "", "2026-07-08T12:00:00Z"); err != nil {
		t.Fatal(err)
	}
	h := s.routes()
	path := "/item/example/repo%2342/disposition"
	form := url.Values{"disposition": {"SHORTLISTED"}, "back": {"/review"}}

	rw := postForm(t, h, path, form)
	if rw.Code != http.StatusSeeOther {
		t.Fatalf("first post: code = %d, want 303", rw.Code)
	}
	if loc := rw.Header().Get("Location"); loc != "/review" {
		t.Fatalf("redirect = %q, want /review", loc)
	}
	row, err := p.Get(it.Key())
	if err != nil {
		t.Fatal(err)
	}
	if row.Disposition != "SHORTLISTED" {
		t.Fatalf("disposition = %q, want SHORTLISTED", row.Disposition)
	}
	if n := countActivities(t, p, "DISPOSITION"); n != 1 {
		t.Fatalf("activities = %d, want 1", n)
	}

	// Same disposition again: still 303, no second activity.
	rw = postForm(t, h, path, form)
	if rw.Code != http.StatusSeeOther {
		t.Fatalf("double submit: code = %d, want 303", rw.Code)
	}
	if n := countActivities(t, p, "DISPOSITION"); n != 1 {
		t.Fatalf("activities after double submit = %d, want 1", n)
	}

	// A real round trip logs every transition, even within the same day.
	postForm(t, h, path, url.Values{"disposition": {"NEW"}})
	postForm(t, h, path, url.Values{"disposition": {"SHORTLISTED"}})
	if n := countActivities(t, p, "DISPOSITION"); n != 3 {
		t.Fatalf("activities after round trip = %d, want 3", n)
	}
}

func TestDispositionPostRejectsBadInput(t *testing.T) {
	s, p := testServer(t)
	it := testItem()
	if _, _, err := p.UpsertItem(&it, StatusEligible, "fit", nil, "", "2026-07-08T12:00:00Z"); err != nil {
		t.Fatal(err)
	}
	h := s.routes()

	// The server only writes operator dispositions, never machine states.
	rw := postForm(t, h, "/item/example/repo%2342/disposition", url.Values{"disposition": {"WON"}})
	if rw.Code != http.StatusBadRequest {
		t.Fatalf("bad disposition: code = %d, want 400", rw.Code)
	}
	rw = postForm(t, h, "/item/example/repo%2399/disposition", url.Values{"disposition": {"DROPPED"}})
	if rw.Code != http.StatusNotFound {
		t.Fatalf("unknown item: code = %d, want 404", rw.Code)
	}
	row, err := p.Get(it.Key())
	if err != nil {
		t.Fatal(err)
	}
	if row.Disposition != "NEW" {
		t.Fatalf("disposition = %q, want NEW untouched", row.Disposition)
	}
}

func TestNotesPostIdempotent(t *testing.T) {
	s, p := testServer(t)
	it := testItem()
	if _, _, err := p.UpsertItem(&it, StatusEligible, "fit", nil, "", "2026-07-08T12:00:00Z"); err != nil {
		t.Fatal(err)
	}
	h := s.routes()
	path := "/item/example/repo%2342/notes"
	form := url.Values{"notes": {"check the flaky test first"}, "back": {"/shortlist"}}

	rw := postForm(t, h, path, form)
	if rw.Code != http.StatusSeeOther {
		t.Fatalf("code = %d, want 303", rw.Code)
	}
	row, err := p.Get(it.Key())
	if err != nil {
		t.Fatal(err)
	}
	if row.Notes != "check the flaky test first" {
		t.Fatalf("notes = %q", row.Notes)
	}
	if n := countActivities(t, p, "NOTES"); n != 1 {
		t.Fatalf("activities = %d, want 1", n)
	}
	// Unchanged notes re-submitted: no-op.
	postForm(t, h, path, form)
	if n := countActivities(t, p, "NOTES"); n != 1 {
		t.Fatalf("activities after double submit = %d, want 1", n)
	}
}

func TestSafeBack(t *testing.T) {
	for in, want := range map[string]string{
		"/review?status=quarantine": "/review?status=quarantine",
		"//evil.example":            "/review",
		"https://evil.example":      "/review",
		"":                          "/review",
	} {
		if got := safeBack(in); got != want {
			t.Errorf("safeBack(%q) = %q, want %q", in, got, want)
		}
	}
}
