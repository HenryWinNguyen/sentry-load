// Command worker is the M1 walking-skeleton worker: it consumes jobs from
// Redis Streams via a consumer group and logs them. No load generation yet
// (see SCOPE.md M2) — this only proves the queue plumbing.
package main

import (
	"context"
	"errors"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	jobsStream    = "sentry:jobs"
	consumerGroup = "workers"
)

func main() {
	addr := os.Getenv("REDIS_ADDR")
	if addr == "" {
		addr = "localhost:6379"
	}
	consumerName := os.Getenv("WORKER_ID")
	if consumerName == "" {
		consumerName = "worker-1"
	}

	rdb := redis.NewClient(&redis.Options{Addr: addr})

	pingCtx, cancelPing := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelPing()
	if err := rdb.Ping(pingCtx).Err(); err != nil {
		log.Fatalf("could not reach redis at %s: %v", addr, err)
	}

	// Create the consumer group, starting from the beginning of the stream.
	// MKSTREAM creates the stream if it doesn't exist yet. BUSYGROUP means
	// the group already exists, which is fine on restart.
	err := rdb.XGroupCreateMkStream(pingCtx, jobsStream, consumerGroup, "0").Err()
	if err != nil && !errors.Is(err, redis.Nil) && !isBusyGroup(err) {
		log.Fatalf("failed to create consumer group: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	log.Printf("%s listening on stream %q (group %q)", consumerName, jobsStream, consumerGroup)

	for {
		select {
		case <-ctx.Done():
			log.Printf("%s shutting down", consumerName)
			return
		default:
		}

		streams, err := rdb.XReadGroup(ctx, &redis.XReadGroupArgs{
			Group:    consumerGroup,
			Consumer: consumerName,
			Streams:  []string{jobsStream, ">"},
			Count:    1,
			Block:    2 * time.Second,
		}).Result()
		if err != nil {
			if errors.Is(err, redis.Nil) || errors.Is(err, context.Canceled) {
				continue
			}
			log.Printf("read error: %v", err)
			continue
		}

		for _, stream := range streams {
			for _, msg := range stream.Messages {
				handleJob(ctx, msg.Values)
				if err := rdb.XAck(ctx, jobsStream, consumerGroup, msg.ID).Err(); err != nil {
					log.Printf("failed to ack job %s: %v", msg.ID, err)
				}
			}
		}
	}
}

func isBusyGroup(err error) bool {
	return err != nil && len(err.Error()) >= 9 && err.Error()[:9] == "BUSYGROUP"
}

// handleJob parses, validates, and runs a load test for one job. It only
// prints a basic totals summary — turning raw results into RPS/percentiles
// and streaming them back to the coordinator is M3, not this milestone.
func handleJob(ctx context.Context, values map[string]interface{}) {
	job, err := parseJob(values)
	if err != nil {
		log.Printf("rejecting malformed job: %v", err)
		return
	}
	if err := job.validate(); err != nil {
		log.Printf("rejecting job %s: %v", job.ID, err)
		return
	}

	log.Printf("running job %s: %s vus=%d duration=%ds pattern=%s",
		job.ID, job.URL, job.VUs, job.DurationSeconds, job.RampPattern)

	results := runLoadTest(ctx, job)

	var errCount int
	for _, r := range results {
		if r.Err != nil {
			errCount++
		}
	}
	log.Printf("job %s done: %d requests, %d errors", job.ID, len(results), errCount)
}
