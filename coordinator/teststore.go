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
	circuitBroken bool // aborted early by the worker's error-rate breaker (M9)
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
// restarted, plus when each user last submitted one (for the M9 cooldown).
// In-memory only, same tradeoff as DomainStore — losing this on restart
// loses in-flight test visibility, not correctness; persisting to Postgres
// is M10's job, not before.
type TestStore struct {
	mu            sync.Mutex
	tests         map[string]*TestState
	lastSubmitted map[string]time.Time           // ownerID -> last Register call
	subscribers   map[string][]chan TestSnapshot // testID -> live WebSocket subscribers (M10)
}

func NewTestStore() *TestStore {
	return &TestStore{
		tests:         make(map[string]*TestState),
		lastSubmitted: make(map[string]time.Time),
		subscribers:   make(map[string][]chan TestSnapshot),
	}
}

// Subscribe registers interest in live updates for testID, returning a
// channel that receives a new TestSnapshot on every Update call that
// touches this test, and an unsubscribe function the caller must call
// exactly once when done (e.g. on WebSocket disconnect) to release it.
// The channel is closed by unsubscribe, never by the store itself.
func (s *TestStore) Subscribe(testID string) (<-chan TestSnapshot, func()) {
	ch := make(chan TestSnapshot, 4)

	s.mu.Lock()
	s.subscribers[testID] = append(s.subscribers[testID], ch)
	s.mu.Unlock()

	var once sync.Once
	unsubscribe := func() {
		once.Do(func() {
			s.mu.Lock()
			defer s.mu.Unlock()
			subs := s.subscribers[testID]
			for i, c := range subs {
				if c == ch {
					s.subscribers[testID] = append(subs[:i], subs[i+1:]...)
					break
				}
			}
			close(ch)
		})
	}
	return ch, unsubscribe
}

// notifySubscribers fans snap out to every live subscriber for its test,
// dropping the update for any subscriber whose channel is currently full
// rather than blocking — a stalled WebSocket reader must never be able to
// stall Update() and, transitively, the shared results-stream watcher.
// Must be called with s.mu held.
func (s *TestStore) notifySubscribers(testID string, snap TestSnapshot) {
	for _, ch := range s.subscribers[testID] {
		select {
		case ch <- snap:
		default:
		}
	}
}

// Register records a newly-enqueued test and its sub-job IDs so incoming
// result snapshots (keyed by test_id/job_id) have somewhere to land, and
// stamps ownerID's cooldown clock. ownerID scopes the test to whichever
// user submitted it (M8).
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
	s.lastSubmitted[ownerID] = state.CreatedAt
}

// CooldownRemaining reports how much longer ownerID must wait before
// submitting another test, given cooldown as the minimum spacing between
// submissions. Zero (or negative) means they're clear to submit now.
func (s *TestStore) CooldownRemaining(ownerID string, cooldown time.Duration) time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()

	last, ok := s.lastSubmitted[ownerID]
	if !ok {
		return 0
	}
	elapsed := time.Since(last)
	if elapsed >= cooldown {
		return 0
	}
	return cooldown - elapsed
}

// Update applies one results-stream snapshot for a sub-job of a test, and
// reports whether every sub-job of the test is now done — the signal
// resultwatcher.go uses to know it's time to persist the finished test
// (M10), without re-triggering on every later message for a test that was
// already done.
// Unknown test/job IDs are ignored rather than erroring — the results
// stream is shared, so a snapshot for a test this store never registered
// (e.g. a stray CLI run, or a test from before a restart) is expected, not
// a bug.
func (s *TestStore) Update(testID, jobID string, requests, errors int, rps float64, p50, p95, p99 string, done, circuitBroken bool) (justFinished bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	t, ok := s.tests[testID]
	if !ok {
		return false
	}
	sj, ok := t.SubJobs[jobID]
	if !ok {
		return false
	}
	wasDone := allSubJobsDone(t)
	sj.requests, sj.errors, sj.rps = requests, errors, rps
	sj.p50, sj.p95, sj.p99 = p50, p95, p99
	sj.done = done
	sj.circuitBroken = circuitBroken

	s.notifySubscribers(testID, buildSnapshot(t))

	return !wasDone && allSubJobsDone(t)
}

func allSubJobsDone(t *TestState) bool {
	for _, sj := range t.SubJobs {
		if !sj.done {
			return false
		}
	}
	return true
}

// snapshotUnscoped is Snapshot without the owner check — for trusted
// internal callers (the persistence trigger in resultwatcher.go) that
// legitimately need any test's data regardless of who owns it. Never
// expose this to a handler.
func (s *TestStore) snapshotUnscoped(testID string) (TestSnapshot, string, bool) {
	s.mu.Lock()
	ownerID := ""
	if t, ok := s.tests[testID]; ok {
		ownerID = t.OwnerID
	}
	s.mu.Unlock()

	snap, ok := s.Snapshot(testID, ownerID)
	return snap, ownerID, ok
}

// SubJobSnapshot is the JSON-friendly view of one sub-job's latest state.
type SubJobSnapshot struct {
	JobID         string  `json:"job_id"`
	Requests      int     `json:"requests"`
	Errors        int     `json:"errors"`
	RPS           float64 `json:"rps"`
	P50MS         string  `json:"p50_ms"`
	P95MS         string  `json:"p95_ms"`
	P99MS         string  `json:"p99_ms"`
	Done          bool    `json:"done"`
	CircuitBroken bool    `json:"circuit_broken"`
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
	CircuitBroken bool             `json:"circuit_broken"` // true if any sub-job aborted early (M9)
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
	return buildSnapshot(t), true
}

// buildSnapshot merges a TestState's sub-jobs into the JSON-friendly
// TestSnapshot view. Shared by Snapshot and Update (the latter to build
// the payload it fans out to WebSocket subscribers) — must be called with
// s.mu already held.
func buildSnapshot(t *TestState) TestSnapshot {
	snap := TestSnapshot{TestID: t.TestID, URL: t.URL, Done: true}
	for jobID, sj := range t.SubJobs {
		snap.SubJobs = append(snap.SubJobs, SubJobSnapshot{
			JobID:         jobID,
			Requests:      sj.requests,
			Errors:        sj.errors,
			RPS:           sj.rps,
			P50MS:         sj.p50,
			P95MS:         sj.p95,
			P99MS:         sj.p99,
			Done:          sj.done,
			CircuitBroken: sj.circuitBroken,
		})
		snap.TotalRequests += sj.requests
		snap.TotalErrors += sj.errors
		snap.CombinedRPS += sj.rps
		if !sj.done {
			snap.Done = false
		}
		if sj.circuitBroken {
			snap.CircuitBroken = true
		}
	}
	sort.Slice(snap.SubJobs, func(i, j int) bool { return snap.SubJobs[i].JobID < snap.SubJobs[j].JobID })
	return snap
}
