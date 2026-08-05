package main

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// testHistoryStore persists finished tests so they survive a coordinator
// restart and can be listed later (SCOPE.md M10: "test history in
// Postgres"). Extracted as an interface, same reason as every other
// external dependency in this package (verify.go, githubauth.go) — tests
// use a fake instead of a real database.
type testHistoryStore interface {
	Save(ctx context.Context, snap TestSnapshot, ownerID string) error
	Get(ctx context.Context, testID, ownerID string) (TestSnapshot, bool, error)
	List(ctx context.Context, ownerID string, limit int) ([]TestSnapshot, error)
}

// postgresHistory is the production testHistoryStore, backed by Postgres.
// Only finished tests are written — TestStore already handles live
// in-flight state in memory, and persisting on every per-second tick would
// be a lot of writes for no benefit history doesn't need (see runbook note
// in docs/PROGRESS.md M10 entry).
type postgresHistory struct {
	pool *pgxpool.Pool
}

func newPostgresHistory(ctx context.Context, connString string) (*postgresHistory, error) {
	pool, err := pgxpool.New(ctx, connString)
	if err != nil {
		return nil, fmt.Errorf("connecting to postgres: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("pinging postgres: %w", err)
	}
	if err := migrate(ctx, pool); err != nil {
		pool.Close()
		return nil, fmt.Errorf("running migrations: %w", err)
	}
	return &postgresHistory{pool: pool}, nil
}

func (h *postgresHistory) Close() {
	h.pool.Close()
}

// migrate applies the schema idempotently. Two tables, no ORM, no separate
// migration tool — proportionate to the schema's actual size (see
// CLAUDE.md's "don't over-engineer" guidance).
func migrate(ctx context.Context, pool *pgxpool.Pool) error {
	_, err := pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS tests (
			test_id        TEXT PRIMARY KEY,
			owner_id       TEXT NOT NULL,
			url            TEXT NOT NULL,
			total_requests INT NOT NULL,
			total_errors   INT NOT NULL,
			combined_rps   DOUBLE PRECISION NOT NULL,
			circuit_broken BOOLEAN NOT NULL,
			finished_at    TIMESTAMPTZ NOT NULL
		);
		CREATE INDEX IF NOT EXISTS idx_tests_owner ON tests (owner_id, finished_at DESC);

		CREATE TABLE IF NOT EXISTS sub_jobs (
			job_id         TEXT PRIMARY KEY,
			test_id        TEXT NOT NULL REFERENCES tests (test_id) ON DELETE CASCADE,
			requests       INT NOT NULL,
			errors         INT NOT NULL,
			rps            DOUBLE PRECISION NOT NULL,
			p50_ms         TEXT NOT NULL,
			p95_ms         TEXT NOT NULL,
			p99_ms         TEXT NOT NULL,
			circuit_broken BOOLEAN NOT NULL
		);
		CREATE INDEX IF NOT EXISTS idx_sub_jobs_test ON sub_jobs (test_id);
	`)
	return err
}

// Save upserts a finished test and its sub-jobs — idempotent so it's safe
// to call more than once for the same test (defensive; the watcher only
// calls it once per test in practice, see resultwatcher.go).
func (h *postgresHistory) Save(ctx context.Context, snap TestSnapshot, ownerID string) error {
	tx, err := h.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("beginning transaction: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op if Commit already succeeded

	_, err = tx.Exec(ctx, `
		INSERT INTO tests (test_id, owner_id, url, total_requests, total_errors, combined_rps, circuit_broken, finished_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (test_id) DO UPDATE SET
			total_requests = EXCLUDED.total_requests,
			total_errors   = EXCLUDED.total_errors,
			combined_rps   = EXCLUDED.combined_rps,
			circuit_broken = EXCLUDED.circuit_broken,
			finished_at    = EXCLUDED.finished_at
	`, snap.TestID, ownerID, snap.URL, snap.TotalRequests, snap.TotalErrors, snap.CombinedRPS, snap.CircuitBroken, time.Now())
	if err != nil {
		return fmt.Errorf("upserting test: %w", err)
	}

	for _, sj := range snap.SubJobs {
		_, err = tx.Exec(ctx, `
			INSERT INTO sub_jobs (job_id, test_id, requests, errors, rps, p50_ms, p95_ms, p99_ms, circuit_broken)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
			ON CONFLICT (job_id) DO UPDATE SET
				requests       = EXCLUDED.requests,
				errors         = EXCLUDED.errors,
				rps            = EXCLUDED.rps,
				p50_ms         = EXCLUDED.p50_ms,
				p95_ms         = EXCLUDED.p95_ms,
				p99_ms         = EXCLUDED.p99_ms,
				circuit_broken = EXCLUDED.circuit_broken
		`, sj.JobID, snap.TestID, sj.Requests, sj.Errors, sj.RPS, sj.P50MS, sj.P95MS, sj.P99MS, sj.CircuitBroken)
		if err != nil {
			return fmt.Errorf("upserting sub-job %s: %w", sj.JobID, err)
		}
	}

	return tx.Commit(ctx)
}

// Get returns a persisted test, scoped to ownerID the same way
// TestStore.Snapshot is — a test owned by someone else reports ok=false
// exactly like an unknown ID.
func (h *postgresHistory) Get(ctx context.Context, testID, ownerID string) (TestSnapshot, bool, error) {
	var snap TestSnapshot
	err := h.pool.QueryRow(ctx, `
		SELECT test_id, url, total_requests, total_errors, combined_rps, circuit_broken
		FROM tests WHERE test_id = $1 AND owner_id = $2
	`, testID, ownerID).Scan(&snap.TestID, &snap.URL, &snap.TotalRequests, &snap.TotalErrors, &snap.CombinedRPS, &snap.CircuitBroken)
	if err != nil {
		return TestSnapshot{}, false, nil //nolint:nilerr // "not found" isn't a caller-facing error here, same as TestStore.Snapshot
	}
	snap.Done = true

	rows, err := h.pool.Query(ctx, `
		SELECT job_id, requests, errors, rps, p50_ms, p95_ms, p99_ms, circuit_broken
		FROM sub_jobs WHERE test_id = $1 ORDER BY job_id
	`, testID)
	if err != nil {
		return TestSnapshot{}, false, fmt.Errorf("querying sub-jobs for %s: %w", testID, err)
	}
	defer rows.Close()

	for rows.Next() {
		var sj SubJobSnapshot
		if err := rows.Scan(&sj.JobID, &sj.Requests, &sj.Errors, &sj.RPS, &sj.P50MS, &sj.P95MS, &sj.P99MS, &sj.CircuitBroken); err != nil {
			return TestSnapshot{}, false, fmt.Errorf("scanning sub-job row: %w", err)
		}
		sj.Done = true
		snap.SubJobs = append(snap.SubJobs, sj)
	}

	return snap, true, rows.Err()
}

// List returns ownerID's most recent tests, newest first, capped at limit.
func (h *postgresHistory) List(ctx context.Context, ownerID string, limit int) ([]TestSnapshot, error) {
	rows, err := h.pool.Query(ctx, `
		SELECT test_id, url, total_requests, total_errors, combined_rps, circuit_broken
		FROM tests WHERE owner_id = $1 ORDER BY finished_at DESC LIMIT $2
	`, ownerID, limit)
	if err != nil {
		return nil, fmt.Errorf("querying test history for %s: %w", ownerID, err)
	}
	defer rows.Close()

	var snaps []TestSnapshot
	for rows.Next() {
		var snap TestSnapshot
		if err := rows.Scan(&snap.TestID, &snap.URL, &snap.TotalRequests, &snap.TotalErrors, &snap.CombinedRPS, &snap.CircuitBroken); err != nil {
			return nil, fmt.Errorf("scanning test row: %w", err)
		}
		snap.Done = true
		snaps = append(snaps, snap)
	}
	return snaps, rows.Err()
}
