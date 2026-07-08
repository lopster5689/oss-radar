package main

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"strings"

	_ "modernc.org/sqlite"
)

// Pool is the canonical inventory. Axes are stored separately: gate_status is
// machine-owned and refreshed every scan; disposition and notes are operator-owned
// and never touched by ingest (partial-update guard, model invariant 4).
type Pool struct{ db *sql.DB }

const schema = `
CREATE TABLE IF NOT EXISTS items (
  dedupe_key      TEXT PRIMARY KEY,
  repo            TEXT NOT NULL,
  number          INTEGER NOT NULL,
  title           TEXT NOT NULL,
  url             TEXT NOT NULL,
  labels          TEXT NOT NULL DEFAULT '[]',
  author          TEXT NOT NULL DEFAULT '',
  created_at      TEXT NOT NULL,
  updated_at      TEXT NOT NULL,
  comments        INTEGER NOT NULL DEFAULT 0,
  reactions       INTEGER NOT NULL DEFAULT 0,
  assignee        TEXT NOT NULL DEFAULT '',
  linked_pr_count INTEGER NOT NULL DEFAULT 0,
  claim_signal    TEXT NOT NULL DEFAULT '',
  gate_status     TEXT NOT NULL,
  gate_reason     TEXT NOT NULL DEFAULT '',
  fit_hits        TEXT NOT NULL DEFAULT '',
  source          TEXT NOT NULL,
  first_seen_at   TEXT NOT NULL,
  verified_at     TEXT NOT NULL,
  disposition     TEXT NOT NULL DEFAULT 'NEW',
  notes           TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS repos (
  name               TEXT PRIMARY KEY,
  weight             REAL NOT NULL DEFAULT 0,
  archived           INTEGER NOT NULL DEFAULT 0,
  last_scan_at       TEXT,
  outside_merge_rate REAL,
  median_review_days REAL,
  health_checked_at  TEXT
);
CREATE TABLE IF NOT EXISTS activities (
  id          INTEGER PRIMARY KEY,
  item_key    TEXT NOT NULL,
  kind        TEXT NOT NULL,
  detail      TEXT NOT NULL DEFAULT '',
  occurred_at TEXT NOT NULL,
  dedupe_hash TEXT NOT NULL UNIQUE
);
CREATE TABLE IF NOT EXISTS runs (
  id          INTEGER PRIMARY KEY,
  started_at  TEXT NOT NULL,
  source      TEXT NOT NULL,
  scanned     INTEGER NOT NULL,
  new_items   INTEGER NOT NULL,
  updated     INTEGER NOT NULL,
  eligible    INTEGER NOT NULL,
  quarantine  INTEGER NOT NULL,
  rejected    INTEGER NOT NULL,
  api_calls   INTEGER NOT NULL,
  duration_ms INTEGER NOT NULL
);
`

func OpenPool(path string) (*Pool, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec("PRAGMA journal_mode=WAL; PRAGMA busy_timeout=5000;" + schema); err != nil {
		db.Close()
		return nil, err
	}
	return &Pool{db}, nil
}

func (p *Pool) Close() error { return p.db.Close() }

