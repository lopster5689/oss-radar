package main

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

// Item is one GitHub issue flattened to exactly what the pipeline stores and the
// gate reads. Sources build it; the gate never sees the wire format.
type Item struct {
	Repo          string
	Number        int
	Title         string
	URL           string
	Labels        []string
	Author        string
	CreatedAt     time.Time
	UpdatedAt     time.Time
	Comments      int
	Reactions     int
	Assignee      string
	State         string
	Locked        bool
	RepoArchived  bool
	LinkedPRCount int
	// RecentComments holds the bodies of the last page of comments. Fetched only
	// for items that survive the metadata gate, so it is empty at first evaluation.
	RecentComments []string
	Source         string
}

func (it Item) Key() string { return fmt.Sprintf("%s#%d", it.Repo, it.Number) }

const (
	StatusEligible   = "ELIGIBLE"
	StatusQuarantine = "QUARANTINE"
	StatusRejected   = "REJECTED"
)

// GateConfig is the compiled, pure input to Evaluate. Now is injected so the gate
// stays deterministic and testable.
type GateConfig struct {
	PositiveLabels      []string
	DisqualifyingLabels []string
	SkillKeywords       []string
	ClaimPatterns       []*regexp.Regexp
	AmbiguousPatterns   []*regexp.Regexp
	MaxAgeDays          int
	MaxComments         int
	Now                 time.Time
}

// Evaluate gates one item. Pure: no I/O, no clock, no randomness.
// Precedence: REJECTED beats QUARANTINE beats ELIGIBLE.
func Evaluate(it Item, cfg GateConfig) (status, reason string, fitHits []string) {
	fitHits = keywordHits(it.Title, it.Labels, cfg.SkillKeywords)
	positive := matchLabel(it.Labels, cfg.PositiveLabels)

	// Hard disqualifiers.
	switch {
	case it.State != "open":
		return StatusRejected, "issue is " + it.State, fitHits
	case it.Assignee != "":
		return StatusRejected, "assignee set: " + it.Assignee, fitHits
	case it.LinkedPRCount > 0:
		return StatusRejected, fmt.Sprintf("%d open linked PR(s)", it.LinkedPRCount), fitHits
	case it.Locked:
		return StatusRejected, "issue is locked", fitHits
	case it.RepoArchived:
		return StatusRejected, "repo is archived", fitHits
	case strings.HasSuffix(it.Author, "[bot]"):
		return StatusRejected, "bot author: " + it.Author, fitHits
	}
	if l := matchLabel(it.Labels, cfg.DisqualifyingLabels); l != "" {
		return StatusRejected, "disqualifying label: " + l, fitHits
	}
	if m := MatchClaim(it.RecentComments, cfg.ClaimPatterns); m != "" {
		return StatusRejected, fmt.Sprintf("claimed in comments: %q", m), fitHits
	}
	// Not in the spec's REJECTED list, but eligibility requires a fit signal and
	// quarantining every unrelated issue would flood the review queue. Precision
	// over recall: no positive label and no keyword hit means not our stock.
	if positive == "" && len(fitHits) == 0 {
		return StatusRejected, "no positive label and no skill keyword match", fitHits
	}

	// Quarantine triggers: human review, not rejection.
	if m := MatchClaim(it.RecentComments, cfg.AmbiguousPatterns); m != "" {
		return StatusQuarantine, fmt.Sprintf("ambiguous claim in comments: %q", m), fitHits
	}
	if cfg.Now.Sub(it.CreatedAt) > time.Duration(cfg.MaxAgeDays)*24*time.Hour {
		return StatusQuarantine, fmt.Sprintf("older than %d days (created %s)", cfg.MaxAgeDays, it.CreatedAt.Format("2006-01-02")), fitHits
	}
	if it.Comments > cfg.MaxComments {
		return StatusQuarantine, fmt.Sprintf("%d comments, over threshold %d", it.Comments, cfg.MaxComments), fitHits
	}
	if positive != "" && len(fitHits) == 0 {
		return StatusQuarantine, fmt.Sprintf("positive label %q but no skill keyword match", positive), fitHits
	}

	var parts []string
	if positive != "" {
		parts = append(parts, fmt.Sprintf("label %q", positive))
	}
	if len(fitHits) > 0 {
		parts = append(parts, "keywords "+strings.Join(fitHits, ","))
	}
	return StatusEligible, strings.Join(parts, "; "), fitHits
}

// MatchClaim returns the first pattern match found in texts, scanning texts in
// order and patterns in order, or "" if none match. Deterministic by construction.
func MatchClaim(texts []string, patterns []*regexp.Regexp) string {
	for _, t := range texts {
		for _, re := range patterns {
			if m := re.FindString(t); m != "" {
				return m
			}
		}
	}
	return ""
}

// matchLabel returns the first label that equals a configured entry or has a
// '/'-separated segment equal to one ("kind/duplicate" matches "duplicate").
// Case-insensitive. Returns "" if nothing matches.
func matchLabel(labels, entries []string) string {
	for _, raw := range labels {
		l := strings.ToLower(strings.TrimSpace(raw))
		for _, e := range entries {
			e = strings.ToLower(e)
			if l == e {
				return l
			}
			for _, seg := range strings.Split(l, "/") {
				if seg == e {
					return l
				}
			}
		}
	}
	return ""
}

func keywordHits(title string, labels, keywords []string) []string {
	text := strings.ToLower(title + " " + strings.Join(labels, " "))
	var hits []string
	for _, kw := range keywords {
		if containsToken(text, strings.ToLower(kw)) {
			hits = append(hits, kw)
		}
	}
	return hits
}

// containsToken reports whether sub occurs in s with non-alphanumeric boundaries,
// so "dns" matches "dns lookup" and "fix DNS." but not "coredns".
func containsToken(s, sub string) bool {
	if sub == "" {
		return false
	}
	for i := 0; ; {
		j := strings.Index(s[i:], sub)
		if j < 0 {
			return false
		}
		j += i
		end := j + len(sub)
		if (j == 0 || !isAlnum(s[j-1])) && (end == len(s) || !isAlnum(s[end])) {
			return true
		}
		i = j + 1
	}
}

func isAlnum(b byte) bool {
	return b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9'
}
