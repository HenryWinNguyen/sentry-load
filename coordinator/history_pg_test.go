package main

import (
	"context"
	"os"
	"testing"
)

// TestPostgresHistoryRoundTrip runs history's real SQL against a real
// database — skipped unless TEST_POSTGRES_URL is set (see
// TestPostgresIdentityRoundTrip).
func TestPostgresHistoryRoundTrip(t *testing.T) {
	url := os.Getenv("TEST_POSTGRES_URL")
	if url == "" {
		t.Skip("TEST_POSTGRES_URL not set")
	}
	ctx := context.Background()
	h, err := newPostgresHistory(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	if _, err := h.pool.Exec(ctx, `TRUNCATE tests CASCADE`); err != nil {
		t.Fatal(err)
	}

	snap := TestSnapshot{
		TestID: "t-1", URL: "https://example.com/", TotalRequests: 140, TotalErrors: 2, CombinedRPS: 70,
		SubJobs: []SubJobSnapshot{
			{JobID: "a", Requests: 100, RPS: 50, P50MS: "10", P95MS: "20", P99MS: "30", Done: true},
			{JobID: "b", Requests: 40, Errors: 2, RPS: 20, P50MS: "11", P95MS: "21", P99MS: "31", Done: true, Abandoned: true},
		},
	}
	if err := h.Save(ctx, snap, "gh-1"); err != nil {
		t.Fatal(err)
	}
	if err := h.Save(ctx, snap, "gh-1"); err != nil { // idempotent
		t.Fatal(err)
	}

	got, ok, err := h.Get(ctx, "t-1", "gh-1")
	if err != nil || !ok {
		t.Fatalf("Get: ok=%v err=%v", ok, err)
	}
	if !got.Abandoned || len(got.SubJobs) != 2 || !got.SubJobs[1].Abandoned || got.SubJobs[0].Abandoned {
		t.Fatalf("abandoned flags not round-tripped: %+v", got)
	}
	if got.TotalRequests != 140 {
		t.Fatalf("got total requests %d, want 140", got.TotalRequests)
	}
	if _, ok, _ := h.Get(ctx, "t-1", "gh-2"); ok {
		t.Fatal("another user's test was readable")
	}
}
