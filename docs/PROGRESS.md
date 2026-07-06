# Progress Log

Reverse-chronological log of major milestones and additions, so it's easy to
see where the build stands without scrolling back through chat history. See
[SCOPE.md](../SCOPE.md) for the full milestone plan (M1-M16) and
[CLAUDE.md](../CLAUDE.md) for architecture/conventions.

## Current status

**M1-M4 done, blocked on M5.** Everything that doesn't require an external
account is complete and verified: coordinator, worker (real load
generation + live metrics), guinea-pig app with a genuine fast/slow
contrast. M5 (deploy to real infra) needs Henry to create an Oracle Cloud
account first — see [M5_CHECKLIST.md](./M5_CHECKLIST.md) for exact steps.
Nothing left to do autonomously until that's handed off.

## Log

### 2026-07-05 — M4 complete: guinea-pig target app (with a bug caught and fixed)
- `/guineapig`: new Go module, SQLite-backed, `/fast` (single JOIN query,
  20-connection pool) vs `/slow` (intentional N+1 — one query per product
  for its reviews — against a 2-connection pool)
- **First version had no real bottleneck at all.** Ran the actual load
  generator against both endpoints (not just eyeballed the code) — /slow
  came back at identical p50/p95/p99 (~1-3ms) to /fast, and the same
  ~500 RPS. Root cause: local SQLite against a 20-row table is fast enough
  that 21 sequential queries still cost a fraction of a millisecond total,
  so the tiny pool never actually contended for anything
- Fixed by adding `simulatedQueryLatency` (4ms), applied per query in both
  handlers equally — standing in for the network round trip a real,
  remote database would add, which is what actually makes N+1 painful in
  production, not local query execution time
- Re-verified live: same test again, now /fast = 4020 requests/8s (~502
  RPS, still capped by the safety limiter) vs /slow = 1633 requests/8s
  (~202 RPS), p50=98ms/p95=102ms/p99=110ms. A real, dramatic, honestly-
  earned contrast this time
- Next: M5 — deploy coordinator + worker to real infra. Needs Henry to
  create a cloud account first (see checklist when this comes up)

### 2026-07-05 — M3 complete: metrics aggregation and live reporting
- `worker/metrics.go`: `computeMetrics` turns raw per-request results into
  RPS/p50/p95/p99/error-count. A request counts as an error on connection
  failure/timeout OR a 5xx response — the target being unhealthy, not just
  the client failing
- `worker/loadgen.go`: `runLoadTest` now takes an `onUpdate` callback,
  fired roughly once per second with a live snapshot plus once more at the
  end with `done=true`
- `worker/main.go`: publishes each snapshot to a new `sentry:results`
  Redis stream, keyed by job ID
- `coordinator/main.go`: after enqueuing, watches `sentry:results` from
  "now" (`$`), prints every snapshot for its own job ID, stops on
  `done=true` or a safety timeout (duration + 15s) so it can never hang
  forever if something goes wrong
- Verified live end-to-end: 10 VUs / 10s steady job produced 11 real
  snapshots (10 ticks + final), correctly capped at the ~500 RPS safety
  limiter, percentiles computed each tick, coordinator's final tally
  (5010 requests) matched the worker's own log exactly
- Next: M4 — guinea-pig target app (fast endpoint + intentionally
  bottlenecked endpoint), replacing the throwaway `python -m http.server`
  used for M1-M3 verification

### 2026-07-05 — M2 complete: real HTTP load generation
- `worker/job.go`: parses the job's wire fields (id/url/vus/
  duration_seconds/ramp_pattern) and enforces hard caps (200 VUs / 300s
  duration max) — rejects out-of-range jobs outright rather than silently
  clamping them, per the "capacity-aware, honest about limits" positioning
  in SCOPE.md
