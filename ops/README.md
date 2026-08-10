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

## What's left — needs Henry (new external account)

1. Sign up for Grafana Cloud's free tier at grafana.com.
2. In the Grafana Cloud portal: Connections → Add new connection →
   Prometheus. Copy the **remote_write endpoint URL**, **instance
   ID/username**, and generate an **API key**.
3. On the VM, edit `~/prometheus-install/prometheus.yml`, uncomment the
   `remote_write` block, fill in those three values, then restart
   Prometheus (`pkill -x prometheus`, then the same `nohup ./prometheus
   --config.file=prometheus.yml --web.listen-address=127.0.0.1:9090
   --storage.tsdb.path=./data --storage.tsdb.retention.time=6h` command
   from `~/prometheus-install/`).
4. In Grafana Cloud's UI, import `grafana-dashboard.json` (Dashboards →
   New → Import → upload/paste JSON), pick the auto-provisioned Prometheus
   datasource when prompted.
5. Confirm data is flowing: run a test from the live dashboard, watch the
   panels update in Grafana Cloud within ~30s.

Once that's done, M13 is complete — merge `m13-prometheus-grafana` to
`main`.
