package main

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Fleet-level metrics, scraped by Prometheus and shipped to Grafana Cloud
// (M13) — a different lens than the per-test live dashboard (M10): this is
// about the coordinator/worker fleet's own health over time (request
// rates, error rates, worker availability), not any single test's results.
var (
	httpRequestsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "sentry_coordinator_http_requests_total",
		Help: "Total HTTP requests handled by the coordinator, by route/method/status.",
	}, []string{"route", "method", "status"})

	httpRequestDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "sentry_coordinator_http_request_duration_seconds",
		Help:    "HTTP request duration in seconds, by route.",
		Buckets: prometheus.DefBuckets,
	}, []string{"route"})

	testsCreatedTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "sentry_coordinator_tests_created_total",
		Help: "Test submissions, by outcome.",
	}, []string{"result"}) // "accepted", "clamped", "no_capacity", "rejected"

	liveWorkersGauge = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "sentry_coordinator_live_workers",
		Help: "Number of workers with a current heartbeat, as last observed by the coordinator.",
	})
)

// metricsMiddleware records request count and latency for every route. Uses
// r.Pattern (the exact registered mux pattern, e.g. "GET /tests/{id}") as
// the label instead of r.URL.Path, keeping cardinality bounded regardless
// of how many distinct test/token IDs get requested.
func metricsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)

		route := r.Pattern
		if route == "" {
			route = "unmatched"
		}
		httpRequestsTotal.WithLabelValues(route, r.Method, strconv.Itoa(rec.status)).Inc()
		httpRequestDuration.WithLabelValues(route).Observe(time.Since(start).Seconds())
	})
}

// refreshLiveWorkersGauge keeps sentry_coordinator_live_workers current
// even when nobody's submitting tests — handleCreateTest updates it too,
// but a fleet with no traffic for a while shouldn't show a stale reading.
func refreshLiveWorkersGauge(ctx context.Context, workers workerCounter) {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		n, err := workers.CountLiveWorkers(ctx)
		if err != nil {
			log.Printf("failed to refresh live worker count: %v", err)
		} else {
			liveWorkersGauge.Set(float64(n))
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

// Hijack delegates to the wrapped ResponseWriter's own Hijacker, if it has
// one. Required for GET /tests/{id}/live (live.go) — gorilla/websocket
// upgrades a connection by hijacking it, and without this method
// statusRecorder would silently hide that capability from net/http's
// interface check, breaking every WebSocket connection through this
// middleware with "response does not implement http.Hijacker".
func (r *statusRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hj, ok := r.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, fmt.Errorf("underlying ResponseWriter does not support hijacking")
	}
	return hj.Hijack()
}
