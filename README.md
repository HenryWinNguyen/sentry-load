# Sentry Load

**Free, genuinely distributed load testing for side projects.** Verify you
own a target, configure a test, watch RPS/latency/error-rate stream in
live, get a shareable report when it's done.

**[Try it live →](https://sentry-load.vercel.app)**

## Why this exists

Students and indie hackers shipping to free tiers (Vercel, Render,
Railway, Fly.io) have no way to find out if their app survives real
concurrent traffic — without paying for a SaaS tool or hand-rolling a k6
script. Sentry Load is built specifically for that gap, not as a smaller
clone of the enterprise tools:

| | **Sentry Load** | k6 (OSS) | JMeter | Loader.io (free) |
|---|---|---|---|---|
| Distributed across real machines | ✅ free | ❌ paywalled (Grafana Cloud) | ✅, but manual RMI/firewall setup per node | ❌ |
| Setup | verify target, pick a preset, go | write a JS test script | configure master/slave nodes by hand | account + config |
| Concurrent load (free tier) | 200 VUs/test | single machine | self-hosted, your own limits | capped at 10 clients |
| Live dashboard | ✅ WebSocket | terminal output | GUI (heavy) | ✅ |
| Shareable results | ✅ public link + embeddable badge | ❌ | ❌ | limited |
| Cost | $0, no card | $0 → paid for distributed | $0 (self-hosted, self-managed) | $0 → paid |

Workers talk to the coordinator only through a Redis queue — no worker
ever needs another node's address, so there's nothing to hand-configure
as the fleet grows across providers/regions (the actual pain JMeter's
master/slave model has).

## How it works

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

1. **Verify you own the target** (DNS TXT record or a `/.well-known/`
   file) — non-negotiable. This is the line between a load tester and a
   DDoS-as-a-service tool.
2. **Configure a test** — VU count, duration, ramp pattern, or a preset
   tuned to indie-launch scale (*Quick Check*, *Launch Day*, *Class
   Demo*).
3. **The coordinator checks fleet capacity before accepting the job** —
   refuses or clamps-with-a-warning instead of silently under-delivering
   if you ask for more workers than are actually online.
4. **Workers fan out across real, independent machines** (currently an
   always-on Oracle Cloud VM + a GCP instance in a different region) and
   stream metrics back over Redis Streams. If a worker dies mid-test,
   another one picks its job back up once its lease lapses.
5. **Latency percentiles are test-wide and accurate** — each worker ships
   a mergeable latency histogram, so the p95 you see covers every request
   across every machine (within 1%), not an average of per-worker p95s.
6. **Results persist and produce a shareable report link** — the "load
   tested by Sentry Load" badge doubles as a growth loop: drop it in a
   README, or wire the included [GitHub Action](.github/actions/load-test)
   to auto-load-test every PR preview deployment and comment the results.

## What's actually deployed right now

- **Coordinator**: always-on Go HTTP API on an Oracle Cloud free-tier VM —
  GitHub OAuth login, domain verification, job submission/status,
  Prometheus metrics
- **Workers**: one on the same Oracle VM, one on a GCP e2-micro in a
  different region — genuinely distributed, not simulated (verified live,
  not just capable of it in code)
- **Dashboard**: Next.js on Vercel, live WebSocket-updated results,
  shareable public reports
- **Monitoring**: Prometheus + Grafana Cloud (free tier) for fleet health,
  separate from the per-test live dashboard
- **Guardrails**: hard caps on VUs/duration/RPS, per-user cooldown, a
  circuit breaker that aborts a test early if the target's error rate
  spikes rather than grinding through a dead target for the full duration

## Local development

```bash
docker compose up -d          # local Redis
(cd guineapig && go run .)    # a load-test target with a deliberate bottleneck to find
go run ./worker                # start a worker
go run ./coordinator            # HTTP API on :8080
(cd dashboard && npm run dev)   # dashboard on :3000
```

Env vars: `REDIS_ADDR`, `REDIS_PASSWORD`, `POSTGRES_URL` (history,
users, sessions, verified domains; everything but history still works
in-memory without it), `GITHUB_CLIENT_ID`/`GITHUB_CLIENT_SECRET`/
`GITHUB_REDIRECT_URL`, `DASHBOARD_URL`, `ALLOWLISTED_HOSTS`.

Integration tests against real Redis/Postgres run in CI and are skipped
locally unless you point them at a scratch instance:

```bash
TEST_REDIS_ADDR=localhost:6379 \
TEST_POSTGRES_URL='postgres://sentryload:sentryload@localhost:5432/sentryload?sslmode=disable' \
go test ./coordinator/... ./worker/...
```

## Project background

Built incrementally, milestone by milestone — one PR per milestone in
this repo's history. Scope, architecture decisions, and what's
deliberately *not* in scope: [`SCOPE.md`](SCOPE.md).

## License

[MIT](LICENSE)
