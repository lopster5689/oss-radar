package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

const timeFmt = time.RFC3339

type repoTally struct {
	Scanned    int `json:"scanned"`
	New        int `json:"new"`
	Eligible   int `json:"eligible"`
	Quarantine int `json:"quarantine"`
	Rejected   int `json:"rejected"`
}

type runStats struct {
	StartedAt  string                `json:"started_at"`
	Source     string                `json:"source"`
	Scanned    int                   `json:"scanned"`
	New        int                   `json:"new"`
	Updated    int                   `json:"updated"`
	Eligible   int                   `json:"eligible"`
	Quarantine int                   `json:"quarantine"`
	Rejected   int                   `json:"rejected"`
	APICalls   int                   `json:"api_calls"`
	DurationMs int64                 `json:"duration_ms"`
	PerRepo    map[string]*repoTally `json:"per_repo"`
}

func (rs *runStats) tally(repo string, inserted bool, status string) {
	t := rs.PerRepo[repo]
	if t == nil {
		t = &repoTally{}
		rs.PerRepo[repo] = t
	}
	rs.Scanned++
	t.Scanned++
	if inserted {
		rs.New++
		t.New++
	} else {
		rs.Updated++
	}
	switch status {
	case StatusEligible:
		rs.Eligible++
		t.Eligible++
	case StatusQuarantine:
		rs.Quarantine++
		t.Quarantine++
	case StatusRejected:
		rs.Rejected++
		t.Rejected++
	}
}

func (rs *runStats) summary() string {
	return fmt.Sprintf("scan source=%s scanned=%d new=%d updated=%d eligible=%d quarantine=%d rejected=%d api_calls=%d duration_ms=%d",
		rs.Source, rs.Scanned, rs.New, rs.Updated, rs.Eligible, rs.Quarantine, rs.Rejected, rs.APICalls, rs.DurationMs)
}

func runScan(p *Pool, c *Client, cfg *Config, src Source, dir string) error {
	start := time.Now().UTC()
	gc, err := cfg.GateConfig(start)
	if err != nil {
		return err
	}
	rs := &runStats{StartedAt: start.Format(timeFmt), Source: src.Name(), PerRepo: map[string]*repoTally{}}
	callsBefore := c.Calls()

	err = src.Discover(c, p, cfg, func(it *Item) error {
		return processItem(p, c, gc, it, rs)
	})
	if err != nil {
		return err
	}

	rs.APICalls = c.Calls() - callsBefore
	rs.DurationMs = time.Since(start).Milliseconds()
	if err := p.RecordRun(rs); err != nil {
		return err
	}
	if err := appendRunsJSONL(dir, rs); err != nil {
		return err
	}
	if err := appendAnalysisLog(dir, rs); err != nil {
		return err
	}
	fmt.Println(rs.summary())
	return nil
}

// processItem gates cheap fields first, deep-fetches only survivors, re-gates,
// then ingests. Metadata-rejected items cost zero extra API calls.
func processItem(p *Pool, c *Client, gc GateConfig, it *Item, rs *runStats) error {
	status, reason, fits := Evaluate(*it, gc)
	if status != StatusRejected && it.Comments > 0 {
		if err := fetchRecentComments(c, it); err != nil {
			return err
		}
		status, reason, fits = Evaluate(*it, gc)
	}
	if status != StatusRejected {
		if err := fetchLinkedPRCount(c, it); err != nil {
			return err
		}
		status, reason, fits = Evaluate(*it, gc)
	}

	claim := MatchClaim(it.RecentComments, gc.ClaimPatterns)
	if claim == "" {
		claim = MatchClaim(it.RecentComments, gc.AmbiguousPatterns)
	}
	now := gc.Now.Format(timeFmt)
	inserted, prev, err := p.UpsertItem(it, status, reason, fits, claim, now)
	if err != nil {
		return err
	}
	if inserted {
		if err := p.AddActivity(it.Key(), "DISCOVERED", fmt.Sprintf("source=%s status=%s", it.Source, status), now); err != nil {
			return err
		}
	} else if prev != status {
		if err := p.AddActivity(it.Key(), "GATE_CHANGED", prev+" -> "+status, now); err != nil {
			return err
		}
	}
	rs.tally(it.Repo, inserted, status)
	return nil
}

func appendRunsJSONL(dir string, rs *runStats) error {
	f, err := os.OpenFile(filepath.Join(dir, "runs.jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	return json.NewEncoder(f).Encode(rs)
}

const analysisHeader = `# oss-radar analysis log

Append-only lab log for the scan pipeline. Every run appends a dated stats block
below. Curated findings, gate tuning notes and source dead-ends get edited in by
hand under Findings. Machine-readable twin: runs.jsonl.

## Findings

(none yet)

---
`

func appendAnalysisLog(dir string, rs *runStats) error {
	path := filepath.Join(dir, "ANALYSIS_LOG.md")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		if err := os.WriteFile(path, []byte(analysisHeader), 0o644); err != nil {
			return err
		}
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()

	started, _ := time.Parse(timeFmt, rs.StartedAt)
	fmt.Fprintf(f, "\n## %s | source=%s\n", started.Format("2006-01-02 15:04 MST"), rs.Source)
	fmt.Fprintf(f, "scanned=%d new=%d updated=%d | eligible=%d quarantine=%d rejected=%d | api_calls=%d duration_ms=%d\n",
		rs.Scanned, rs.New, rs.Updated, rs.Eligible, rs.Quarantine, rs.Rejected, rs.APICalls, rs.DurationMs)
	repos := make([]string, 0, len(rs.PerRepo))
	for r := range rs.PerRepo {
		repos = append(repos, r)
	}
	sort.Strings(repos)
	for _, r := range repos {
		t := rs.PerRepo[r]
		fmt.Fprintf(f, "- %s: scanned=%d new=%d eligible=%d quarantine=%d rejected=%d\n",
			r, t.Scanned, t.New, t.Eligible, t.Quarantine, t.Rejected)
	}
	return nil
}
