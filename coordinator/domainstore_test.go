package main

import "testing"

func TestDomainStoreIssueAndCheckChallenge(t *testing.T) {
	s := NewDomainStore()

	if _, ok := s.Challenge("example.com"); ok {
		t.Fatal("expected no challenge before one is issued")
	}

	token, err := s.IssueChallenge("example.com")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if token == "" {
		t.Fatal("expected a non-empty token")
	}

	got, ok := s.Challenge("example.com")
	if !ok {
		t.Fatal("expected a challenge to be present after issuing one")
	}
	if got != token {
		t.Fatalf("got %q, want %q", got, token)
	}
}

func TestDomainStoreDifferentDomainsGetDifferentTokens(t *testing.T) {
	s := NewDomainStore()

	tokenA, err := s.IssueChallenge("a.com")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	tokenB, err := s.IssueChallenge("b.com")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if tokenA == tokenB {
		t.Fatalf("expected distinct tokens, got the same value %q for both domains", tokenA)
	}
}

func TestDomainStoreReissueOverwritesPreviousChallenge(t *testing.T) {
	s := NewDomainStore()

	first, err := s.IssueChallenge("example.com")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	second, err := s.IssueChallenge("example.com")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if first == second {
		t.Fatal("expected re-issuing to produce a new token")
	}

	got, ok := s.Challenge("example.com")
	if !ok || got != second {
		t.Fatalf("got (%q, %v), want (%q, true)", got, ok, second)
	}
}

func TestDomainStoreVerifiedRoundTrip(t *testing.T) {
	s := NewDomainStore()

	if s.IsVerified("example.com") {
		t.Fatal("expected domain to be unverified before MarkVerified")
	}

	s.MarkVerified("example.com")

	if !s.IsVerified("example.com") {
		t.Fatal("expected domain to be verified after MarkVerified")
	}
	if s.IsVerified("other.com") {
		t.Fatal("expected an unrelated domain to remain unverified")
	}
}
