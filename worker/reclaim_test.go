package main

import (
	"context"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

// testQueue returns a jobQueue on a throwaway stream in the Redis at
// TEST_REDIS_ADDR (CI sets it; locally: `docker compose up -d redis` and
// TEST_REDIS_ADDR=localhost:6379), with one job already delivered to a
// consumer named "dead" that will never ack it. Skipped if unset.
func testQueue(t *testing.T) (jobQueue, string) {
	t.Helper()
	addr := os.Getenv("TEST_REDIS_ADDR")
	if addr == "" {
		t.Skip("TEST_REDIS_ADDR not set")
	}
	ctx := context.Background()
	rdb := redis.NewClient(&redis.Options{Addr: addr})
	q := jobQueue{rdb: rdb, stream: "test:jobs:" + strconv.FormatInt(time.Now().UnixNano(), 36), group: "workers"}
	t.Cleanup(func() {
		rdb.Del(context.Background(), q.stream)
		rdb.Close()
	})

	if err := rdb.XGroupCreateMkStream(ctx, q.stream, q.group, "0").Err(); err != nil {
		t.Fatal(err)
	}
	if err := rdb.XAdd(ctx, &redis.XAddArgs{Stream: q.stream, Values: map[string]interface{}{"id": "job-1", "test_id": "test-1"}}).Err(); err != nil {
		t.Fatal(err)
	}
	got, err := rdb.XReadGroup(ctx, &redis.XReadGroupArgs{Group: q.group, Consumer: "dead", Streams: []string{q.stream, ">"}, Count: 1}).Result()
	if err != nil || len(got[0].Messages) != 1 {
		t.Fatalf("delivering job to the dead consumer: %v", err)
	}
	return q, got[0].Messages[0].ID
}

const testMinIdle = 200 * time.Millisecond

func TestClaimStaleWaitsForIdleThreshold(t *testing.T) {
	q, _ := testQueue(t)
	msg, _, err := q.claimStale(context.Background(), "alive", testMinIdle, maxDeliveries)
	if err != nil {
		t.Fatal(err)
	}
	if msg != nil {
		t.Fatal("claimed a job that was delivered moments ago")
	}
}

func TestClaimStaleTakesOverDeadWorkersJob(t *testing.T) {
	q, id := testQueue(t)
	time.Sleep(testMinIdle + 100*time.Millisecond)

	msg, exhausted, err := q.claimStale(context.Background(), "alive", testMinIdle, maxDeliveries)
	if err != nil {
		t.Fatal(err)
	}
	if msg == nil || msg.ID != id {
		t.Fatalf("got %v, want job %s reclaimed", msg, id)
	}
	if exhausted {
		t.Fatal("second delivery reported as exhausted")
	}
	if msg.Values["id"] != "job-1" {
		t.Fatalf("reclaimed job lost its payload: %v", msg.Values)
	}

	// And it now belongs to "alive", so a third worker can't grab it too.
	msg, _, err = q.claimStale(context.Background(), "third", testMinIdle, maxDeliveries)
	if err != nil {
		t.Fatal(err)
	}
	if msg != nil {
		t.Fatal("a just-reclaimed job was claimable again immediately")
	}
}

func TestHeldLeaseIsNeverReclaimed(t *testing.T) {
	q, id := testQueue(t)
	ctx := context.Background()

	// "dead" is actually alive and renewing, much faster than the idle
	// threshold — the long-running-test case.
	stop := q.holdLease(ctx, "dead", id, testMinIdle/4)
	time.Sleep(3 * testMinIdle)

	msg, _, err := q.claimStale(ctx, "alive", testMinIdle, maxDeliveries)
	stop()
	if err != nil {
		t.Fatal(err)
	}
	if msg != nil {
		t.Fatal("reclaimed a job whose lease was being renewed")
	}

	// Once renewal stops, the same job does become reclaimable.
	time.Sleep(testMinIdle + 100*time.Millisecond)
	msg, _, err = q.claimStale(ctx, "alive", testMinIdle, maxDeliveries)
	if err != nil {
		t.Fatal(err)
	}
	if msg == nil {
		t.Fatal("job not reclaimable after its lease lapsed")
	}
}

func TestClaimStaleFlagsPoisonJobs(t *testing.T) {
	q, id := testQueue(t)
	ctx := context.Background()
	// Pretend it has already been delivered maxDeliveries times.
	if err := q.rdb.Do(ctx, "XCLAIM", q.stream, q.group, "dead", 0, id, "RETRYCOUNT", maxDeliveries).Err(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(testMinIdle + 100*time.Millisecond)

	msg, exhausted, err := q.claimStale(ctx, "alive", testMinIdle, maxDeliveries)
	if err != nil {
		t.Fatal(err)
	}
	if msg == nil || !exhausted {
		t.Fatalf("got msg=%v exhausted=%v, want the job flagged as exhausted", msg, exhausted)
	}
}
