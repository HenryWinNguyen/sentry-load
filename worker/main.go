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

	// workerRegistryPrefix + consumer name is a self-expiring Redis key a
	// worker refreshes on heartbeatInterval — the coordinator counts these
	// (see coordinator/capacity.go) to know how many workers are actually
	// alive before accepting a test that needs more than that (M12). A
	// worker that crashes or loses connectivity just stops refreshing its
	// key and ages out within heartbeatTTL, no explicit deregistration
	// needed.
	workerRegistryPrefix = "sentry:worker:"
	heartbeatInterval    = 5 * time.Second
	heartbeatTTL         = 15 * time.Second
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

	go heartbeat(ctx, rdb, consumerName)

	metricsAddr := os.Getenv("METRICS_ADDR")
	if metricsAddr == "" {
		metricsAddr = ":9091"
	}
	go serveMetrics(metricsAddr)

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
			// NOGROUP means the stream/group got wiped out from under us
			// (e.g. a manual Redis flush) — recreate it rather than
			// spinning forever on the same error. Any other error also
			// gets the sleep below so a persistent Redis outage doesn't
			// turn into a tight retry loop burning CPU.
			if isNoGroup(err) {
				if cerr := rdb.XGroupCreateMkStream(ctx, jobsStream, consumerGroup, "0").Err(); cerr != nil && !isBusyGroup(cerr) {
					log.Printf("failed to recreate consumer group: %v", cerr)
				}
			}
			time.Sleep(2 * time.Second)
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

// heartbeat refreshes this worker's presence key until ctx is cancelled.
// Runs once immediately so the coordinator sees this worker as available
// right away, not after the first tick.
func heartbeat(ctx context.Context, rdb *redis.Client, consumerName string) {
	key := workerRegistryPrefix + consumerName
	send := func() {
		if err := rdb.Set(ctx, key, time.Now().UTC().Format(time.RFC3339), heartbeatTTL).Err(); err != nil {
			log.Printf("failed to send heartbeat: %v", err)
		}
	}

	send()
	ticker := time.NewTicker(heartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			send()
		}
	}
}

func isBusyGroup(err error) bool {
	return err != nil && len(err.Error()) >= 9 && err.Error()[:9] == "BUSYGROUP"
}

func isNoGroup(err error) bool {
	return err != nil && len(err.Error()) >= 7 && err.Error()[:7] == "NOGROUP"
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

	jobsInProgress.Inc()
	defer jobsInProgress.Dec()

	// m.Requests/m.Errors are cumulative since the job started, not
	// per-tick — track what's already been counted so the Prometheus
	// counters only advance by each tick's delta instead of double-adding
	// the running total on every publish call.
	var countedRequests, countedErrors int
	publish := func(m Metrics) {
		if delta := m.Requests - countedRequests; delta > 0 {
			requestsGeneratedTotal.Add(float64(delta))
			countedRequests = m.Requests
		}
		if delta := m.Errors - countedErrors; delta > 0 {
			requestErrorsTotal.Add(float64(delta))
			countedErrors = m.Errors
		}
		if m.Done {
			jobsTotal.WithLabelValues(strconv.FormatBool(m.CircuitBroken)).Inc()
		}

		fields := map[string]interface{}{
			"job_id":         m.JobID,
			"test_id":        job.TestID,
			"elapsed_ms":     strconv.FormatInt(m.Elapsed.Milliseconds(), 10),
			"requests":       strconv.Itoa(m.Requests),
			"errors":         strconv.Itoa(m.Errors),
			"rps":            strconv.FormatFloat(m.RPS, 'f', 1, 64),
			"p50_ms":         strconv.FormatInt(m.P50.Milliseconds(), 10),
			"p95_ms":         strconv.FormatInt(m.P95.Milliseconds(), 10),
			"p99_ms":         strconv.FormatInt(m.P99.Milliseconds(), 10),
			"done":           strconv.FormatBool(m.Done),
			"circuit_broken": strconv.FormatBool(m.CircuitBroken),
		}
		if err := rdb.XAdd(ctx, &redis.XAddArgs{Stream: resultsStream, Values: fields}).Err(); err != nil {
			log.Printf("failed to publish metrics for job %s: %v", m.JobID, err)
		}
	}

	final := runLoadTest(ctx, job, publish)
	log.Printf("job %s done: %d requests", job.ID, len(final))
}
