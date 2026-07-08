package main

import (
	"strings"
	"testing"
	"time"
)

var gateNow = time.Date(2026, 7, 8, 12, 0, 0, 0, time.UTC)

func testGateConfig(t *testing.T) GateConfig {
	t.Helper()
	claim, err := compileAll([]string{
		`i('|’)?ll take`,
		`i('|’)?m working on (this|it)`,
		`(^|\s)/assign`,
		`can i (take|work on|try|pick up|have)`,
		`assign (this |it |the issue )?to me`,
		`assigned to me`,
	})
	if err != nil {
		t.Fatal(err)
	}
	ambiguous, err := compileAll([]string{
		`i can help`,
		`i('|’)?m interested`,
		`(^|\s)\+1\b`,
		`me too`,
		`anyone (else )?working on (this|it)`,
	})
	if err != nil {
		t.Fatal(err)
	}
	return GateConfig{
		PositiveLabels:      []string{"help wanted", "good first issue", "kind/bug"},
		DisqualifyingLabels: []string{"wontfix", "duplicate", "invalid", "question", "tracking", "epic", "meta", "needs-design"},
		SkillKeywords:       []string{"dns", "grpc", "cache", "timeout", "memory leak"},
		ClaimPatterns:       claim,
		AmbiguousPatterns:   ambiguous,
		MaxAgeDays:          180,
		MaxComments:         15,
		Now:                 gateNow,
	}
}

// baseItem is fresh, open, unassigned, positively labeled and keyword-fit: ELIGIBLE.
func baseItem() Item {
	return Item{
		Repo:      "example/repo",
		Number:    1,
		Title:     "Fix DNS timeout in resolver",
		Labels:    []string{"help wanted"},
		Author:    "alice",
		CreatedAt: gateNow.AddDate(0, 0, -10),
		State:     "open",
		Comments:  2,
	}
}

func TestEvaluate(t *testing.T) {
	cfg := testGateConfig(t)
	cases := []struct {
		name       string
		mutate     func(*Item)
		wantStatus string
		wantReason string // substring
	}{
		{"eligible baseline", func(it *Item) {}, StatusEligible, `label "help wanted"`},
		{"eligible keywords only, no positive label", func(it *Item) { it.Labels = nil }, StatusEligible, "keywords dns,timeout"},

		// REJECTED rules.
		{"closed", func(it *Item) { it.State = "closed" }, StatusRejected, "issue is closed"},
		{"assignee set", func(it *Item) { it.Assignee = "bob" }, StatusRejected, "assignee set: bob"},
		{"linked open PR", func(it *Item) { it.LinkedPRCount = 2 }, StatusRejected, "2 open linked PR"},
		{"locked", func(it *Item) { it.Locked = true }, StatusRejected, "locked"},
		{"repo archived", func(it *Item) { it.RepoArchived = true }, StatusRejected, "archived"},
		{"bot author", func(it *Item) { it.Author = "renovate[bot]" }, StatusRejected, "bot author"},
		{"disqualifying label", func(it *Item) { it.Labels = append(it.Labels, "wontfix") }, StatusRejected, "disqualifying label: wontfix"},
		{"disqualifying label segment", func(it *Item) { it.Labels = append(it.Labels, "kind/duplicate") }, StatusRejected, "disqualifying label: kind/duplicate"},
		{"hard claim in comments", func(it *Item) { it.RecentComments = []string{"I'll take this one"} }, StatusRejected, "claimed in comments"},
		{"hard claim case-insensitive", func(it *Item) { it.RecentComments = []string{"I'M WORKING ON THIS now"} }, StatusRejected, "claimed in comments"},
		{"slash assign command", func(it *Item) { it.RecentComments = []string{"/assign"} }, StatusRejected, "claimed in comments"},
		{"can i take", func(it *Item) { it.RecentComments = []string{"Can I take this issue?"} }, StatusRejected, "claimed in comments"},
		{"no fit signal at all", func(it *Item) { it.Labels = nil; it.Title = "Improve documentation wording" }, StatusRejected, "no positive label and no skill keyword"},

		// QUARANTINE rules.
		{"ambiguous claim", func(it *Item) { it.RecentComments = []string{"I'm interested in this"} }, StatusQuarantine, "ambiguous claim"},
		{"plus one me too", func(it *Item) { it.RecentComments = []string{"+1 me too"} }, StatusQuarantine, "ambiguous claim"},
		{"anyone working question", func(it *Item) { it.RecentComments = []string{"Is anyone working on this?"} }, StatusQuarantine, "ambiguous claim"},
		{"older than 180 days", func(it *Item) { it.CreatedAt = gateNow.AddDate(0, 0, -200) }, StatusQuarantine, "older than 180 days"},
		{"heavy discussion", func(it *Item) { it.Comments = 16 }, StatusQuarantine, "over threshold 15"},
		{"positive label but no keyword", func(it *Item) { it.Title = "Improve error message wording" }, StatusQuarantine, "no skill keyword match"},

		// Precedence: REJECTED beats QUARANTINE beats ELIGIBLE.
		{"closed beats old", func(it *Item) { it.State = "closed"; it.CreatedAt = gateNow.AddDate(0, 0, -400) }, StatusRejected, "issue is closed"},
		{"assignee beats ambiguous claim", func(it *Item) { it.Assignee = "bob"; it.RecentComments = []string{"me too"} }, StatusRejected, "assignee set"},
		{"hard claim beats old", func(it *Item) {
			it.CreatedAt = gateNow.AddDate(0, 0, -400)
			it.RecentComments = []string{"assigned to me please"}
		}, StatusRejected, "claimed in comments"},
		{"ambiguous beats eligible", func(it *Item) { it.RecentComments = []string{"i can help with this"} }, StatusQuarantine, "ambiguous claim"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			it := baseItem()
			tc.mutate(&it)
			status, reason, _ := Evaluate(it, cfg)
			if status != tc.wantStatus {
				t.Fatalf("status = %s (reason %q), want %s", status, reason, tc.wantStatus)
			}
			if !strings.Contains(reason, tc.wantReason) {
				t.Fatalf("reason = %q, want substring %q", reason, tc.wantReason)
			}
		})
	}
}

