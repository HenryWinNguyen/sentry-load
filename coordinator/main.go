// Command coordinator publishes one load-test job to Redis Streams and
// watches sentry:results for that job's live metrics until it's done.
// No HTTP API yet, no persisted history, no multi-job support — that's
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

	const durationSeconds = 10
	jobID := uuid.NewString()
	job := map[string]interface{}{
		"id":               jobID,
		"url":              "http://localhost:8081/fast",
		"vus":              strconv.Itoa(10),
		"duration_seconds": strconv.Itoa(durationSeconds),
		"ramp_pattern":     "steady",
	}

	entryID, err := rdb.XAdd(pingCtx, &redis.XAddArgs{
		Stream: jobsStream,
		Values: job,
	}).Result()
	if err != nil {
		log.Fatalf("failed to enqueue job: %v", err)
	}
	log.Printf("enqueued job %s (stream entry %s) to %q", jobID, entryID, jobsStream)

	watchResults(rdb, jobID, durationSeconds)
}

// watchResults reads sentry:results from "now" and prints every snapshot
// belonging to jobID until it sees one marked done, or a safety timeout
// (job duration + buffer) elapses.
func watchResults(rdb *redis.Client, jobID string, durationSeconds int) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(durationSeconds+15)*time.Second)
	defer cancel()

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
				log.Printf("timed out waiting for results for job %s", jobID)
				return
			}
			log.Printf("results read error: %v", err)
			continue
		}

		for _, stream := range streams {
			for _, msg := range stream.Messages {
				lastID = msg.ID
				if msg.Values["job_id"] != jobID {
					continue
				}
				done := msg.Values["done"] == "true"
				log.Printf("[%ss] requests=%v errors=%v rps=%v p50=%vms p95=%vms p99=%vms done=%v",
					msToSeconds(msg.Values["elapsed_ms"]),
					msg.Values["requests"], msg.Values["errors"], msg.Values["rps"],
					msg.Values["p50_ms"], msg.Values["p95_ms"], msg.Values["p99_ms"], done)
				if done {
					return
				}
			}
		}
	}
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
