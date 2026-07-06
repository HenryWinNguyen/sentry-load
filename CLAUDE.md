# CLAUDE.md — Sentry Load

Working reference for building in this repo. For *why* this project exists,
its positioning, and milestone-level scope, see [SCOPE.md](./SCOPE.md) — don't
duplicate that content here, link to it.

As components get built out, each gets its own README for implementation
detail (e.g. `/coordinator/README.md` for the queue consumer-group setup,
`/worker/README.md` for the load-generation engine internals). This file
stays the high-level entry point that links out to those.

## Project overview

Distributed load-testing platform: users verify they own a domain, configure
a test (VU count, duration, ramp pattern), and workers in multiple
regions/providers generate HTTP load against the target while streaming live
metrics (RPS, latency percentiles, error rate) back to a coordinator. Full
purpose/audience/positioning: [SCOPE.md](./SCOPE.md).

## User experience (target end state, built incrementally — see milestones in SCOPE.md)

1. User verifies domain ownership (DNS TXT record or `/.well-known/`
   challenge file) — or, in V1, target is a fixed allowlist Henry controls.
2. User configures a test: VU count, duration, ramp pattern (or a preset —
   Quick Check / Launch Day / Class Demo).
3. Coordinator checks current worker fleet capacity before accepting the
   job (capacity-aware admission control — refuses/warns rather than
   silently under-delivering).
4. Test runs; live metrics stream to the user (terminal in V1, WebSocket
   dashboard from V2).
5. On completion, results persist and produce a shareable public report
   link.

## Architecture overview

```
        ┌─────────────┐
Users → │  Control API │ (Go) — auth, domain verification, job config, results API
        └──────┬──────┘
               │ enqueues job
        ┌──────▼──────┐
        │  Job Queue   │ (Redis Streams)
        └──────┬──────┘
      ┌─────────┼─────────┐
      ▼         ▼         ▼
  Worker A   Worker B   Worker C     ← Go binaries, ephemeral, different regions/providers
      │         │         │
      └─────────┼─────────┘
               ▼
        Metrics aggregator → Postgres (from V2; V1 prints to terminal)
               │
               ▼
   Live dashboard (WebSocket, from V2) + shareable report link
```

- **Coordinator** (`/coordinator`): Go service, always-on. Owns job
  creation/status API, admission control, result aggregation.
- **Worker** (`/worker`): Go binary, ephemeral — provisioned per test, not
  always-on. Consumes jobs from Redis Streams, generates load via a
  hand-rolled goroutine pool + tuned `http.Transport` + token-bucket rate
  limiter (not a third-party load-gen library — this is deliberate, see
  SCOPE.md decisions log), reports metrics back over Redis Streams.
- **Dashboard** (`/dashboard`, from V2): Next.js, live view over WebSocket +
  historical/shareable reports.
- **Queue**: Redis Streams, both directions (jobs out, results in) —
  avoids workers needing to know the coordinator's network address.
- **Guinea-pig app** (`/guineapig`): the M1-M4 local load-test target, not
  part of the deployed product. `/fast` (one query, 20-conn pool) vs
  `/slow` (intentional N+1 + 2-conn pool) — see SCOPE.md decisions log for
  why. Uses `simulatedQueryLatency` (4ms per query) to stand in for a real
  DB's network round trip; without it, local SQLite is fast enough that
  the N+1 pattern produced no measurable difference at all — verified,
  not assumed, then fixed.

## Constraints and policies

- **Never test an unverified target.** Domain-ownership verification is
  non-negotiable — this is the line between a load tester and a
  DDoS-as-a-service tool. No exceptions, no "just for testing" bypass flags.
- **Hard caps always on**, even in V1's single-allowlist mode: max
  VUs/duration/RPS per test. Build the cap-checking path early so it's not
  bolted on later.
- **Free-tier only, deliberately.** No paid infra. Workers are ephemeral
  (provisioned per test, torn down after) rather than always-on, both for
  cost and to get genuine multi-region distribution — see SCOPE.md hosting
  note for current provider specifics (these change; re-verify before
  relying on any specific free-tier limit).
- **Out of scope, permanently**: testing unverified URLs, massive scale
  (tens of thousands of VUs), billing/payments, teams/org accounts, a custom
  test-scripting DSL, non-HTTP protocol testing. Full list in SCOPE.md.

## Working autonomy

Proceed without asking for: writing/editing code within the current
milestone, local builds/tests, commits to that milestone's branch, updating
`docs/PROGRESS.md`, adding a package clearly needed for the current
milestone. Opening and merging the PR to `main` once local verification
passes is also autonomous — no need to wait for review.

Stop and check in for: the end of every milestone (M1, M2, ...) — summarize
what shipped before starting the next one; anything touching money or a new
external account (cloud signups, DNS records — these need Henry's identity/
payment info so they're his steps regardless); any deviation from what's
written in SCOPE.md/CLAUDE.md (if reality forces a scope or architecture
change mid-build, pause and explain rather than quietly changing course);
anything destructive or hard to reverse (force-push, history reset,
deleting branches).

## Repo etiquette

- **Branching**: one branch per milestone (e.g. `m1-coordinator-worker-skeleton`,
  `m2-load-generator`), merged to `main` when that milestone's local
  verification passes. Solo project, so no PR review required, but still
  open a PR against `main` and merge it (rather than pushing straight to
  `main`) — keeps a real history of what shipped per milestone for the
  portfolio/resume story.
- **Commits**: small, one logical change each. Conventional-commit-style
  prefixes (`feat:`, `fix:`, `refactor:`, `docs:`) — not required but keeps
  `git log` readable as the project grows.
- **Folder structure**: `/coordinator`, `/worker`, `/dashboard` (from V2),
  each an independent Go module (or Next.js app for dashboard) — monorepo,
  not a shared module, so each can be deployed/versioned independently.
- **Don't commit**: `.env` (real secrets), anything under `node_modules/`,
  Go build output, `.DS_Store`. See `.gitignore`.
- **Docs stay current**: when a milestone changes the architecture or a
  constraint, update this file or SCOPE.md in the same PR — don't let them
  drift from what's actually built.
- **Progress log**: [docs/PROGRESS.md](./docs/PROGRESS.md) tracks major
  milestones/additions as they land, reverse-chronological. Check it first
  to see current status; append an entry (in the same commit) whenever a
  milestone completes or a major addition/decision happens.

## Frequently used commands

```
# Local dev loop
docker compose up -d          # local Redis (localhost:6379)
(cd guineapig && go run .)    # load-test target: /fast (single query) vs /slow (N+1 + tiny pool), :8081
go run ./worker                # start a worker (blocks, listens on sentry:jobs)
go run ./coordinator           # enqueues one fake job, then exits (M1 behavior)
(cd <module> && go build ./...)  # build one module (run go.work doesn't support root ./... yet)
go test ./...                  # run tests, from repo root or per-module

# Repo
gh repo create sentry-load --public --source=. --push   # one-time, done
gh pr create                                             # open a PR for the current branch
gh pr merge --squash                                     # merge once verified
```
