package main

import (
	"bytes"
	"database/sql"
	"embed"
	"encoding/json"
	"flag"
	"fmt"
	"html/template"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

//go:embed web
var webFS embed.FS

// server is the localhost cockpit (model component 8). It writes only the
// operator-owned fields, disposition and notes, and every such write appends
// an activities row so the timeline stays append-only truth. Scans triggered
// from the UI go through the exact same runScan path as the CLI.
type server struct {
	pool    *Pool
	cfgPath string
	dir     string

	mu       sync.Mutex
	scanning bool
	lastScan string
}

func cmdServe(cfgPath, dbPath string, args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	addr := fs.String("addr", "127.0.0.1:7333", "listen address")
	tick := fs.Duration("tick", 0, "run a full scan every interval while serving (0 = off)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	addrSet := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "addr" {
			addrSet = true
		}
	})
	host, _, err := net.SplitHostPort(*addr)
	if err != nil {
		return fmt.Errorf("bad --addr %q: %w", *addr, err)
	}
	if !isLoopback(host) {
		if !addrSet {
			return fmt.Errorf("refusing to bind non-loopback %s without an explicit --addr", *addr)
		}
		fmt.Fprintf(os.Stderr, "warning: %s is not loopback, the dashboard has no auth\n", *addr)
	}

	// Fail fast on a broken config instead of at the first scan click.
	if _, err := LoadConfig(cfgPath); err != nil {
		return err
	}
	p, err := OpenPool(dbPath)
	if err != nil {
		return err
	}
	defer p.Close()

	s := &server{pool: p, cfgPath: cfgPath, dir: filepath.Dir(dbPath)}
	if *tick > 0 {
		go func() {
			for range time.Tick(*tick) {
				s.startScan()
			}
		}()
	}
	fmt.Printf("radar dashboard on http://%s\n", *addr)
	return http.ListenAndServe(*addr, s.routes())
}

func isLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func (s *server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.handleOverview)
	mux.HandleFunc("GET /review", s.handleReview)
	mux.HandleFunc("GET /shortlist", s.handleShortlist)
	mux.HandleFunc("GET /dropped", s.handleDropped)
	mux.HandleFunc("GET /api/summary", s.handleSummary)
	mux.HandleFunc("POST /scan", s.handleScan)
	mux.HandleFunc("POST /item/", s.handleItem)
	mux.HandleFunc("GET /static/style.css", func(w http.ResponseWriter, r *http.Request) {
		b, err := webFS.ReadFile("web/style.css")
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
		w.Write(b)
	})
	return mux
}

var templateFuncs = template.FuncMap{
	// ageDays renders issue age as a compact day count.
	"ageDays": func(ts string) string {
		t, err := time.Parse(timeFmt, ts)
		if err != nil {
			return "?"
		}
		return fmt.Sprintf("%dd", int(time.Since(t).Hours()/24))
	},
	"fmtTime": func(ts string) string {
		t, err := time.Parse(timeFmt, ts)
		if err != nil {
			return ts
		}
		return t.Format("2006-01-02 15:04")
	},
	"fmtDur": func(ms int64) string {
		return (time.Duration(ms) * time.Millisecond).Round(100 * time.Millisecond).String()
	},
	// itemPath builds a POST target for one item. The '#' in the dedupe key must
	// be percent-encoded or the browser treats the rest as a fragment.
	"itemPath": func(key, action string) string {
		return "/item/" + strings.ReplaceAll(key, "#", "%23") + "/" + action
	},
	"add": func(a, b int) int { return a + b },
	"sub": func(a, b int) int { return a - b },
}

var pageTemplates = func() map[string]*template.Template {
	out := map[string]*template.Template{}
	for _, p := range []string{"overview", "review", "shortlist", "dropped"} {
		out[p] = template.Must(template.New("layout.html").Funcs(templateFuncs).
			ParseFS(webFS, "web/layout.html", "web/"+p+".html"))
	}
	return out
}()