// UpsertItem is the ingest boundary. Insert on first sight; on update touch only
// acquisition and gate fields. first_seen_at, disposition and notes are never
// written on the update path. Returns whether the row was new and the previous
// gate_status so the caller can journal transitions.
func (p *Pool) UpsertItem(it *Item, status, reason string, fitHits []string, claim, now string) (inserted bool, prevStatus string, err error) {
	labels, err := json.Marshal(it.Labels)
	if err != nil {
		return false, "", err
	}
	fits := strings.Join(fitHits, ",")

	var prevSource string
	row := p.db.QueryRow(`SELECT gate_status, source FROM items WHERE dedupe_key = ?`, it.Key())
	switch err := row.Scan(&prevStatus, &prevSource); {
	case err == sql.ErrNoRows:
		_, err = p.db.Exec(`INSERT INTO items
			(dedupe_key, repo, number, title, url, labels, author, created_at, updated_at,
			 comments, reactions, assignee, linked_pr_count, claim_signal,
			 gate_status, gate_reason, fit_hits, source, first_seen_at, verified_at)
			VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			it.Key(), it.Repo, it.Number, it.Title, it.URL, string(labels), it.Author,
			it.CreatedAt.Format(timeFmt), it.UpdatedAt.Format(timeFmt),
			it.Comments, it.Reactions, it.Assignee, it.LinkedPRCount, claim,
			status, reason, fits, it.Source, now, now)
		return true, "", err
	case err != nil:
		return false, "", err
	}

	// An item found by both sources keeps watchlist as its source.
	source := it.Source
	if prevSource == "watchlist" {
		source = "watchlist"
	}
	_, err = p.db.Exec(`UPDATE items SET
		title=?, url=?, labels=?, author=?, created_at=?, updated_at=?,
		comments=?, reactions=?, assignee=?, linked_pr_count=?, claim_signal=?,
		gate_status=?, gate_reason=?, fit_hits=?, source=?, verified_at=?
		WHERE dedupe_key=?`,
		it.Title, it.URL, string(labels), it.Author,
		it.CreatedAt.Format(timeFmt), it.UpdatedAt.Format(timeFmt),
		it.Comments, it.Reactions, it.Assignee, it.LinkedPRCount, claim,
		status, reason, fits, source, now, it.Key())
	return false, prevStatus, err
}

// AddActivity appends to the per-item timeline, deduped by content hash within a
// day so idempotent re-runs cannot double-log the same event.
func (p *Pool) AddActivity(itemKey, kind, detail, occurredAt string) error {
	day := occurredAt
	if len(day) > 10 {
		day = day[:10]
	}
	sum := sha256.Sum256([]byte(itemKey + "|" + kind + "|" + detail + "|" + day))
	_, err := p.db.Exec(`INSERT OR IGNORE INTO activities (item_key, kind, detail, occurred_at, dedupe_hash)
		VALUES (?,?,?,?,?)`, itemKey, kind, detail, occurredAt, hex.EncodeToString(sum[:]))
	return err
}

func (p *Pool) UpsertRepo(name string, weight float64, archived bool) error {
	_, err := p.db.Exec(`INSERT INTO repos (name, weight, archived) VALUES (?,?,?)
		ON CONFLICT(name) DO UPDATE SET weight=excluded.weight, archived=excluded.archived`,
		name, weight, boolInt(archived))
	return err
}

func (p *Pool) RepoCursor(name string) (string, error) {
	var cur sql.NullString
	err := p.db.QueryRow(`SELECT last_scan_at FROM repos WHERE name = ?`, name).Scan(&cur)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return cur.String, err
}

func (p *Pool) SetRepoCursor(name, at string) error {
	_, err := p.db.Exec(`UPDATE repos SET last_scan_at = ? WHERE name = ?`, at, name)
	return err
}

func (p *Pool) RecordRun(rs *runStats) error {
	_, err := p.db.Exec(`INSERT INTO runs
		(started_at, source, scanned, new_items, updated, eligible, quarantine, rejected, api_calls, duration_ms)
		VALUES (?,?,?,?,?,?,?,?,?,?)`,
		rs.StartedAt, rs.Source, rs.Scanned, rs.New, rs.Updated,
		rs.Eligible, rs.Quarantine, rs.Rejected, rs.APICalls, rs.DurationMs)
	return err
}

// ItemRow mirrors the items table for display.
type ItemRow struct {
	DedupeKey, Repo, Title, URL, Labels, Author string
	CreatedAt, UpdatedAt, Assignee, ClaimSignal string
	GateStatus, GateReason, FitHits, Source     string
	FirstSeenAt, VerifiedAt, Disposition, Notes string
	Number, Comments, Reactions, LinkedPRCount  int
}

const itemCols = `dedupe_key, repo, number, title, url, labels, author, created_at, updated_at,
	comments, reactions, assignee, linked_pr_count, claim_signal,
	gate_status, gate_reason, fit_hits, source, first_seen_at, verified_at, disposition, notes`

func scanItemRow(s interface{ Scan(...any) error }) (ItemRow, error) {
	var r ItemRow
	err := s.Scan(&r.DedupeKey, &r.Repo, &r.Number, &r.Title, &r.URL, &r.Labels, &r.Author,
		&r.CreatedAt, &r.UpdatedAt, &r.Comments, &r.Reactions, &r.Assignee, &r.LinkedPRCount,
		&r.ClaimSignal, &r.GateStatus, &r.GateReason, &r.FitHits, &r.Source,
		&r.FirstSeenAt, &r.VerifiedAt, &r.Disposition, &r.Notes)
	return r, err
}

func (p *Pool) Get(key string) (ItemRow, error) {
	return scanItemRow(p.db.QueryRow(`SELECT `+itemCols+` FROM items WHERE dedupe_key = ?`, key))
}

// Review returns eligible and quarantined items not dropped by the operator,
// newest issue activity first.
func (p *Pool) Review(limit int) ([]ItemRow, error) {
	rows, err := p.db.Query(`SELECT `+itemCols+` FROM items
		WHERE gate_status IN ('ELIGIBLE','QUARANTINE') AND disposition <> 'DROPPED'
		ORDER BY updated_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ItemRow
	for rows.Next() {
		r, err := scanItemRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (p *Pool) SetDisposition(key, disp string) error {
	res, err := p.db.Exec(`UPDATE items SET disposition = ? WHERE dedupe_key = ?`, disp, key)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (p *Pool) countBy(col string) (map[string]int, int, error) {
	rows, err := p.db.Query(`SELECT ` + col + `, COUNT(*) FROM items GROUP BY ` + col)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out, total := map[string]int{}, 0
	for rows.Next() {
		var k string
		var n int
		if err := rows.Scan(&k, &n); err != nil {
			return nil, 0, err
		}
		out[k] = n
		total += n
	}
	return out, total, rows.Err()
}

type repoCount struct {
	Repo string
	N    int
}

func (p *Pool) eligibleByRepo() ([]repoCount, error) {
	rows, err := p.db.Query(`SELECT repo, COUNT(*) FROM items
		WHERE gate_status = 'ELIGIBLE' AND disposition <> 'DROPPED'
		GROUP BY repo ORDER BY COUNT(*) DESC, repo`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []repoCount
	for rows.Next() {
		var rc repoCount
		if err := rows.Scan(&rc.Repo, &rc.N); err != nil {
			return nil, err
		}
		out = append(out, rc)
	}
	return out, rows.Err()
}

type repoRow struct {
	Name       string
	Weight     float64
	Archived   int
	LastScanAt string
}

func (p *Pool) repoRows() ([]repoRow, error) {
	rows, err := p.db.Query(`SELECT name, weight, archived, COALESCE(last_scan_at,'') FROM repos ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []repoRow
	for rows.Next() {
		var r repoRow
		if err := rows.Scan(&r.Name, &r.Weight, &r.Archived, &r.LastScanAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (p *Pool) staleCount(cutoff string) (int, error) {
	var n int
	err := p.db.QueryRow(`SELECT COUNT(*) FROM items
		WHERE gate_status IN ('ELIGIBLE','QUARANTINE') AND verified_at < ?`, cutoff).Scan(&n)
	return n, err
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
