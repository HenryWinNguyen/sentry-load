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

func TestTestStoreUpdateReportsJustFinished(t *testing.T) {
	s := NewTestStore()
	s.Register("test-1", "user-1", "http://example.com/fast", []string{"job-a", "job-b"})

	if finished := s.Update("test-1", "job-a", 100, 0, 50.0, "10", "20", "30", false, false); finished {
		t.Fatal("expected justFinished=false while job-a isn't even done yet")
	}
	if finished := s.Update("test-1", "job-a", 150, 0, 50.0, "10", "20", "30", true, false); finished {
		t.Fatal("expected justFinished=false while job-b hasn't reported done yet")
	}
	if finished := s.Update("test-1", "job-b", 200, 0, 60.0, "10", "20", "30", true, false); !finished {
		t.Fatal("expected justFinished=true the moment the last sub-job reports done")
	}
	// A later, redundant "done" message for the same test shouldn't
	// re-trigger — it already transitioned once.
	if finished := s.Update("test-1", "job-b", 200, 0, 60.0, "10", "20", "30", true, false); finished {
		t.Fatal("expected justFinished=false on a repeat done message")
	}
}

func TestTestStoreUpdateJustFinishedIgnoresUnknownIDs(t *testing.T) {
	s := NewTestStore()
	s.Register("test-1", "user-1", "http://example.com/fast", []string{"job-a"})

	if finished := s.Update("unknown-test", "job-a", 1, 0, 1, "", "", "", true, false); finished {
		t.Fatal("expected justFinished=false for an unknown test")
	}
	if finished := s.Update("test-1", "unknown-job", 1, 0, 1, "", "", "", true, false); finished {
		t.Fatal("expected justFinished=false for an unknown sub-job")
	}
}

func TestTestStoreSnapshotUnscoped(t *testing.T) {
	s := NewTestStore()
	s.Register("test-1", "user-1", "http://example.com/fast", []string{"job-a"})

	snap, ownerID, ok := s.snapshotUnscoped("test-1")
	if !ok {
		t.Fatal("expected the registered test to be found")
	}
	if ownerID != "user-1" {
		t.Fatalf("got ownerID %q, want user-1", ownerID)
	}
	if snap.TestID != "test-1" {
		t.Fatalf("got test ID %q, want test-1", snap.TestID)
	}

	if _, _, ok := s.snapshotUnscoped("nope"); ok {
		t.Fatal("expected ok=false for an unregistered test ID")
	}
}

func TestTestStoreSubscribeReceivesUpdates(t *testing.T) {
	s := NewTestStore()
	s.Register("test-1", "user-1", "http://example.com/fast", []string{"job-a"})

	ch, unsubscribe := s.Subscribe("test-1")
	defer unsubscribe()

	s.Update("test-1", "job-a", 100, 0, 50.0, "10", "20", "30", false, false)

	select {
	case snap := <-ch:
		if snap.TotalRequests != 100 {
			t.Fatalf("got total requests %d, want 100", snap.TotalRequests)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for a subscriber update")
	}
}

func TestTestStoreSubscribeUnrelatedTestDoesNotNotify(t *testing.T) {
	s := NewTestStore()
	s.Register("test-1", "user-1", "http://example.com/fast", []string{"job-a"})
	s.Register("test-2", "user-1", "http://example.com/fast", []string{"job-b"})

	ch, unsubscribe := s.Subscribe("test-1")
	defer unsubscribe()

	s.Update("test-2", "job-b", 50, 0, 10.0, "1", "2", "3", true, false)

	select {
	case snap := <-ch:
		t.Fatalf("expected no update for an unrelated test, got %+v", snap)
	case <-time.After(50 * time.Millisecond):
		// expected: nothing arrived
	}
}

func TestTestStoreUnsubscribeStopsUpdatesAndClosesChannel(t *testing.T) {
	s := NewTestStore()
	s.Register("test-1", "user-1", "http://example.com/fast", []string{"job-a"})

	ch, unsubscribe := s.Subscribe("test-1")
	unsubscribe()

	if _, open := <-ch; open {
		t.Fatal("expected the channel to be closed after unsubscribe")
	}

	// Must not panic even though nothing is reading it anymore.
	s.Update("test-1", "job-a", 100, 0, 50.0, "10", "20", "30", true, false)
}

func TestTestStoreSubscribeDoesNotBlockOnFullChannel(t *testing.T) {
	s := NewTestStore()
	s.Register("test-1", "user-1", "http://example.com/fast", []string{"job-a"})

	ch, unsubscribe := s.Subscribe("test-1")
	defer unsubscribe()

	// Never drain the channel — fill it well past its buffer and confirm
	// Update() keeps returning instead of blocking forever on a stalled
	// subscriber.
	done := make(chan struct{})
	go func() {
		for i := 0; i < 20; i++ {
			s.Update("test-1", "job-a", i, 0, 1.0, "1", "2", "3", false, false)
		}
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Update() blocked on a full subscriber channel instead of dropping the update")
	}
	<-ch // drain one, just to use the channel and avoid an unused-var-style lint complaint
}
