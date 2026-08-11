# Monitoring (M13)

Prometheus + Grafana for fleet-level health — request rates, error rates,
worker capacity, load-generation throughput — over time, distinct from the
per-test live dashboard (M10) which is about one test's own results.

## What's here

- `prometheus.yml` — scrape config template. Deployed on the VM at
  `~/prometheus-install/prometheus.yml`. Scrapes the coordinator's
  `GET /metrics` (`:8080`) and the worker's `GET /metrics` (`:9091`,
  `METRICS_ADDR` env var to change it), both localhost-only on the VM — no
  firewall/NSG changes needed for scraping itself.
- `grafana-dashboard.json` — a ready-to-import Grafana dashboard (9 panels:
  live workers, jobs in progress, test outcomes, request/error rates,
  p95 latency, load-gen throughput, goroutines). Uses a `${DS_PROMETHEUS}`
  datasource variable, so Grafana prompts you to pick the datasource on
  import instead of needing a hardcoded UID.

## What's already deployed (as of 2026-08-09)

- Prometheus `v3.13.2` installed and running on the VM
  (`~/prometheus-install/`, started via `nohup`, same convention as
  coordinator/worker — no systemd unit), bound to `127.0.0.1:9090` only
  (not exposed externally — nothing outside the VM needs to reach it
  directly once remote_write is wired up).
- Confirmed scraping both targets successfully
  (`curl http://localhost:9090/api/v1/targets` → both `up`).
- 6h local retention — local storage is just a short buffer, Grafana Cloud
  is meant to be the actual long-term store once wired up below.

## Status: done (2026-08-10)

Grafana Cloud free tier is wired up and confirmed working:
- `remote_write` configured on the VM's Prometheus (credentials live only
  on the VM, in `~/prometheus-install/prometheus.yml` — never committed;
  `ops/prometheus.yml` in git stays a placeholder template)
- `grafana-dashboard.json` imported into Grafana Cloud as "Sentry Load —
  fleet health", confirmed pulling live data
- `prometheus_remote_storage_samples_failed_total` = 0 at time of setup —
  clean push, no auth/format issues

If the API token ever needs rotating: Grafana Cloud portal → Connections
→ your Prometheus connection → manage access policies, generate a new
token, update the `password` field in the VM's `prometheus.yml`, restart
Prometheus the same way described in the log below.
