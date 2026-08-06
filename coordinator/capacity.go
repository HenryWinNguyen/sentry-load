package main

import (
	"context"
	"fmt"

	"github.com/redis/go-redis/v9"
)

// workerRegistryPrefix matches worker/main.go's key of the same name — not
// a shared constant across modules (see CLAUDE.md: coordinator and worker
// are independent deployables that agree on a wire format, not on shared
// Go code), just the same literal string by convention.
const workerRegistryPrefix = "sentry:worker:"

// workerCounter reports how many workers are currently alive, for
// capacity-aware admission control (M12) — extracted as an interface, same
// reason as every other external dependency in this package, so handler
// tests don't need real Redis.
type workerCounter interface {
	CountLiveWorkers(ctx context.Context) (int, error)
}

// redisWorkerCounter is the production workerCounter. Each worker
// refreshes a self-expiring key on a short interval (worker/main.go's
// heartbeat); counting how many currently exist is a good-enough proxy
// for "how many workers are available right now" without needing a more
// elaborate registration/deregistration protocol.
type redisWorkerCounter struct {
	rdb *redis.Client
}

func (c *redisWorkerCounter) CountLiveWorkers(ctx context.Context) (int, error) {
	var count int
	iter := c.rdb.Scan(ctx, 0, workerRegistryPrefix+"*", 100).Iterator()
	for iter.Next(ctx) {
		count++
	}
	if err := iter.Err(); err != nil {
		return 0, fmt.Errorf("scanning worker registry: %w", err)
	}
	return count, nil
}