func (s *server) render(w http.ResponseWriter, page string, data any) {
	var buf bytes.Buffer
	if err := pageTemplates[page].ExecuteTemplate(&buf, "layout.html", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	buf.WriteTo(w)
}

// startScan launches a full scan in the background unless one is already
// running. Returns whether a scan was started.
func (s *server) startScan() bool {
	s.mu.Lock()
	if s.scanning {
		s.mu.Unlock()
		return false
	}
	s.scanning = true
	s.mu.Unlock()
	go func() {
		result := s.scanAll()
		s.mu.Lock()
		s.scanning = false
		s.lastScan = result
		s.mu.Unlock()
	}()
	return true
}

// scanAll runs all sources through the same path as `radar scan`: same gate,
// same logging to the runs table, runs.jsonl and ANALYSIS_LOG.md. The returned
// line is shown on the overview; errors are surfaced there, never swallowed.
func (s *server) scanAll() string {
	ts := time.Now().UTC().Format(timeFmt)
	cfg, err := LoadConfig(s.cfgPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "scan:", err)
		return ts + " scan failed: " + err.Error()
	}
	c, err := NewClient()
	if err != nil {
		fmt.Fprintln(os.Stderr, "scan:", err)
		return ts + " scan failed: " + err.Error()
	}
	var parts []string
	for _, src := range []Source{watchlistSource{}, searchSource{}} {
		rs, err := runScan(s.pool, c, cfg, src, s.dir)
		if err != nil {
			fmt.Fprintln(os.Stderr, "scan:", err)
			return fmt.Sprintf("%s %s scan failed: %v", ts, src.Name(), err)
		}
		parts = append(parts, fmt.Sprintf("%s scanned=%d new=%d eligible=%d quarantine=%d",
			src.Name(), rs.Scanned, rs.New, rs.Eligible, rs.Quarantine))
	}
	return ts + " ok: " + strings.Join(parts, " | ")
}

