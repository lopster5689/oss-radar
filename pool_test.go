package main

import (
	"path/filepath"
	"testing"
	"time"
)

func testPool(t *testing.T) *Pool {
	t.Helper()
	p, err := OpenPool(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { p.Close() })
	return p
}

func testItem() Item {
	return Item{
		Repo:      "example/repo",
		Number:    42,
		Title:     "Fix DNS timeout",
		URL:       "https://github.com/example/repo/issues/42",
		Labels:    []string{"help wanted"},
		Author:    "alice",
		CreatedAt: time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC),
		UpdatedAt: time.Date(2026, 7, 2, 0, 0, 0, 0, time.UTC),
		State:     "open",
		Source:    "watchlist",
	}
}

// Ingest contract: re-pushing the same item produces zero duplicate rows.
func TestUpsertIdempotent(t *testing.T) {
	p := testPool(t)
	it := testItem()
	now := "2026-07-08T12:00:00Z"

	inserted, _, err := p.UpsertItem(&it, StatusEligible, "fit", []string{"dns"}, "", now)
	if err != nil || !inserted {
		t.Fatalf("first upsert: inserted=%v err=%v", inserted, err)
	}
	inserted, prev, err := p.UpsertItem(&it, StatusEligible, "fit", []string{"dns"}, "", now)
	if err != nil || inserted {
		t.Fatalf("second upsert: inserted=%v err=%v", inserted, err)
	}
	if prev != StatusEligible {
		t.Fatalf("prev status = %q", prev)
	}
	var n int
	if err := p.db.QueryRow(`SELECT COUNT(*) FROM items`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("items count = %d err=%v, want 1", n, err)
	}
}

// Ingest contract: updates touch only acquisition and gate fields, never
// disposition, notes or first_seen_at.
func TestUpsertPreservesOperatorFields(t *testing.T) {
	p := testPool(t)
	it := testItem()
	if _, _, err := p.UpsertItem(&it, StatusEligible, "fit", nil, "", "2026-07-08T12:00:00Z"); err != nil {
		t.Fatal(err)
	}
	if err := p.SetDisposition(it.Key(), "SHORTLISTED"); err != nil {
		t.Fatal(err)
	}
	if _, err := p.db.Exec(`UPDATE items SET notes = 'my note' WHERE dedupe_key = ?`, it.Key()); err != nil {
		t.Fatal(err)
	}

	it.Title = "Fix DNS timeout (updated)"
	if _, _, err := p.UpsertItem(&it, StatusQuarantine, "older", nil, "", "2026-07-09T12:00:00Z"); err != nil {
		t.Fatal(err)
	}
	row, err := p.Get(it.Key())
	if err != nil {
		t.Fatal(err)
	}
	if row.Title != "Fix DNS timeout (updated)" || row.GateStatus != StatusQuarantine {
		t.Fatalf("acquisition fields not refreshed: %+v", row)
	}
	if row.Disposition != "SHORTLISTED" || row.Notes != "my note" {
		t.Fatalf("operator fields clobbered: disposition=%q notes=%q", row.Disposition, row.Notes)
	}
	if row.FirstSeenAt != "2026-07-08T12:00:00Z" || row.VerifiedAt != "2026-07-09T12:00:00Z" {
		t.Fatalf("timestamps wrong: first_seen=%q verified=%q", row.FirstSeenAt, row.VerifiedAt)
	}
}

// An item found by both sources keeps watchlist as its source.
func TestSourceWatchlistWins(t *testing.T) {
	p := testPool(t)
	it := testItem()
	if _, _, err := p.UpsertItem(&it, StatusEligible, "fit", nil, "", "2026-07-08T12:00:00Z"); err != nil {
		t.Fatal(err)
	}
	it.Source = "search"
	if _, _, err := p.UpsertItem(&it, StatusEligible, "fit", nil, "", "2026-07-08T13:00:00Z"); err != nil {
		t.Fatal(err)
	}
	row, err := p.Get(it.Key())
	if err != nil {
		t.Fatal(err)
	}
	if row.Source != "watchlist" {
		t.Fatalf("source = %q, want watchlist", row.Source)
	}
}

func TestActivityDedupe(t *testing.T) {
	p := testPool(t)
	for i := 0; i < 2; i++ {
		if err := p.AddActivity("example/repo#42", "DISCOVERED", "source=watchlist status=ELIGIBLE", "2026-07-08T12:00:00Z"); err != nil {
			t.Fatal(err)
		}
	}
	var n int
	if err := p.db.QueryRow(`SELECT COUNT(*) FROM activities`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("activities count = %d err=%v, want 1", n, err)
	}
}
