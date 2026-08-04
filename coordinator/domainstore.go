package main

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"
)

// DomainStore tracks per-domain verification challenges and verified
// status in memory. Good enough for the walking-skeleton stage — losing
// this on restart just means re-verifying, not losing test history.
// Persisting it to Postgres is M10's job (SCOPE.md), not before.
type DomainStore struct {
	mu         sync.Mutex
	challenges map[string]string
	verified   map[string]bool
}

func NewDomainStore() *DomainStore {
	return &DomainStore{
		challenges: make(map[string]string),
		verified:   make(map[string]bool),
	}
}

// IssueChallenge generates a fresh random token for domain and remembers
// it, overwriting any previous unclaimed challenge for that domain.
func (s *DomainStore) IssueChallenge(domain string) (string, error) {
	token, err := randomToken()
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.challenges[domain] = token
	return token, nil
}

// Challenge returns the outstanding token for domain, if any.
func (s *DomainStore) Challenge(domain string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.challenges[domain]
	return t, ok
}

// MarkVerified records domain as verified.
func (s *DomainStore) MarkVerified(domain string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.verified[domain] = true
}

// IsVerified reports whether domain has been successfully verified.
func (s *DomainStore) IsVerified(domain string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.verified[domain]
}

func randomToken() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generating verification token: %w", err)
	}
	return hex.EncodeToString(b), nil
}
