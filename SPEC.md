# oss-radar: the credibility machine

Instance #2 of the Restock Pipeline Model (`~/claude_kitchen/restock-pipeline-model.md`).
Stock = open source contribution opportunities. Action = a pull request (or a cheaper
triage touch). Purpose = build durable credibility in the Go systems / observability /
DNS space, which is the public proof-of-work behind Roshan's career framing and
HydraDNS.

Strategy note that shapes everything below: **credibility compounds within a project,
not across projects.** Five merged PRs in one respected repo make you a known
contributor there; five PRs scattered across five repos make you a stranger everywhere.
So this machine deliberately biases depth over breadth, which inverts one LeadHub
assumption (there, any lead is as good as another; here, the same repo getting warmer
is the whole point).

---

## The spec sheet (13 blanks, filled)

Per the model: kill on paper if 4, 7, 8, 11, or 12 are vague. None are. Verdict: build.

**1. Corpus + acquisition ban-risk.** GitHub Issues via the authenticated REST/GraphQL
API: (a) a curated watchlist of repos, (b) label searches (`help wanted`,
`good first issue`) across the Go ecosystem. Ban-risk: low. The API is the sanctioned
access path; 5,000 req/hr core budget. The acquiring account IS the acting account
(`lopster568`), so per the model, acquisition spends the action scope's budget: stay
far under limits, incremental fetches only (`since` cursors), no scraping ever.

**2. Item + dedupeKey + cross-corpus identity.** Item = one issue. dedupeKey =
`owner/repo#number` (natural, exact). Cross-corpus: the same root cause filed in two
repos or an issue superseded by another; handled by recording cross-references during
enrichment and flagging, never auto-merging.

**3. Enrichment layers** (cheapest first, provenance recorded):
L1 issue metadata (labels, assignee, age, comment count, reactions) arrives free with
discovery. L2 linked PRs and cross-references (timeline API). L3 recent comments,
scanned for claim signals. L4 repo health: outside-contributor PR merge rate, median
time-to-first-review, maintainer responsiveness (computed per repo, cached with a TTL,
this doubles as the source viability scan the model requires). L5 (later, AI-quarantined)
difficulty and fit scoring.

**4. Match-gate criterion.** Two tiers, exactly as the model prescribes for
qualification-shaped domains:
- **Deterministic hard disqualifiers** (reject): issue closed; assignee set; an open
  linked PR exists; a claim in recent comments (regexes: "I'll take", "working on
  this", "/assign", "can I take", "assigned to me"); disqualifying labels (wontfix,
  duplicate, invalid, question, tracking, epic, meta, needs-design); repo archived.
  This tier is the existing manual freshness-scan rule from the global playbook,
  encoded as structure.
- **Quarantine (med) triggers** (review queue, not rejected, not eligible): ambiguous
  claim language; issue older than 180 days; heavy discussion (>15 comments, likely
  contested or design-bound); positive label but no skill-keyword match.
- **Eligible (high)**: open, unassigned, unclaimed, un-PR'd, and either a positive
  label (help wanted, good first issue, kind/bug) or a skill-keyword hit (dns, grpc,
  otel, tracing, storage, tls, udp, cache, goroutine, race, memory leak...) in
  title/labels.
The semantic worth-it judgment (is this issue credibility-per-hour positive?) is
HUMAN, made in the review queue. AI may later pre-score into the queue, never past it.

**5. Scrub rules + fact TTLs.** Scrub: locked issues, bot-authored issues, repos with
near-zero outside merge rate (measured, not assumed). Fact TTLs: THE defining
constraint of this domain, "unclaimed" is a fact with a shelf life of days. `verified_at`
on every item; anything older than 7 days is stale; **mandatory pre-flight**: re-run
the full gate against the live API minutes before any work starts. Never start a PR on
stale data.

**6. Action + channels.** Human-composed, always (HITL drain variant: the machine
selects, ranks, pre-flights, claims, and records; Roshan writes the code and the PR).
Two channels with very different costs:
- **PR** (hours): the real credibility unit.
- **Triage touch** (minutes): reproduce a bug and confirm it, bisect, add a failing
  test case, answer a question. Cheap trust-builders that warm a repo before the first
  PR and keep presence between PRs.

**7. Failure signal + reputation budget + SCOPE.** Budget = maintainer goodwill,
scoped **per repo**. Failure signals: PR closed unmerged, explicit maintainer pushback,
PR ignored past 3x the repo's median review latency. Breaker: 2 closed-unmerged in one
repo = that repo pauses for reassessment. Throttle: feedback is weeks-latency, so the
binding control is the **in-flight cap**, max 1 open PR per repo, max 3 open PRs
total (which is also the operator WIP limit, blank 13).

**8. Feedback events + correlation token + promotion.** Token = the PR number (and
branch), linked to the item id at claim time. Events: review comments, approvals,
requested changes, merge, close, mentions. Merged = WON. Maintainer engagement
(review, comment) = WARM, and a WARM PR outranks all new work: **responding to review
within hours is the single highest-conversion action in this domain.** Repeat merges
in the same repo = the compounding credibility metric.

