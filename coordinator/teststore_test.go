package main

import (
	"testing"
	"time"
)

func TestTestStoreSnapshotUnknownID(t *testing.T) {
	s := NewTestStore()
	if _, ok := s.Snapshot("nope", "user-1"); ok {
		t.Fatal("expected ok=false for an unregistered test ID")
	}
}

func TestTestStoreRegisterStartsNotDone(t *testing.T) {
	s := NewTestStore()
	s.Register("test-1", "user-1", "http://example.com/fast", []string{"job-a", "job-b"})

	snap, ok := s.Snapshot("test-1", "user-1")
	if !ok {
		t.Fatal("expected the registered test to be found")
	}
	if snap.Done {
		t.Fatal("expected a freshly-registered test to not be done")
	}
	if len(snap.SubJobs) != 2 {
		t.Fatalf("got %d sub-jobs, want 2", len(snap.SubJobs))
	}
}

func TestTestStoreSnapshotScopedToOwner(t *testing.T) {
	s := NewTestStore()
	s.Register("test-1", "user-1", "http://example.com/fast", []string{"job-a"})

	if _, ok := s.Snapshot("test-1", "user-2"); ok {
		t.Fatal("expected a different user's test to be invisible, same as an unknown ID")
	}
	if _, ok := s.Snapshot("test-1", "user-1"); !ok {
		t.Fatal("expected the owning user to still see their own test")
	}
}

func TestTestStoreUpdateMergesAndDetectsDone(t *testing.T) {
	s := NewTestStore()
	s.Register("test-1", "user-1", "http://example.com/fast", []string{"job-a", "job-b"})

	s.Update("test-1", "job-a", 100, 0, 50.0, "10", "20", "30", false, false)
	snap, _ := s.Snapshot("test-1", "user-1")
	if snap.Done {
		t.Fatal("expected not done while job-b hasn't reported yet")
	}
	if snap.TotalRequests != 100 || snap.CombinedRPS != 50.0 {
		t.Fatalf("got requests=%d rps=%v, want 100/50.0", snap.TotalRequests, snap.CombinedRPS)
	}

	s.Update("test-1", "job-b", 200, 5, 75.0, "12", "22", "32", true, false)
	// job-a hasn't reported done=true yet.
	snap, _ = s.Snapshot("test-1", "user-1")
	if snap.Done {
		t.Fatal("expected not done while job-a hasn't reported done=true")
	}

	s.Update("test-1", "job-a", 150, 1, 55.0, "10", "20", "30", true, false)
	snap, _ = s.Snapshot("test-1", "user-1")
	if !snap.Done {
		t.Fatal("expected done once every sub-job reports done=true")
	}
	if snap.TotalRequests != 350 {
		t.Fatalf("got total requests %d, want 350", snap.TotalRequests)
	}
	if snap.TotalErrors != 6 {
		t.Fatalf("got total errors %d, want 6", snap.TotalErrors)
	}
	if snap.CombinedRPS != 130.0 {
		t.Fatalf("got combined rps %v, want 130.0", snap.CombinedRPS)
	}
}

func TestTestStoreUpdateIgnoresUnknownIDs(t *testing.T) {
	s := NewTestStore()
	s.Register("test-1", "user-1", "http://example.com/fast", []string{"job-a"})

	// Neither call should panic or affect the registered test.
	s.Update("unknown-test", "job-a", 999, 0, 0, "", "", "", true, false)
	s.Update("test-1", "unknown-job", 999, 0, 0, "", "", "", true, false)

	snap, _ := s.Snapshot("test-1", "user-1")
	if snap.TotalRequests != 0 {
		t.Fatalf("got total requests %d, want 0 (stray updates should be ignored)", snap.TotalRequests)
	}
}

func TestTestStoreUpdatePropagatesCircuitBroken(t *testing.T) {
	s := NewTestStore()
	s.Register("test-1", "user-1", "http://example.com/fast", []string{"job-a", "job-b"})

	s.Update("test-1", "job-a", 50, 40, 10.0, "10", "20", "30", true, true)
	s.Update("test-1", "job-b", 100, 1, 20.0, "10", "20", "30", true, false)

	snap, _ := s.Snapshot("test-1", "user-1")
	if !snap.CircuitBroken {
		t.Fatal("expected the test-level flag to be set once any sub-job trips the breaker")
	}
	for _, sj := range snap.SubJobs {
		want := sj.JobID == "job-a"
		if sj.CircuitBroken != want {
			t.Errorf("sub-job %s: got circuit_broken=%v, want %v", sj.JobID, sj.CircuitBroken, want)
		}
	}
}

func TestTestStoreCooldownRemaining(t *testing.T) {
	s := NewTestStore()

	if remaining := s.CooldownRemaining("user-1", 30*time.Second); remaining != 0 {
		t.Fatalf("got remaining %v, want 0 for a user who has never submitted", remaining)
	}

	s.Register("test-1", "user-1", "http://example.com/fast", []string{"job-a"})

	remaining := s.CooldownRemaining("user-1", 30*time.Second)
	if remaining <= 0 || remaining > 30*time.Second {
		t.Fatalf("got remaining %v, want something in (0s, 30s] right after submitting", remaining)
	}

	if remaining := s.CooldownRemaining("user-2", 30*time.Second); remaining != 0 {
		t.Fatalf("got remaining %v, want 0 for a different, unrelated user", remaining)
	}

	if remaining := s.CooldownRemaining("user-1", 0); remaining != 0 {
		t.Fatalf("got remaining %v, want 0 when the cooldown itself is disabled (zero)", remaining)
	}
}
