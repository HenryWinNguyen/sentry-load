package main

import (
	"sort"
	"time"
)

// Metrics is a point-in-time snapshot of a running (or finished) load test.
// Sent to the coordinator repeatedly while the test runs, and once more
// with Done=true at the end.
type Metrics struct {
	JobID    string
	Elapsed  time.Duration
	Requests int
	Errors   int
	RPS      float64
	P50      time.Duration
	P95      time.Duration
	P99      time.Duration
	Done     bool
}

// computeMetrics summarizes results collected so far. A request counts as
// an error if it failed outright (connection error, timeout) or the target
// returned a 5xx — both indicate the target, not the load generator, is in
// trouble.
func computeMetrics(jobID string, results []RequestResult, elapsed time.Duration, done bool) Metrics {
	m := Metrics{JobID: jobID, Elapsed: elapsed, Done: done}
	n := len(results)
	if n == 0 {
		return m
	}

	latencies := make([]time.Duration, n)
	for i, r := range results {
		latencies[i] = r.Latency
		if r.Err != nil || r.StatusCode >= 500 {
			m.Errors++
		}
	}
	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })

	percentile := func(p float64) time.Duration {
		idx := int(p * float64(n-1))
		return latencies[idx]
	}

	m.Requests = n
	if elapsed > 0 {
		m.RPS = float64(n) / elapsed.Seconds()
	}
	m.P50 = percentile(0.50)
	m.P95 = percentile(0.95)
	m.P99 = percentile(0.99)
	return m
}
