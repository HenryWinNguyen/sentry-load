// Command coordinator publishes a load-test job to Redis Streams, fanned out
// across WORKER_COUNT sub-jobs sharing one test ID, and watches
// sentry:results for live per-worker snapshots until every sub-job reports
// done. No HTTP API yet, no persisted history, no multi-test support — that's
// V2 (see SCOPE.md).
package main

import (
	"context"
	"errors"
	"log"
	"os"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

const (
	jobsStream    = "sentry:jobs"
	resultsStream = "sentry:results"
)

func main() {
	addr := os.Getenv("REDIS_ADDR")
	if addr == "" {
		addr = "localhost:6379"
	}

	rdb := redis.NewClient(&redis.Options{Addr: addr})
	pingCtx, cancelPing := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelPing()
	if err := rdb.Ping(pingCtx).Err(); err != nil {
		log.Fatalf("could not reach redis at %s: %v", addr, err)
	}

	targetURL := os.Getenv("TARGET_URL")
	if targetURL == "" {
		targetURL = "http://localhost:8081/fast"
	}

	fanout := 1
	if v := os.Getenv("WORKER_COUNT"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			log.Fatalf("invalid WORKER_COUNT %q: must be a positive integer", v)
		}
		fanout = n
	}

	const (
		totalVUs        = 20
		durationSeconds = 10
	)

	testID := uuid.NewString()
	jobIDs := make([]string, fanout)

	// Split totalVUs as evenly as possible across the fanout; any remainder
	// (totalVUs not evenly divisible) goes to the first few sub-jobs.
	base, remainder := totalVUs/fanout, totalVUs%fanout
	for i := 0; i < fanout; i++ {
		vus := base
		if i < remainder {
			vus++
		}
		jobID := uuid.NewString()
		jobIDs[i] = jobID
		job := map[string]interface{}{
			"id":               jobID,
			"test_id":          testID,
			"url":              targetURL,
			"vus":              strconv.Itoa(vus),
			"duration_seconds": strconv.Itoa(durationSeconds),
			"ramp_pattern":     "steady",
		}
		entryID, err := rdb.XAdd(pingCtx, &redis.XAddArgs{Stream: jobsStream, Values: job}).Result()
		if err != nil {
			log.Fatalf("failed to enqueue sub-job %d/%d: %v", i+1, fanout, err)
		}
		log.Printf("enqueued sub-job %d/%d: %s (%d vus, stream entry %s)", i+1, fanout, jobID, vus, entryID)
	}
	log.Printf("test %s fanned out across %d worker(s), %d total vus", testID, fanout, totalVUs)

	watchResults(rdb, testID, jobIDs, durationSeconds)
}

// subJobState is the latest known snapshot for one sub-job (one worker's
// share of the test), plus its raw percentile strings for display — merging
// percentiles across workers isn't statistically valid without the
// underlying samples, so each worker's own p50/p95/p99 is shown as-is
// rather than averaged into a fake combined number.
type subJobState struct {
	requests      int
	errors        int
	rps           float64
	p50, p95, p99 string
	done          bool
}

// watchResults reads sentry:results from "now", updates per-sub-job state
// as snapshots for testID arrive, and prints a live per-worker line plus a
// merged summary once every sub-job has reported done=true.
func watchResults(rdb *redis.Client, testID string, jobIDs []string, durationSeconds int) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(durationSeconds+15)*time.Second)
	defer cancel()

	states := make(map[string]*subJobState, len(jobIDs))
	for _, id := range jobIDs {
		states[id] = &subJobState{}
	}

	allDone := func() bool {
		for _, s := range states {
			if !s.done {
				return false
			}
		}
		return true
	}

	lastID := "$" // only entries added after this call starts
	for {
		streams, err := rdb.XRead(ctx, &redis.XReadArgs{
			Streams: []string{resultsStream, lastID},
			Block:   2 * time.Second,
		}).Result()
		if err != nil {
			if errors.Is(err, redis.Nil) {
				continue
			}
			if errors.Is(err, context.DeadlineExceeded) {
				log.Printf("timed out waiting for results for test %s", testID)
				return
			}
			log.Printf("results read error: %v", err)
			continue
		}

		for _, stream := range streams {
			for _, msg := range stream.Messages {
				lastID = msg.ID
				if msg.Values["test_id"] != testID {
					continue // some other test's traffic on the shared stream
				}
				jobID, _ := msg.Values["job_id"].(string)
				s, tracked := states[jobID]
				if !tracked {
					continue
				}

				s.requests = intField(msg.Values["requests"])
				s.errors = intField(msg.Values["errors"])
				s.rps = floatField(msg.Values["rps"])
				s.p50, s.p95, s.p99 = strField(msg.Values["p50_ms"]), strField(msg.Values["p95_ms"]), strField(msg.Values["p99_ms"])
				s.done = msg.Values["done"] == "true"

				log.Printf("[%ss] worker %s: requests=%d errors=%d rps=%.1f p50=%sms p95=%sms p99=%sms done=%v",
					msToSeconds(msg.Values["elapsed_ms"]), shortID(jobID), s.requests, s.errors, s.rps, s.p50, s.p95, s.p99, s.done)

				if allDone() {
					printSummary(testID, states)
					return
				}
			}
		}
	}
}

func printSummary(testID string, states map[string]*subJobState) {
	var totalRequests, totalErrors int
	var totalRPS float64
	for _, s := range states {
		totalRequests += s.requests
		totalErrors += s.errors
		totalRPS += s.rps
	}
	log.Printf("test %s done: %d worker(s), %d total requests, %d total errors, %.1f combined rps",
		testID, len(states), totalRequests, totalErrors, totalRPS)
}

func msToSeconds(v interface{}) string {
	s, ok := v.(string)
	if !ok {
		return "?"
	}
	ms, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return "?"
	}
	return strconv.FormatFloat(ms/1000, 'f', 1, 64)
}

func intField(v interface{}) int {
	s, ok := v.(string)
	if !ok {
		return 0
	}
	n, _ := strconv.Atoi(s)
	return n
}

func floatField(v interface{}) float64 {
	s, ok := v.(string)
	if !ok {
		return 0
	}
	f, _ := strconv.ParseFloat(s, 64)
	return f
}

func strField(v interface{}) string {
	s, ok := v.(string)
	if !ok {
		return "?"
	}
	return s
}

func shortID(id string) string {
	if len(id) <= 8 {
		return id
	}
	return id[:8]
}
