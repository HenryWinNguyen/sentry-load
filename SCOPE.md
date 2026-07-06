# Sentry Load — Distributed Load Testing Platform

## Purpose

Load-test your own side project before it goes live. Real audience: students/indie
hackers deploying on free tiers (Vercel, Render, Railway, Fly.io) who have no idea
if their app survives real concurrent traffic, and no way to find out without
paying for a SaaS tool or hand-rolling a script.

Flow: verify domain ownership once → configure a test → get a live dashboard
(RPS, latency percentiles, error rate) → get a shareable report link when it's
done. The shareable link doubles as the growth loop — "load tested by Sentry
Load" is a badge people can post when they launch something.

**Non-negotiable safety line:** never allow testing a target without proven
ownership. This is what separates a load-testing tool from a DDoS-as-a-service
tool.

## Why this project (personal context)

Resume is strong on full-stack (Next.js/TS/Postgres) and enterprise Java/Spring,
but has no distributed systems, queuing, orchestration, or observability tooling
— and a Cybersecurity minor with nothing on the resume that uses it. This project
targets exactly those gaps: Go, a real job queue, multi-region worker
orchestration, and abuse/verification design.

## Architecture

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
  Worker A   Worker B   Worker C     ← Go binaries, different regions/clouds
      │         │         │
      └─────────┼─────────┘
               ▼
        Metrics aggregator → Postgres
               │
               ▼
   Live dashboard (WebSocket push) + shareable report link
