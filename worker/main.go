// Command worker consumes load-test jobs from Redis Streams, generates
// real HTTP load against each job's target, and streams live metrics
// snapshots back to the coordinator.
package main

import (
	"context"
	"errors"
	"log"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	jobsStream    = "sentry:jobs"
	resultsStream = "sentry:results"
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
				handleJob(ctx, rdb, msg.Values)
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

// handleJob parses, validates, and runs a load test for one job, streaming
// a live metrics snapshot back to the coordinator roughly once per second
// over resultsStream, plus one final snapshot with done=true.
func handleJob(ctx context.Context, rdb *redis.Client, values map[string]interface{}) {
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

	publish := func(m Metrics) {
		fields := map[string]interface{}{
			"job_id":      m.JobID,
			"elapsed_ms":  strconv.FormatInt(m.Elapsed.Milliseconds(), 10),
			"requests":    strconv.Itoa(m.Requests),
			"errors":      strconv.Itoa(m.Errors),
			"rps":         strconv.FormatFloat(m.RPS, 'f', 1, 64),
			"p50_ms":      strconv.FormatInt(m.P50.Milliseconds(), 10),
			"p95_ms":      strconv.FormatInt(m.P95.Milliseconds(), 10),
			"p99_ms":      strconv.FormatInt(m.P99.Milliseconds(), 10),
			"done":        strconv.FormatBool(m.Done),
		}
		if err := rdb.XAdd(ctx, &redis.XAddArgs{Stream: resultsStream, Values: fields}).Err(); err != nil {
			log.Printf("failed to publish metrics for job %s: %v", m.JobID, err)
		}
	}

	final := runLoadTest(ctx, job, publish)
	log.Printf("job %s done: %d requests", job.ID, len(final))
}
