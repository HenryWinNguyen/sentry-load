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
// RampPattern and returns every request's raw result.
//
// Each VU is a goroutine in a closed-loop (send, wait for response, send
// again) — the standard virtual-user model, not a fixed-RPS open model.
// A shared rate limiter caps aggregate RPS at maxSafeRPS regardless of VU
// count, as a safety backstop independent of the per-job hard caps in
// job.go.
func runLoadTest(ctx context.Context, job Job) []RequestResult {
	ctx, cancel := context.WithTimeout(ctx, time.Duration(job.DurationSeconds)*time.Second)
	defer cancel()

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
	return results
}
