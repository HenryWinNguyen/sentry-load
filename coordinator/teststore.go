package main

import (
	"sort"
	"sync"
	"time"
)

// subJobState is the latest known result snapshot for one sub-job (one
// worker's share of a test).
type subJobState struct {
	requests      int
	errors        int
	rps           float64
	p50, p95, p99 string
	done          bool
}

// TestState is the full in-memory record of one submitted test: which
// sub-jobs it fanned out to and their latest reported state.
type TestState struct {
	TestID    string
	OwnerID   string
	URL       string
	CreatedAt time.Time
	SubJobs   map[string]*subJobState // keyed by job ID
}

// TestStore tracks every test the coordinator has accepted since it last
// restarted. In-memory only, same tradeoff as DomainStore — losing this on
// restart loses in-flight test visibility, not correctness; persisting to
// Postgres is M10's job, not before.
type TestStore struct {
	mu    sync.Mutex
	tests map[string]*TestState
}

func NewTestStore() *TestStore {
	return &TestStore{tests: make(map[string]*TestState)}
}

// Register records a newly-enqueued test and its sub-job IDs so incoming
// result snapshots (keyed by test_id/job_id) have somewhere to land.
// ownerID scopes the test to whichever user submitted it (M8).
func (s *TestStore) Register(testID, ownerID, url string, jobIDs []string) {
	state := &TestState{
		TestID:    testID,
		OwnerID:   ownerID,
		URL:       url,
		CreatedAt: time.Now(),
		SubJobs:   make(map[string]*subJobState, len(jobIDs)),
	}
	for _, id := range jobIDs {
		state.SubJobs[id] = &subJobState{}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.tests[testID] = state
}

// Update applies one results-stream snapshot for a sub-job of a test.
// Unknown test/job IDs are ignored rather than erroring — the results
// stream is shared, so a snapshot for a test this store never registered
// (e.g. a stray CLI run, or a test from before a restart) is expected, not
// a bug.
func (s *TestStore) Update(testID, jobID string, requests, errors int, rps float64, p50, p95, p99 string, done bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	t, ok := s.tests[testID]
	if !ok {
		return
	}
	sj, ok := t.SubJobs[jobID]
	if !ok {
		return
	}
	sj.requests, sj.errors, sj.rps = requests, errors, rps
	sj.p50, sj.p95, sj.p99 = p50, p95, p99
	sj.done = done
}

// SubJobSnapshot is the JSON-friendly view of one sub-job's latest state.
type SubJobSnapshot struct {
	JobID    string  `json:"job_id"`
	Requests int     `json:"requests"`
	Errors   int     `json:"errors"`
	RPS      float64 `json:"rps"`
	P50MS    string  `json:"p50_ms"`
	P95MS    string  `json:"p95_ms"`
	P99MS    string  `json:"p99_ms"`
	Done     bool    `json:"done"`
}

// TestSnapshot is the JSON-friendly view of a whole test: merged totals
// plus each sub-job's own numbers. Percentiles are intentionally per-worker
// only, not averaged into one combined figure — merging percentiles across
// independent samples isn't statistically valid without the raw data
// (verified live in M6: workers on different infra had genuinely different
// latency distributions).
type TestSnapshot struct {
	TestID        string           `json:"test_id"`
	URL           string           `json:"url"`
	Done          bool             `json:"done"`
	TotalRequests int              `json:"total_requests"`
	TotalErrors   int              `json:"total_errors"`
	CombinedRPS   float64          `json:"combined_rps"`
	SubJobs       []SubJobSnapshot `json:"sub_jobs"`
}

// Snapshot returns the current state of testID, scoped to ownerID — a test
// owned by someone else reports ok=false exactly the same as a test that
// doesn't exist at all, so a caller can't use this to probe for the
// existence of other users' tests.
func (s *TestStore) Snapshot(testID, ownerID string) (TestSnapshot, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	t, ok := s.tests[testID]
	if !ok || t.OwnerID != ownerID {
		return TestSnapshot{}, false
	}

	snap := TestSnapshot{TestID: t.TestID, URL: t.URL, Done: true}
	for jobID, sj := range t.SubJobs {
		snap.SubJobs = append(snap.SubJobs, SubJobSnapshot{
			JobID:    jobID,
			Requests: sj.requests,
			Errors:   sj.errors,
			RPS:      sj.rps,
			P50MS:    sj.p50,
			P95MS:    sj.p95,
			P99MS:    sj.p99,
			Done:     sj.done,
		})
		snap.TotalRequests += sj.requests
		snap.TotalErrors += sj.errors
		snap.CombinedRPS += sj.rps
		if !sj.done {
			snap.Done = false
		}
	}
	sort.Slice(snap.SubJobs, func(i, j int) bool { return snap.SubJobs[i].JobID < snap.SubJobs[j].JobID })

	return snap, true
}