func TestEvaluateFitHits(t *testing.T) {
	cfg := testGateConfig(t)
	it := baseItem()
	it.Title = "Memory leak in gRPC cache layer"
	_, _, fits := Evaluate(it, cfg)
	want := []string{"grpc", "cache", "memory leak"}
	if len(fits) != len(want) {
		t.Fatalf("fits = %v, want %v", fits, want)
	}
	for i := range want {
		if fits[i] != want[i] {
			t.Fatalf("fits = %v, want %v", fits, want)
		}
	}
}

func TestEvaluateIsPure(t *testing.T) {
	cfg := testGateConfig(t)
	it := baseItem()
	it.RecentComments = []string{"looks good", "+1"}
	s1, r1, _ := Evaluate(it, cfg)
	s2, r2, _ := Evaluate(it, cfg)
	if s1 != s2 || r1 != r2 {
		t.Fatalf("Evaluate not deterministic: (%s,%q) vs (%s,%q)", s1, r1, s2, r2)
	}
}

func TestContainsTokenBoundaries(t *testing.T) {
	cases := []struct {
		text, kw string
		want     bool
	}{
		{"fix dns lookup", "dns", true},
		{"fix (DNS) lookup", "dns", true}, // caller lowercases
		{"coredns plugin", "dns", false},
		{"udp5 handling", "udp", false},
		{"memory leak in parser", "memory leak", true},
	}
	for _, tc := range cases {
		if got := containsToken(strings.ToLower(tc.text), tc.kw); got != tc.want {
			t.Errorf("containsToken(%q, %q) = %v, want %v", tc.text, tc.kw, got, tc.want)
		}
	}
}

// TestConfigCompiles guards the shipped config: every regex must compile and the
// gate section must satisfy validation.
func TestConfigCompiles(t *testing.T) {
	cfg, err := LoadConfig("config.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cfg.GateConfig(gateNow); err != nil {
		t.Fatal(err)
	}
	if len(cfg.Watchlist.Repos) != 8 {
		t.Fatalf("watchlist repos = %d, want 8", len(cfg.Watchlist.Repos))
	}
}
