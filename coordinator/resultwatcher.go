package main

import (
	"context"
	"errors"
	"log"
	"time"

	"github.com/redis/go-redis/v9"
)

const resultsStream = "sentry:results"

// watchResults runs for the lifetime of the server (until ctx is
// cancelled), continuously reading sentry:results and routing each
// snapshot into store by test_id/job_id. Unlike V1's one-shot
// per-CLI-invocation watcher, this handles however many tests are in
// flight concurrently, since the coordinator is now a long-running service
// rather than a run-once binary.
func watchResults(ctx context.Context, rdb *redis.Client, store *TestStore) {
	lastID := "$" // only entries added after the watcher starts
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		streams, err := rdb.XRead(ctx, &redis.XReadArgs{
			Streams: []string{resultsStream, lastID},
			Block:   2 * time.Second,
		}).Result()
		if err != nil {
			if errors.Is(err, redis.Nil) || errors.Is(err, context.Canceled) {
				continue
			}
			log.Printf("results read error: %v", err)
			continue
		}

		for _, stream := range streams {
			for _, msg := range stream.Messages {
				lastID = msg.ID
				store.Update(
					strField(msg.Values["test_id"]),
					strField(msg.Values["job_id"]),
					intField(msg.Values["requests"]),
					intField(msg.Values["errors"]),
					floatField(msg.Values["rps"]),
					strField(msg.Values["p50_ms"]),
					strField(msg.Values["p95_ms"]),
					strField(msg.Values["p99_ms"]),
					msg.Values["done"] == "true",
				)
			}
		}
	}
}