func (s *server) handleScan(w http.ResponseWriter, r *http.Request) {
	s.startScan()
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

type overviewData struct {
	Title, Active string
	Total, Stale  int
	Gate, Disp    map[string]int
	EligibleRepos []repoEligible
	LastBySource  map[string]string
	Runs          []runRow
	APICallsToday int
	Scanning      bool
	LastScan      string
}

func (s *server) handleOverview(w http.ResponseWriter, r *http.Request) {
	d := overviewData{Title: "overview", Active: "overview"}
	var err error
	if d.Gate, d.Total, err = s.pool.countBy("gate_status"); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if d.Disp, _, err = s.pool.countBy("disposition"); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	staleCutoff := time.Now().UTC().Add(-7 * 24 * time.Hour).Format(timeFmt)
	if d.Stale, err = s.pool.staleCount(staleCutoff); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if d.EligibleRepos, err = s.pool.eligibleByRepoWeighted(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if d.LastBySource, err = s.pool.lastRunPerSource(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if d.Runs, err = s.pool.recentRuns(10); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if d.APICallsToday, err = s.pool.apiCallsSince(time.Now().UTC().Format("2006-01-02")); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.mu.Lock()
	d.Scanning, d.LastScan = s.scanning, s.lastScan
	s.mu.Unlock()
	s.render(w, "overview", d)
}

func (s *server) handleSummary(w http.ResponseWriter, r *http.Request) {
	gate, total, err := s.pool.countBy("gate_status")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	disp, _, err := s.pool.countBy("disposition")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	staleCutoff := time.Now().UTC().Add(-7 * 24 * time.Hour).Format(timeFmt)
	stale, err := s.pool.staleCount(staleCutoff)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	calls, err := s.pool.apiCallsSince(time.Now().UTC().Format("2006-01-02"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.mu.Lock()
	scanning, lastScan := s.scanning, s.lastScan
	s.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"pool":            total,
		"eligible":        gate[StatusEligible],
		"quarantine":      gate[StatusQuarantine],
		"rejected":        gate[StatusRejected],
		"new":             disp["NEW"],
		"shortlisted":     disp["SHORTLISTED"],
		"dropped":         disp["DROPPED"],
		"stale":           stale,
		"api_calls_today": calls,
		"scanning":        scanning,
		"last_scan":       lastScan,
	})
}

type reviewData struct {
	Title, Active   string
	Status, Repo, Q string
	Back            string
	Repos           []string
	Items           []ItemRow
}

func (s *server) handleReview(w http.ResponseWriter, r *http.Request) {
	d := reviewData{
		Title:  "review",
		Active: "review",
		Status: r.FormValue("status"),
		Repo:   r.FormValue("repo"),
		Q:      r.FormValue("q"),
		Back:   r.URL.RequestURI(),
	}
	if d.Status == "" {
		d.Status = "all"
	}
	var err error
	if d.Items, err = s.pool.ReviewQueue(d.Status, d.Repo, d.Q); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if d.Repos, err = s.pool.ReviewRepos(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.render(w, "review", d)
}

type shortlistData struct {
	Title, Active string
	Items         []ItemRow
}

func (s *server) handleShortlist(w http.ResponseWriter, r *http.Request) {
	items, err := s.pool.ByDisposition("SHORTLISTED", 500, 0)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.render(w, "shortlist", shortlistData{Title: "shortlist", Active: "shortlist", Items: items})
}

const droppedPerPage = 50

type droppedData struct {
	Title, Active      string
	Items              []ItemRow
	Page, Pages, Total int
}

func (s *server) handleDropped(w http.ResponseWriter, r *http.Request) {
	page, _ := strconv.Atoi(r.FormValue("page"))
	if page < 1 {
		page = 1
	}
	disp, _, err := s.pool.countBy("disposition")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	total := disp["DROPPED"]
	items, err := s.pool.ByDisposition("DROPPED", droppedPerPage, (page-1)*droppedPerPage)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	pages := (total + droppedPerPage - 1) / droppedPerPage
	if pages < 1 {
		pages = 1
	}
	s.render(w, "dropped", droppedData{
		Title: "dropped", Active: "dropped",
		Items: items, Page: page, Pages: pages, Total: total,
	})
}

// handleItem serves POST /item/{key}/disposition and POST /item/{key}/notes.
// Both writes are idempotent: a no-change submit neither errors nor appends an
// activity, so double-submits are harmless. Real changes append DISPOSITION or
// NOTES activities with nanosecond timestamps in the dedupe hash, so repeated
// legitimate transitions within one day are all recorded.
func (s *server) handleItem(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/item/")
	i := strings.LastIndex(rest, "/")
	if i <= 0 {
		http.NotFound(w, r)
		return
	}
	key, action := rest[:i], rest[i+1:]

	cur, err := s.pool.Get(key)
	if err == sql.ErrNoRows {
		http.Error(w, "no such item: "+key, http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	switch action {
	case "disposition":
		disp := r.FormValue("disposition")
		switch disp {
		case "NEW", "SHORTLISTED", "DROPPED":
		default:
			http.Error(w, "bad disposition: "+disp, http.StatusBadRequest)
			return
		}
		if cur.Disposition != disp {
			if err := s.pool.SetDisposition(key, disp); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			if err := s.pool.AddOperatorActivity(key, "DISPOSITION", cur.Disposition+" -> "+disp, now); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
		}
	case "notes":
		notes := r.FormValue("notes")
		if cur.Notes != notes {
			if err := s.pool.SetNotes(key, notes); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			if err := s.pool.AddOperatorActivity(key, "NOTES", truncate(notes, 120), now); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
		}
	default:
		http.NotFound(w, r)
		return
	}
	http.Redirect(w, r, safeBack(r.FormValue("back")), http.StatusSeeOther)
}

// safeBack keeps redirects on-site: relative paths only, no scheme-relative
// URLs.
func safeBack(back string) string {
	if strings.HasPrefix(back, "/") && !strings.HasPrefix(back, "//") {
		return back
	}
	return "/review"
}
