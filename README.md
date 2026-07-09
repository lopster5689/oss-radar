# oss-radar

Scans GitHub issues into a local SQLite pool, gates them deterministically
(ELIGIBLE / QUARANTINE / REJECTED), and gives one operator a queue to shortlist
contribution candidates from. Instance #2 of the Restock Pipeline Model; the
full spec is in [SPEC.md](SPEC.md).

The machine discovers and gates. The human decides. No automation ever files a
PR or comments on an issue.

## Commands

```
radar [-config config.yaml] [-db radar.db] <command>

  scan [--source watchlist|search|all]      discover, gate and ingest issues
  serve [--addr 127.0.0.1:7333] [--tick 0]  web dashboard (localhost cockpit)
  status                                    pool totals, per-repo eligible, staleness
  review [--limit N]                        eligible + quarantine items, newest first
  shortlist <owner/repo#N>                  set disposition SHORTLISTED
  drop <owner/repo#N>                       set disposition DROPPED
  show <owner/repo#N>                       full stored record for one item
```

Auth: set `GITHUB_TOKEN`, or have `gh auth login` done; the client falls back
to `gh auth token`.

## Dashboard

`radar serve` binds 127.0.0.1:7333 and serves four pages: overview (funnel
tiles, eligible by repo, staleness, recent runs, a Scan now button), review
(the NEW eligible/quarantine queue with shortlist/drop actions and filters),
shortlist (the working set for the weekly sitting, with per-item notes), and
dropped (paginated, reversible).

The server writes only operator-owned fields (disposition, notes), and every
change appends an activities row, so the timeline stays append-only truth.
Scans triggered from the UI run the exact same path as `radar scan`, including
run logging to the runs table, runs.jsonl and ANALYSIS_LOG.md. Non-loopback
binds require an explicit `--addr` and carry no auth, so do not do that.

`radar serve --tick 6h` in a terminal makes the dashboard self-refreshing: a
full scan runs every 6 hours while the server is up, no cron needed.

## Config

`config.yaml` holds the watchlist (repos with strategic weights), the label
searches, and the gate: positive labels, disqualifying labels, skill keywords,
claim regexes, and quarantine thresholds. The gate is pure and unit-tested;
tune it in config, not code.

## Files

- `radar.db`: the SQLite pool (items, repos, activities, runs)
- `runs.jsonl`: machine-readable run log
- `ANALYSIS_LOG.md`: append-only lab log, curated findings on top
