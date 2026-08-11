package main

import (
	"log"
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Fleet-level metrics (M13) — how this worker itself is doing over time
// (jobs handled, load generated, errors), distinct from the per-test
// Metrics in metrics.go that get streamed back to the coordinator for the
// live dashboard.
var (
	jobsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "sentry_worker_jobs_total",
		Help: "Load-test jobs this worker has finished, by outcome.",
	}, []string{"circuit_broken"})

	jobsInProgress = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "sentry_worker_jobs_in_progress",
		Help: "Load-test jobs this worker is currently running.",
	})

	requestsGeneratedTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "sentry_worker_requests_generated_total",
		Help: "Total HTTP requests this worker has sent against test targets.",
	})

	requestErrorsTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "sentry_worker_request_errors_total",
		Help: "Total request errors (connection failures or 5xx) this worker has recorded.",
	})
)

// serveMetrics runs a small HTTP server exposing /metrics for Prometheus to
// scrape. Separate from the coordinator's API server entirely — the worker
// has no other HTTP surface, so this is its own listener rather than
// something bolted onto an existing mux.
func serveMetrics(addr string) {
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())
	log.Printf("metrics listening on %s", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Printf("metrics server error: %v", err)
	}
}
