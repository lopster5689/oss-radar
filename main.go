package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const usageText = `usage: radar [-config path] [-db path] <command> [args]

commands:
  scan [--source watchlist|search|all]   discover, gate and ingest issues
  status                                 pool totals, per-repo eligible, staleness
  review [--limit N]                     eligible + quarantine items, newest first
  shortlist <owner/repo#N>               set disposition SHORTLISTED
  drop <owner/repo#N>                    set disposition DROPPED
  show <owner/repo#N>                    full stored record for one item
`

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "radar:", err)
		os.Exit(1)
	}
}

func run() error {
	cfgPath := flag.String("config", "config.yaml", "path to config.yaml")
	dbPath := flag.String("db", "radar.db", "path to the sqlite pool")
	flag.Usage = func() { fmt.Fprint(os.Stderr, usageText) }
	flag.Parse()
	args := flag.Args()
	if len(args) == 0 {
		flag.Usage()
		return errors.New("missing command")
	}

	cmd, rest := args[0], args[1:]
	switch cmd {
	case "scan":
		return cmdScan(*cfgPath, *dbPath, rest)
	case "status":
		return cmdStatus(*dbPath)
	case "review":
		return cmdReview(*dbPath, rest)
	case "shortlist":
		return cmdDisposition(*dbPath, rest, "SHORTLISTED")
	case "drop":
		return cmdDisposition(*dbPath, rest, "DROPPED")
	case "show":
		return cmdShow(*dbPath, rest)
	default:
		flag.Usage()
		return fmt.Errorf("unknown command %q", cmd)
	}
}

func cmdScan(cfgPath, dbPath string, args []string) error {
	fs := flag.NewFlagSet("scan", flag.ContinueOnError)
	source := fs.String("source", "all", "watchlist|search|all")
	if err := fs.Parse(args); err != nil {
		return err
	}
	var sources []Source
	switch *source {
	case "watchlist":
		sources = []Source{watchlistSource{}}
	case "search":
		sources = []Source{searchSource{}}
	case "all":
		sources = []Source{watchlistSource{}, searchSource{}}
	default:
		return fmt.Errorf("unknown source %q", *source)
	}

	cfg, err := LoadConfig(cfgPath)
	if err != nil {
		return err
	}
	p, err := OpenPool(dbPath)
	if err != nil {
		return err
	}
	defer p.Close()
	c, err := NewClient()
	if err != nil {
		return err
	}
	dir := filepath.Dir(dbPath)
	for _, s := range sources {
		if err := runScan(p, c, cfg, s, dir); err != nil {
			return err
		}
	}
	return nil
}

func cmdStatus(dbPath string) error {
	p, err := OpenPool(dbPath)
	if err != nil {
		return err
	}
	defer p.Close()

	byStatus, total, err := p.countBy("gate_status")
	if err != nil {
		return err
	}
	byDisp, _, err := p.countBy("disposition")
	if err != nil {
		return err
	}
	fmt.Printf("pool: %d items\n\ngate status:\n", total)
	for _, s := range []string{StatusEligible, StatusQuarantine, StatusRejected} {
		fmt.Printf("  %-11s %5d\n", s, byStatus[s])
	}
	fmt.Println("\ndisposition:")
	disps := make([]string, 0, len(byDisp))
	for d := range byDisp {
		disps = append(disps, d)
	}
	sort.Strings(disps)
	for _, d := range disps {
		fmt.Printf("  %-11s %5d\n", d, byDisp[d])
	}

	eligible, err := p.eligibleByRepo()
	if err != nil {
		return err
	}
	fmt.Println("\neligible by repo:")
	if len(eligible) == 0 {
		fmt.Println("  (none)")
	}
	for _, rc := range eligible {
		fmt.Printf("  %-50s %4d\n", rc.Repo, rc.N)
	}

	repos, err := p.repoRows()
	if err != nil {
		return err
	}
	fmt.Println("\nwatchlist repos (last scan):")
	for _, r := range repos {
		last := r.LastScanAt
		if last == "" {
			last = "never"
		}
		note := ""
		if r.Archived == 1 {
			note = "  [archived]"
		}
		fmt.Printf("  %-50s w=%.1f  %s%s\n", r.Name, r.Weight, last, note)
	}

	cutoff := time.Now().UTC().Add(-7 * 24 * time.Hour).Format(timeFmt)
	stale, err := p.staleCount(cutoff)
	if err != nil {
		return err
	}
	fmt.Printf("\nstale: %d eligible/quarantine items verified over 7 days ago\n", stale)
	return nil
}

