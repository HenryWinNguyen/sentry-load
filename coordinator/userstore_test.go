package main

import "testing"

func TestUserStoreFindOrCreateIsIdempotent(t *testing.T) {
	s := NewUserStore()

	first, err := s.FindOrCreate(123, "henry")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	second, err := s.FindOrCreate(123, "henry")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if first.ID != second.ID {
		t.Fatalf("got different user IDs (%q, %q) for the same GitHub ID", first.ID, second.ID)
	}
}

func TestUserStoreFindOrCreateRefreshesLoginOnRename(t *testing.T) {
	s := NewUserStore()

	first, err := s.FindOrCreate(123, "old-name")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	renamed, err := s.FindOrCreate(123, "new-name")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if renamed.ID != first.ID {
		t.Fatal("expected the same user record across a GitHub rename")
	}
	if renamed.GitHubLogin != "new-name" {
		t.Fatalf("got login %q, want new-name", renamed.GitHubLogin)
	}
}

func TestUserStoreDifferentGitHubIDsGetDifferentUsers(t *testing.T) {
	s := NewUserStore()

	a, err := s.FindOrCreate(1, "a")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	b, err := s.FindOrCreate(2, "b")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if a.ID == b.ID {
		t.Fatal("expected distinct users for distinct GitHub IDs")
	}
}

func TestUserStoreSessionRoundTrip(t *testing.T) {
	s := NewUserStore()
	user, err := s.FindOrCreate(123, "henry")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	token, err := s.IssueSession(user.ID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if token == "" {
		t.Fatal("expected a non-empty session token")
	}

	got, ok := s.UserForSession(token)
	if !ok {
		t.Fatal("expected the issued session to resolve to a user")
	}
	if got.ID != user.ID {
		t.Fatalf("got user %q, want %q", got.ID, user.ID)
	}
}

func TestUserStoreUnknownSessionFails(t *testing.T) {
	s := NewUserStore()
	if _, ok := s.UserForSession("does-not-exist"); ok {
		t.Fatal("expected an unknown token to fail")
	}
}

func TestUserStoreMultipleSessionsPerUser(t *testing.T) {
	s := NewUserStore()
	user, _ := s.FindOrCreate(123, "henry")

	tokenA, err := s.IssueSession(user.ID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	tokenB, err := s.IssueSession(user.ID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tokenA == tokenB {
		t.Fatal("expected distinct tokens for distinct sessions")
	}

	if _, ok := s.UserForSession(tokenA); !ok {
		t.Fatal("expected the first session to still be valid")
	}
	if _, ok := s.UserForSession(tokenB); !ok {
		t.Fatal("expected the second session to also be valid")
	}
}
