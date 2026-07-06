// Command coordinator is the M1 walking-skeleton coordinator: it publishes
// one fake job to Redis Streams so a worker can prove it can consume it.
// No HTTP API, no real load-test config, no result aggregation yet — that's
// M2/M3+ (see SCOPE.md).
package main

import (
	"context"
	"log"
	"os"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

const jobsStream = "sentry:jobs"

func main() {
	addr := os.Getenv("REDIS_ADDR")
	if addr == "" {
		addr = "localhost:6379"
	}

	rdb := redis.NewClient(&redis.Options{Addr: addr})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := rdb.Ping(ctx).Err(); err != nil {
		log.Fatalf("could not reach redis at %s: %v", addr, err)
	}

	job := map[string]interface{}{
		"id":               uuid.NewString(),
		"url":              "http://localhost:8081/fast",
		"vus":              strconv.Itoa(10),
		"duration_seconds": strconv.Itoa(30),
		"ramp_pattern":     "steady",
	}

	id, err := rdb.XAdd(ctx, &redis.XAddArgs{
		Stream: jobsStream,
		Values: job,
	}).Result()
	if err != nil {
		log.Fatalf("failed to enqueue job: %v", err)
	}

	log.Printf("enqueued job %s (stream entry %s) to %q", job["id"], id, jobsStream)
}
