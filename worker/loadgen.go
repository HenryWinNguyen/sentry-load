package main

import (
	"context"
	"net/http"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// RequestResult is one HTTP request's outcome. M2 only records these in
// memory — turning them into RPS/percentiles/error-rate is M3's job.
type RequestResult struct {
	Timestamp  time.Time
	Latency    time.Duration
	StatusCode int
	Err        error
}

// runLoadTest generates load against job.URL per job.VUs/DurationSeconds/
// RampPattern and returns every request's raw result. If onUpdate is
// non-nil, it's called roughly once per second with a live metrics
// snapshot, and once more at the end with Done=true — that's how the
// coordinator gets a running view of the test instead of just a final
// number.
//
// Each VU is a goroutine in a closed-loop (send, wait for response, send
// again) — the standard virtual-user model, not a fixed-RPS open model.
// A shared rate limiter caps aggregate RPS at maxSafeRPS regardless of VU
// count, as a safety backstop independent of the per-job hard caps in
// job.go.
func runLoadTest(ctx context.Context, job Job, onUpdate func(Metrics)) []RequestResult {
	ctx, cancel := context.WithTimeout(ctx, time.Duration(job.DurationSeconds)*time.Second)
	defer cancel()
	// A second cancel layered on top of the duration timeout, so the
	// circuit breaker below can stop every VU early without waiting out
	// the full configured duration. Everything downstream already reads
	// from `ctx`, so this shadow is the only change needed to make VU
	// loops and the ramp-pattern startup delay both breaker-aware for free.
	ctx, cancelBreaker := context.WithCancel(ctx)
	defer cancelBreaker()

	transport := &http.Transport{
		MaxIdleConnsPerHost: job.VUs + 10,
		MaxConnsPerHost:     job.VUs + 10,
	}
	client := &http.Client{Transport: transport, Timeout: 10 * time.Second}
	limiter := rate.NewLimiter(rate.Limit(maxSafeRPS), job.VUs)

	var (
		mu      sync.Mutex
		results = make([]RequestResult, 0, 1024)
	)
	record := func(r RequestResult) {
		mu.Lock()
		results = append(results, r)
		mu.Unlock()
	}
	snapshot := func() []RequestResult {
		mu.Lock()
		defer mu.Unlock()
		cp := make([]RequestResult, len(results))
		copy(cp, results)
		return cp
	}

	start := time.Now()
	tickerDone := make(chan struct{})
	var (
		abortedMu sync.Mutex
		aborted   bool
	)
	if onUpdate != nil {
		go func() {
			ticker := time.NewTicker(time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-tickerDone:
					return
				case <-ticker.C:
					m := computeMetrics(job.ID, snapshot(), time.Since(start), false)
					if tripsCircuitBreaker(m) {
						abortedMu.Lock()
						aborted = true
						abortedMu.Unlock()
						m.CircuitBroken = true
						onUpdate(m)
						cancelBreaker() // stop every VU now instead of waiting out the duration
						return
					}
					onUpdate(m)
				}
			}
		}()
	}

	vuLoop := func() {
		for {
			if err := limiter.Wait(ctx); err != nil {
				return // context deadline reached or cancelled
			}
			start := time.Now()
			resp, err := client.Get(job.URL)
			result := RequestResult{Timestamp: start, Latency: time.Since(start), Err: err}
			if err == nil {
				result.StatusCode = resp.StatusCode
				resp.Body.Close()
			}
			record(result)
		}
	}

	var wg sync.WaitGroup
	switch job.RampPattern {
	case "steady":
		wg.Add(job.VUs)
		for i := 0; i < job.VUs; i++ {
			go func() {
				defer wg.Done()
				vuLoop()
			}()
		}
	case "ramp":
		// Linearly ramp from 0 to job.VUs active VUs over the full
		// duration: VU i joins at i * (duration / VUs).
		step := time.Duration(job.DurationSeconds) * time.Second / time.Duration(job.VUs)
		wg.Add(job.VUs)
		for i := 0; i < job.VUs; i++ {
			offset := time.Duration(i) * step
			go func() {
				defer wg.Done()
				select {
				case <-ctx.Done():
					return
				case <-time.After(offset):
				}
				vuLoop()
			}()
		}
	}

	wg.Wait()
	close(tickerDone)
	final := snapshot()
	if onUpdate != nil {
		finalMetrics := computeMetrics(job.ID, final, time.Since(start), true)
		abortedMu.Lock()
		finalMetrics.CircuitBroken = aborted
		abortedMu.Unlock()
		onUpdate(finalMetrics)
	}
	return final
}
