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

func TestPostgresHistoryRoundTripsCombinedPercentiles(t *testing.T) {
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

	p50, p95, p99 := 4.2, 88.1, 140.0
	withP := TestSnapshot{TestID: "with", URL: "https://example.com/", P50MS: &p50, P95MS: &p95, P99MS: &p99}
	without := TestSnapshot{TestID: "without", URL: "https://example.com/"}
	for _, s := range []TestSnapshot{withP, without} {
		if err := h.Save(ctx, s, "gh-1"); err != nil {
			t.Fatal(err)
		}
	}

	got, _, err := h.Get(ctx, "with", "gh-1")
	if err != nil || got.P95MS == nil || *got.P95MS != p95 {
		t.Fatalf("combined p95 not round-tripped: %+v %v", got.P95MS, err)
	}
	trend, err := h.ListByURL(ctx, "gh-1", "https://example.com/", 10)
	if err != nil || len(trend) != 2 {
		t.Fatalf("trend: %v %v", trend, err)
	}
	for _, s := range trend {
		if (s.TestID == "without") != (s.P95MS == nil) {
			t.Errorf("test %s: p95 = %v", s.TestID, s.P95MS)
		}
	}
}