**9. Yield + conversion metrics.** Intake yield: eligible-per-100-scanned, per source.
Conversion: merged-per-submitted, per repo. The strategic metric above both: merges
per repo over time (depth), plus credibility events (asked to review, mentioned,
invited).

**10. Cadence.** On a stalled own-PR: one polite ping after the repo's median review
latency has clearly passed, max 2 pings, then park. Never ping a maintainer about a
new issue claim. Cooldown between claims in the same repo if a PR is already open
there (the in-flight cap enforces this structurally).

**11. Cost per action + capacity + ranking.** PR = 2-8 hours of Roshan's time, and
his time is the true budget (HydraDNS is the primary project; this machine is a
sidecar at ~5-8 h/week). Ranking = **credibility-per-hour**:
`(repo weight x fit x mergeability) / estimated effort`, where repo weight is a
config-declared strategic value (CoreDNS and Jaeger high: DNS credibility feeds
HydraDNS directly, Jaeger already has merged history), fit is skill-keyword density,
and mergeability comes from the measured repo health (L4). Exploration fraction per
the model's success-to-the-successful fix: reserve some review-queue slots for repos
with no history yet.

**12. Time-to-act sensitivity.** High: good issues get claimed in days. Two structural
answers: freshness-ranked scan (newest eligible first) and the domain's cheap
**reserve step**, commenting "I'd like to take this" / requesting assignment BEFORE
investing hours. The claim comment is itself a rationed action (spends goodwill if
abandoned), so claims count toward the per-repo budget and an abandoned claim is a
recorded failure.

**13. Operator capacity.** WIP limit: 3 open PRs total, 1 per repo. Review-queue SLA:
one sitting per week (~30 min, pick next week's targets). Weekly hours cap: ~8. If
WARM (review responses due) items exist, they consume the budget before any new claim.

---

## Sources (viability, model step 2)

Watchlist v1 (config, not code; strategic weights in parentheses):
- `jaegertracing/jaeger` (high: existing merged history, observability)
- `open-telemetry/opentelemetry-collector` + `opentelemetry-collector-contrib` (high: observability center of gravity)
- `open-telemetry/opentelemetry-go` (med)
- `coredns/coredns` (high: DNS in Go, direct HydraDNS synergy)
- `miekg/dns` (med: the Go DNS library HydraDNS builds on)
- `unikraft/kraftkit` (med: existing history)
- `prometheus/prometheus` (med)
Search source: `label:"help wanted" language:Go state:open` and
`label:"good first issue" language:Go state:open` filtered to stars>500, recency <30d.
Repo health (L4) is measured by the tool itself, so viability is continuously
re-verified rather than assumed once. Dead-end log lives in ANALYSIS_LOG.md, same as
leadgen.

## Architecture mapping (model component -> this build)

- [1] Sources: `watchlist` + `search` adapters, incremental via `since`, journaled.
- [2] Enrich: L1-L4 above; gate = pure deterministic functions with unit tests.
- [3] Ingest: idempotent upsert by dedupeKey into SQLite; machine never touches
  operator fields (disposition, notes, claim state).
- [4] Pool: SQLite, axes stored separately: gate_status (ELIGIBLE/QUARANTINE/REJECTED+reason),
  disposition (NEW/SHORTLISTED/CLAIMED/IN_FLIGHT/WON/LOST/PARKED/DROPPED),
  verified_at, repo health cache, append-only activities table.
- [5] Campaigns: implicit v1 (one standing program); cadence rules in config.
- [6] Drain: HITL. `radar next` proposes ranked candidates; `radar claim` runs the
  live pre-flight, then records the claim; `radar submitted <pr-url>` records the act.
  In-flight caps enforced here. No automation ever files a PR.
- [7] Feedback: `radar sync` polls own-PR states + events into the timeline; WON/LOST
  labeling; WARM alerts.
- [8] Cockpit: CLI first (operator is the engineer himself), `radar status` +
  `radar review` + a generated HTML digest later if wanted.
- [9] Ops: cron daily scan, runs.jsonl, ANALYSIS_LOG.md, SQLite backup copy.
- [10] AI quarantine: no AI anywhere in v1. Later: fit/difficulty pre-scoring writes
  advisory columns only.

## Build order (model steps 3-8)

- **Phase A (now):** pool schema + watchlist/search adapters + deterministic gate +
  ingest + `scan/status/review` CLI. Prove: re-scan = 0 dupes; gate precision
  hand-checked on a real sample; runs logged.
- **Phase B:** repo-health enrichment (L4), ranking, `next/claim/submitted` with live
  pre-flight and caps.
- **Phase C:** `sync` feedback loop, WON/LOST, WARM alerts, weekly digest.
- **Phase D (only after first merged PR sourced through the machine):** exploration
  fraction, AI pre-scoring, HTML digest. Per the model: prove the loop before
  multiplying.

Language: Go (module `ossradar`), stdlib + `mattn/go-sqlite3` or `modernc.org/sqlite`,
`gh` token via env/gh CLI. The tool itself is portfolio-grade by design and a
candidate for open-sourcing once it has produced merged PRs (the machine proving
itself is the README).