- `worker/loadgen.go`: goroutine-per-VU closed-loop load generator, tuned
  `http.Transport` (raised `MaxIdleConnsPerHost`/`MaxConnsPerHost` so the
  client itself doesn't bottleneck), shared `golang.org/x/time/rate`
  limiter capping aggregate RPS as a safety backstop independent of the
  per-job caps. Supports `steady` (all VUs from t=0) and `ramp` (linear
  0→VUs over the full duration) patterns
- Verified all three paths manually: steady (5 VUs/3s → 1504 requests, 0
  errors), ramp (6 VUs/3s → 1505 requests, 0 errors), and hard-cap
  rejection (99999 VUs correctly rejected with a clear log message,
  worker kept running)
- Worker only prints request/error totals for now — turning raw results
  into RPS/percentiles and streaming them back to the coordinator is M3,
  not this milestone
- Next: M3 — metrics aggregation and reporting back to the coordinator

### 2026-07-05 — M1 complete: coordinator/worker skeleton over local Redis
- Installed Go 1.26.4 (Homebrew) — wasn't present on the machine
- `docker-compose.yml`: local Redis 7 on :6379
- `/coordinator`: Go module, connects to Redis, XADDs one fake job
  (id/url/vus/duration_seconds/ramp_pattern) to stream `sentry:jobs`, exits
- `/worker`: Go module, creates a consumer group on `sentry:jobs`, blocks on
  `XREADGROUP`, logs each job received, XACKs it, runs until SIGINT/SIGTERM
- `go.work` at repo root wires up both modules for local tooling (gopls,
  etc.) without merging them — still independently deployable
- Verified end-to-end: started worker in background, ran coordinator, worker
  logged the exact job the coordinator enqueued — the core plumbing works
- Set up a scoped Bash permission allowlist (`.claude/settings.local.json`,
  gitignored) for routine commands (go/docker/git/gh/mkdir) so autonomous
  runs don't stall on approval prompts for expected, repeated commands
- Next: M2 — worker actually generates HTTP load (VU count/duration/ramp)
  against a target instead of just logging the job

### 2026-07-05 — Pre-M1: Project scaffolding
- Defined purpose, audience, and positioning vs. bigger load testers (k6
  Cloud, BlazeMeter, Loader.io) — SCOPE.md
- Locked V1 decisions: guinea-pig app design (fast endpoint + intentionally
  bottlenecked endpoint), hand-written Go load generator (goroutine pool +
  tuned `http.Transport` + rate limiter, not a third-party library)
- Corrected an earlier hosting assumption: Oracle Cloud Always Free Ampere
  A1 was cut in half and is single-region only (as of June 2026); Fly.io
  killed its free tier for new accounts. Revised plan: one Oracle Always
  Free VM as always-on coordinator, ephemeral workers (GitHub Actions
  and/or GCP e2-micro) provisioned per test instead of an always-on
  multi-region fleet
- Wrote CLAUDE.md: architecture overview, user experience flow, constraints
  and policies, repo etiquette, commands reference
- Initialized git repo, first commit (SCOPE.md, CLAUDE.md, .gitignore)
- Installed Claude Code plugins tied to immediate/next work (not
  speculative): `gopls-lsp` (Go code intelligence), `github` (official
  GitHub MCP server), `redis-development` (Redis Streams best practices).
  Verified against the actual official marketplace catalog rather than
  guessed names. Deferred `typescript-lsp`, `vercel`, `terraform`,
  `grafana-mcp` until their respective milestones (V2 dashboard, deployment,
  Final observability)
- Installed `feature-dev` (official Anthropic plugin: `code-architect`,
  `code-explorer`, `code-reviewer` agents + `/feature-dev` command) — maps
  onto the milestone-driven build process. Evaluated and skipped
  `compounding-engineering` (third-party marketplace, mostly irrelevant
  bundled content for a Go project) and deferred `frontend-design`
  (official, but no UI work until V2/M10)
- `gh auth login` complete (HTTPS, account HenryWinNguyen)
- Created GitHub repo (public): https://github.com/HenryWinNguyen/sentry-load
  — initial commits pushed
- Next: M1 — coordinator + worker skeleton talking over local Redis