func cmdReview(dbPath string, args []string) error {
	fs := flag.NewFlagSet("review", flag.ContinueOnError)
	limit := fs.Int("limit", 20, "max items to list")
	if err := fs.Parse(args); err != nil {
		return err
	}
	p, err := OpenPool(dbPath)
	if err != nil {
		return err
	}
	defer p.Close()
	items, err := p.Review(*limit)
	if err != nil {
		return err
	}
	if len(items) == 0 {
		fmt.Println("review queue is empty")
		return nil
	}
	now := time.Now().UTC()
	for _, it := range items {
		age := "?"
		if t, err := time.Parse(timeFmt, it.CreatedAt); err == nil {
			age = fmt.Sprintf("%dd", int(now.Sub(t).Hours()/24))
		}
		fmt.Printf("%-52s %5s  %-10s  %s\n", it.DedupeKey, age, it.GateStatus, truncate(it.Title, 78))
		fits := it.FitHits
		if fits == "" {
			fits = "-"
		}
		fmt.Printf("    %s | fits: %s | %s\n", it.GateReason, fits, it.URL)
	}
	return nil
}

func cmdDisposition(dbPath string, args []string, disp string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: radar %s <owner/repo#N>", strings.ToLower(disp[:4]))
	}
	key := args[0]
	p, err := OpenPool(dbPath)
	if err != nil {
		return err
	}
	defer p.Close()
	if err := p.SetDisposition(key, disp); err != nil {
		if err == sql.ErrNoRows {
			return fmt.Errorf("no such item: %s", key)
		}
		return err
	}
	now := time.Now().UTC().Format(timeFmt)
	if err := p.AddActivity(key, "DISPOSITION", disp, now); err != nil {
		return err
	}
	fmt.Printf("%s -> %s\n", key, disp)
	return nil
}

func cmdShow(dbPath string, args []string) error {
	if len(args) != 1 {
		return errors.New("usage: radar show <owner/repo#N>")
	}
	p, err := OpenPool(dbPath)
	if err != nil {
		return err
	}
	defer p.Close()
	it, err := p.Get(args[0])
	if err != nil {
		if err == sql.ErrNoRows {
			return fmt.Errorf("no such item: %s", args[0])
		}
		return err
	}
	var labels []string
	_ = json.Unmarshal([]byte(it.Labels), &labels)
	rows := []struct{ k, v string }{
		{"key", it.DedupeKey},
		{"title", it.Title},
		{"url", it.URL},
		{"labels", strings.Join(labels, ", ")},
		{"author", it.Author},
		{"created_at", it.CreatedAt},
		{"updated_at", it.UpdatedAt},
		{"comments", fmt.Sprint(it.Comments)},
		{"reactions", fmt.Sprint(it.Reactions)},
		{"assignee", it.Assignee},
		{"linked_prs", fmt.Sprint(it.LinkedPRCount)},
		{"claim_signal", it.ClaimSignal},
		{"gate_status", it.GateStatus},
		{"gate_reason", it.GateReason},
		{"fit_hits", it.FitHits},
		{"source", it.Source},
		{"first_seen_at", it.FirstSeenAt},
		{"verified_at", it.VerifiedAt},
		{"disposition", it.Disposition},
		{"notes", it.Notes},
	}
	for _, r := range rows {
		fmt.Printf("%-14s %s\n", r.k+":", r.v)
	}
	return nil
}
