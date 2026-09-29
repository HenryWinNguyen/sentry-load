package main

import (
	"context"
	"log"
	"time"

	"github.com/redis/go-redis/v9"
)

// Stalled-job recovery. The consumer group gives at-least-once delivery: a
// job stays in the group's pending-entries list (PEL) until XACK'd, and a
// worker only acks once the job is finished. But nothing ever *re-delivered*
// a pending job — if a worker died mid-test, its sub-job sat pending forever
// and the test never finished.
//
// The fix has two halves:
//
//   - Lease renewal. While running a job, a worker re-claims its own entry
//     every leaseRenewInterval (XCLAIM ... JUSTID with min-idle 0), which
//     resets the entry's idle time without bumping its delivery count. A
//     healthy worker's job therefore never looks idle, no matter how long
//     the test runs (up to 300s).
//   - Reclaim. Before reading new work, an idle worker checks the PEL for
//     entries idle longer than staleJobAfter — i.e. whose lease stopped
//     being renewed, because the worker holding it crashed or lost Redis —
//     and claims one. XCLAIM's own min-idle check makes this safe when two
//     workers race for the same entry: only one gets it.
//
// A job that has already been delivered maxDeliveries times is treated as
// poison (it keeps killing whatever runs it, or the fleet keeps dying) and
// is abandoned: acked, and reported to the coordinator as done+abandoned so
// the test finishes instead of hanging.
//
// Deploy note: a worker without lease renewal running alongside workers
// with reclaim would have its long jobs stolen after staleJobAfter, so all
// workers need to be upgraded together.
const (
	leaseRenewInterval = 10 * time.Second
	staleJobAfter      = 45 * time.Second
	maxDeliveries      = 3
)

// jobQueue is the consumer-group side of the jobs stream. Stream and group
// are fields (not the package constants) so integration tests can run
// against throwaway stream names instead of the real sentry:jobs.
type jobQueue struct {
	rdb    *redis.Client
	stream string
	group  string
}

// renewLease resets the idle time of msgID, which consumer must already
// own. JUSTID keeps the delivery counter unchanged.
func (q jobQueue) renewLease(ctx context.Context, consumer, msgID string) error {
	return q.rdb.XClaimJustID(ctx, &redis.XClaimArgs{
		Stream:   q.stream,
		Group:    q.group,
		Consumer: consumer,
		MinIdle:  0,
		Messages: []string{msgID},
	}).Err()
}

// holdLease renews consumer's lease on msgID every interval until the
// returned stop function is called.
func (q jobQueue) holdLease(ctx context.Context, consumer, msgID string, interval time.Duration) (stop func()) {
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := q.renewLease(ctx, consumer, msgID); err != nil && ctx.Err() == nil {
					log.Printf("failed to renew lease on job %s: %v", msgID, err)
				}
			}
		}
	}()
	return func() {
		cancel()
		<-done
	}
}

// claimStale claims one pending entry that has been idle for at least
// minIdle, if there is one. exhausted reports that the entry had already
// been delivered maxDeliveries times before this claim — the caller should
// abandon it rather than run it again. msg is nil when there's nothing to
// reclaim.
//
// consumer's own stale entries are claimable too: a worker restarted under
// the same WORKER_ID after a crash finds its previous incarnation's job
// still pending under its own name.
func (q jobQueue) claimStale(ctx context.Context, consumer string, minIdle time.Duration, maxDeliveries int64) (msg *redis.XMessage, exhausted bool, err error) {
	pending, err := q.rdb.XPendingExt(ctx, &redis.XPendingExtArgs{
		Stream: q.stream,
		Group:  q.group,
		Idle:   minIdle,
		Start:  "-",
		End:    "+",
		Count:  10,
	}).Result()
	if err != nil {
		return nil, false, err
	}

	for _, p := range pending {
		claimed, err := q.rdb.XClaim(ctx, &redis.XClaimArgs{
			Stream:   q.stream,
			Group:    q.group,
			Consumer: consumer,
			MinIdle:  minIdle,
			Messages: []string{p.ID},
		}).Result()
		if err != nil {
			return nil, false, err
		}
		// Empty means another worker claimed it between XPENDING and
		// XCLAIM (its idle time reset), or the entry itself was trimmed
		// from the stream. Either way, not ours — try the next one.
		if len(claimed) == 0 || claimed[0].Values == nil {
			continue
		}
		return &claimed[0], p.RetryCount >= maxDeliveries, nil
	}
	return nil, false, nil
}

// abandonJob reports a poison job to the coordinator as finished, so its
// test completes instead of waiting on a sub-job nothing will ever run.
// Counts are deliberately omitted: the coordinator keeps whatever the last
// worker to run this job reported before dying, rather than having them
// overwritten with zeros.
func abandonJob(ctx context.Context, rdb *redis.Client, values map[string]interface{}) {
	jobID, _ := values["id"].(string)
	testID, _ := values["test_id"].(string)
	log.Printf("abandoning job %s (test %s) after %d deliveries", jobID, testID, maxDeliveries)
	jobsAbandonedTotal.Inc()

	fields := map[string]interface{}{
		"job_id":    jobID,
		"test_id":   testID,
		"done":      "true",
		"abandoned": "true",
	}
	if err := rdb.XAdd(ctx, &redis.XAddArgs{Stream: resultsStream, Values: fields}).Err(); err != nil {
		log.Printf("failed to report abandoned job %s: %v", jobID, err)
	}
}
