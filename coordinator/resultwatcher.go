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
//
// history may be nil (Postgres not configured, M10's persistence layer is
// optional local-dev-friendly like GitHub OAuth in M8) — a finished test
// is persisted here, once, right when its last sub-job reports done.
//
// users/webhooks/dashboardURL drive the finished-test webhook
// notification, decoupled from history entirely — a coordinator with no
// Postgres configured still notifies webhooks, and vice versa.
func watchResults(ctx context.Context, rdb *redis.Client, store *TestStore, history testHistoryStore, users *UserStore, webhooks webhookNotifier, dashboardURL string) {
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
				testID := strField(msg.Values["test_id"])
				justFinished := store.Update(
					testID,
					strField(msg.Values["job_id"]),
					intField(msg.Values["requests"]),
					intField(msg.Values["errors"]),
					floatField(msg.Values["rps"]),
					strField(msg.Values["p50_ms"]),
					strField(msg.Values["p95_ms"]),
					strField(msg.Values["p99_ms"]),
					msg.Values["done"] == "true",
					msg.Values["circuit_broken"] == "true",
				)
				if justFinished {
					onTestFinished(ctx, store, history, users, webhooks, dashboardURL, testID)
				}
			}
		}
	}
}

// onTestFinished handles everything that happens once, exactly when a
// test's last sub-job reports done: persisting to history (if configured)
// and notifying the owner's webhook (if they have one configured) — the
// two are independent of each other, neither gates the other.
func onTestFinished(ctx context.Context, store *TestStore, history testHistoryStore, users *UserStore, webhooks webhookNotifier, dashboardURL, testID string) {
	snap, ownerID, ok := store.snapshotUnscoped(testID)
	if !ok {
		return
	}

	if history != nil {
		saveCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		if err := history.Save(saveCtx, snap, ownerID); err != nil {
			log.Printf("failed to persist finished test %s: %v", testID, err)
		}
		cancel()
	}

	// Off the hot path: a slow or unreachable webhook endpoint must not
	// stall the shared results watcher from processing the next test's
	// updates, the same non-blocking-fan-out reasoning as the WebSocket
	// subscriber layer in live.go.
	go notifyFinishedTest(ctx, history, users, webhooks, dashboardURL, snap, ownerID)
}

func notifyFinishedTest(ctx context.Context, history testHistoryStore, users *UserStore, webhooks webhookNotifier, dashboardURL string, snap TestSnapshot, ownerID string) {
	user, ok := users.GetByID(ownerID)
	if !ok || user.WebhookURL == "" {
		return
	}

	reportURL := ""
	if history != nil {
		if token, ok, err := history.EnsureShareToken(ctx, snap.TestID, ownerID); err == nil && ok {
			reportURL = dashboardURL + "/reports/" + token
		}
	}

	notifyCtx, cancel := context.WithTimeout(ctx, webhookTimeout)
	defer cancel()
	if err := webhooks.Notify(notifyCtx, user.WebhookURL, snap, reportURL); err != nil {
		log.Printf("failed to send webhook for test %s: %v", snap.TestID, err)
	}
}
