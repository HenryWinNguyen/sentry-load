package main

import (
	"log"
	"math"
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
	abandoned     bool // gave up after repeated worker failures (see worker/reclaim.go)
	// sketch is this sub-job's latency histogram so far (sketch.go), nil
	// if the worker didn't send one (an older worker binary) or it didn't
	// decode.
	sketch *latencySketch
}

// resultUpdate is one results-stream snapshot for a sub-job, as reported
// by its worker.
type resultUpdate struct {
	Requests      int
	Errors        int
	RPS           float64
	P50, P95, P99 string
	Done          bool
	CircuitBroken bool
	LatencySketch string // worker/sketch.go's wire format; empty from older workers
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
func (s *TestStore) Update(testID, jobID string, u resultUpdate) (justFinished bool) {
	var sketch *latencySketch
	if u.LatencySketch != "" {
		var err error
		if sketch, err = decodeLatencySketch(u.LatencySketch); err != nil {
			log.Printf("ignoring latency sketch for job %s: %v", jobID, err)
		}
	}

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
	sj.requests, sj.errors, sj.rps = u.Requests, u.Errors, u.RPS
	sj.p50, sj.p95, sj.p99 = u.P50, u.P95, u.P99
	sj.done = u.Done
	sj.circuitBroken = u.CircuitBroken
	sj.sketch = sketch

	s.notifySubscribers(testID, buildSnapshot(t))

	return !wasDone && allSubJobsDone(t)
}

// MarkAbandoned records that the worker fleet gave up on a sub-job after
// it hit its max delivery count (every worker that picked it up died
// mid-run). The sub-job counts as done, so the test can finish instead of
// waiting forever, and keeps whatever numbers were last reported for it
// rather than having them zeroed. Reports justFinished exactly like Update.
func (s *TestStore) MarkAbandoned(testID, jobID string) (justFinished bool) {
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
	sj.done = true
	sj.abandoned = true

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
	// Abandoned means no worker managed to finish this sub-job: each one
	// that picked it up died mid-run, until it hit the max delivery count.
	// Its numbers are whatever the last worker reported before dying.
	Abandoned bool `json:"abandoned"`
}

// TestSnapshot is the JSON-friendly view of a whole test: merged totals
// plus each sub-job's own numbers. The test-wide percentiles come from
// merging every worker's latency sketch (sketch.go) — never from averaging
// per-worker percentiles, which isn't statistically valid (verified live
// in M6: workers on different infra had genuinely different latency
// distributions).
type TestSnapshot struct {
	TestID string `json:"test_id"`
	URL    string `json:"url"`
	// Label is a user-set display name, empty until someone sets one via
	// PUT /tests/{id}/label — only ever populated from Postgres history,
	// same as FinishedAt, since a live in-flight test in TestStore has no
	// concept of a label yet.
	Label         string  `json:"label,omitempty"`
	Done          bool    `json:"done"`
	CircuitBroken bool    `json:"circuit_broken"` // true if any sub-job aborted early (M9)
	Abandoned     bool    `json:"abandoned"`      // true if any sub-job was abandoned (see SubJobSnapshot.Abandoned)
	TotalRequests int     `json:"total_requests"`
	TotalErrors   int     `json:"total_errors"`
	CombinedRPS   float64 `json:"combined_rps"`
	// P50MS/P95MS/P99MS are test-wide latency percentiles across every
	// worker, within 1% of the true value. Nil when they can't be computed
	// honestly: no requests yet, a sub-job from a worker too old to send a
	// sketch, or a test persisted before sketches existed.
	P50MS   *float64         `json:"p50_ms,omitempty"`
	P95MS   *float64         `json:"p95_ms,omitempty"`
	P99MS   *float64         `json:"p99_ms,omitempty"`
	SubJobs []SubJobSnapshot `json:"sub_jobs"`
	// FinishedAt is only populated for snapshots loaded from Postgres
	// history (nil for anything still live in TestStore) — used for the
	// per-target trend view, which needs a real timestamp to chart
	// against, not just insertion order.
	FinishedAt *time.Time `json:"finished_at,omitempty"`
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
			Abandoned:     sj.abandoned,
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
		if sj.abandoned {
			snap.Abandoned = true
		}
	}
	sort.Slice(snap.SubJobs, func(i, j int) bool { return snap.SubJobs[i].JobID < snap.SubJobs[j].JobID })
	snap.P50MS, snap.P95MS, snap.P99MS = combinedPercentiles(t)
	return snap
}

// combinedPercentiles merges every sub-job's latency sketch into one and
// reads the test-wide p50/p95/p99 off it. All nil unless every sub-job
// that has sent requests also sent a sketch — a merge missing one worker's
// samples would be a wrong answer presented as a right one.
func combinedPercentiles(t *TestState) (p50, p95, p99 *float64) {
	merged := &latencySketch{counts: make(map[int]uint64)}
	for _, sj := range t.SubJobs {
		if sj.requests == 0 {
			continue
		}
		if sj.sketch == nil {
			return nil, nil, nil
		}
		merged.merge(sj.sketch)
	}
	q := func(q float64) *float64 {
		v, ok := merged.quantileMS(q)
		if !ok {
			return nil
		}
		v = math.Round(v*10) / 10
		return &v
	}
	return q(0.50), q(0.95), q(0.99)
}
