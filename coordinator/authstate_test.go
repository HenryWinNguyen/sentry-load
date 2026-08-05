package main

import "testing"

func TestStateStoreIssueAndConsume(t *testing.T) {
	s := newStateStore()

	state, err := s.Issue()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if state == "" {
		t.Fatal("expected a non-empty state token")
	}
	if !s.Consume(state) {
		t.Fatal("expected a freshly-issued state to be consumable")
	}
}

func TestStateStoreConsumeIsSingleUse(t *testing.T) {
	s := newStateStore()
	state, _ := s.Issue()

	if !s.Consume(state) {
		t.Fatal("expected the first consume to succeed")
	}
	if s.Consume(state) {
		t.Fatal("expected the second consume of the same state to fail")
	}
}

func TestStateStoreConsumeUnknownFails(t *testing.T) {
	s := newStateStore()
	if s.Consume("never-issued") {
		t.Fatal("expected an unknown state to fail")
	}
}