```

Coordinator and workers communicate through Redis Streams in both directions
(jobs out, metrics results in) — avoids workers needing to know the
coordinator's network address, works cleanly across NAT'd free-tier VMs in
different regions.

## Decisions locked so far

- **Repo:** single monorepo — `/coordinator`, `/worker`, `/dashboard`.
- **V1 test targets:** guinea-pig apps deployed specifically to be load-tested,
  not real side projects — zero risk while the engine is unproven.
- **V1 dashboard:** none — terminal/log output only. All effort goes into the
  distributed engine first.
- **V1 test config:** configurable from day one — VU count, duration, ramp
  pattern (not a single hardcoded shape).
- **V2 auth:** GitHub OAuth — fits the target audience (devs), no password
  storage/reset flows to build.
- **Guinea-pig app design:** two endpoints — one fast/normal, one with a
  deliberate flaw (N+1 query or tiny connection pool) — so load-test results
  show a real breaking point instead of a flat line, and give a genuine
  "found and fixed the bottleneck" story.
- **Worker load-generation approach:** hand-written in Go — goroutine worker
  pool + tuned `http.Transport` (`MaxIdleConnsPerHost`/`MaxConnsPerHost`) +
  token-bucket rate limiter (`golang.org/x/time/rate`), not an existing
  library like vegeta. Bounded scope, real learning value; the whole point
  of the project is Go concurrency + distributed-systems plumbing.

## Positioning vs. bigger load testers (k6 Cloud, BlazeMeter, Loader.io)

Not competing on scale — leaning into being small, free, and right-sized for
the actual audience (indie devs on free-tier hosts):

- **Presets tuned to indie-launch scale**, not raw enterprise configurability:
  *Quick Check* (60s, ~50 VUs), *Launch Day* (5 min ramp to a few hundred
  VUs), *Class Demo* (steady moderate load).
- **Capacity-aware admission control**: coordinator checks current worker
  fleet capacity and refuses/warns instead of silently under-delivering when
  a requested test exceeds it — honest about limits instead of pretending to
  be enterprise-scale.
- **No signup friction**: verify domain, go — no card, no sales call.
- **Ephemeral workers** (see hosting note below) instead of an idling 24/7
  fleet — forced by free-tier reality, but a legitimate distributed-systems
  pattern in its own right.

## Hosting reality check (verified July 2026 — supersedes earlier assumption)

Original plan assumed multiple free Oracle Cloud VMs across regions plus a
Fly.io coordinator. Both assumptions are stale:

- Oracle Always Free Ampere A1 was **cut in half** (2 OCPU/12GB total, down
  from 4/24) as of June 15, 2026, and Always Free compute has only ever been
  **one home region per account** — not multi-region on its own.
- Fly.io **killed its ongoing free tier** for new accounts; new orgs get a
  2-hour trial, then it's paid.

Revised plan: one Oracle Always Free VM as the **always-on coordinator**
(low resource need — just accepting jobs and aggregating results). Workers
are **ephemeral** — provisioned only for the duration of a test, then torn
down — using GitHub Actions runners (free for public repos) and/or a Google
Cloud e2-micro Always Free instance in a different region as a second
source. Exact combo gets pressure-tested hands-on at V1-M5, not locked in
now, since free-tier terms keep shifting.

## V1 — walking skeleton

Goal: prove the coordinator → queue → worker → target → metrics loop works
end to end, deployed on real (free-tier) infrastructure, not just localhost.

- Coordinator API (Go): create test (VU count, duration, ramp pattern), get
  status/results
- Redis Streams queue between coordinator and worker
- One worker: generates HTTP load against a target per the configured shape,
  reports RPS / latency percentiles (p50/p95/p99) / error rate back
- Targets: fixed allowlist of guinea-pig apps deployed specifically for testing
- No auth, single-user
- Output: terminal/log stream of live metrics + final summary
- Deployed: coordinator + 1 worker on free-tier infra (Oracle Cloud
  Always-Free or Fly.io)

## V2 — distributed and safe for other people

- Multiple workers across 2-3 regions — the actual distributed load
  generation
- Domain-ownership verification (DNS TXT record or `/.well-known/` challenge
  file) gating any target not on the V1 allowlist
- GitHub OAuth
- Hard caps: max duration / max RPS / max concurrent VUs per test, per-user
  cooldowns
- Kill-switch / circuit breaker if target error rate spikes mid-test
- Shareable public report link
- Test history persisted in Postgres
- Live dashboard (Next.js) — pulled forward from "final" now that V1 proved
  the engine works headless

## Final / polish (pick based on time remaining)

- Prometheus + Grafana on the worker fleet (operational observability, not
  user-facing)
- Multiple test shapes: spike, soak/endurance, ramp — selectable in UI
- "Load tested by Sentry Load" embeddable badge (growth loop)
- GitHub Action to auto load-test a PR preview deployment
- Kubernetes for worker orchestration/autoscaling

## Explicitly out of scope, permanently

- Testing unverified arbitrary URLs — the non-negotiable DDoS-vector line
- Massive scale (tens of thousands of VUs) — not needed for this audience,
  not free-tier feasible
- Billing / payments
- Teams / org accounts, multi-tenancy beyond per-user
- Custom test-scripting DSL (like k6's JS API)
- Non-HTTP protocol load testing (gRPC, raw TCP, MQTT)

## Draft resume bullet (target for after V2)

> Designed and deployed a distributed load-testing platform (Go, Redis
> Streams, Postgres) with domain-ownership verification and multi-region
> worker orchestration across free-tier cloud providers, enabling real users
> to safely load-test their own deployments with live streaming metrics.

## Milestones

### V1 — walking skeleton
- **M1** — Coordinator and worker run locally, connected through local Redis;
  coordinator enqueues a fake job, worker dequeues and logs it. (Proves the
  plumbing, no load generation yet.)
- **M2** — Worker generates HTTP load (VU count/duration/ramp) against a
  local target, records raw latency/status per request in memory.
- **M3** — Worker computes RPS/p50/p95/p99/error-rate and streams live
  results back to the coordinator over Redis; coordinator prints them live
  to terminal.
- **M4** — Build the guinea-pig target app (fast endpoint + intentionally
  bottlenecked endpoint).
- **M5** — Deploy coordinator + worker to real infra (Oracle Always Free VM
  + one ephemeral worker source) and run the first real *remote* test end to
  end. **This is "V1 done."**

### V2 — distributed and safe for other people
- **M6** — Second worker in a different provider/region; coordinator fans
  one test across both and merges results.
- **M7** — Domain-ownership verification (DNS TXT / well-known file) gating
  non-allowlisted targets.
- **M8** — GitHub OAuth; tests scoped to a user.
- **M9** — Abuse guardrails: hard caps on VUs/duration/RPS, per-user
  cooldown, circuit breaker to abort if target error rate spikes.
- **M10** — Live Next.js dashboard (WebSocket) replacing terminal output;
  test history in Postgres.
- **M11** — Shareable public report link.

### Final / polish
- **M12** — Capacity-aware admission control.
- **M13** — Prometheus + Grafana on the worker fleet.
- **M14** — Preset test shapes (Quick Check / Launch Day / Class Demo).
- **M15** — Shareable badge + GitHub Action integration.
- **M16 (stretch)** — Kubernetes-based worker orchestration/autoscaling.

## Open questions for next session

- Exact free-tier provider(s) for worker hosting (GitHub Actions vs. GCP
  e2-micro vs. both) — decide hands-on at M5/M6.
- Repo visibility (public vs. private) — affects whether GitHub Actions
  minutes are free (public repos get unlimited free minutes).
