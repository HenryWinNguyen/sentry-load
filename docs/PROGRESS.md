# Progress Log

Reverse-chronological log of major milestones and additions, so it's easy to
see where the build stands without scrolling back through chat history. See
[SCOPE.md](../SCOPE.md) for the full milestone plan (M1-M16) and
[CLAUDE.md](../CLAUDE.md) for architecture/conventions.

## Current status

**Pre-M1.** Scoping and repo scaffolding done. Not yet started: coordinator/
worker skeleton.

## Log

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
- Next: create GitHub repo (public), then start M1 — coordinator + worker
  skeleton talking over local Redis
