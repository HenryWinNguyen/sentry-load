package main

import (
	"sync"
	"time"
)

const stateTTL = 10 * time.Minute

// stateStore issues and single-use-validates the CSRF `state` parameter for
// the GitHub OAuth flow. A state is only ever accepted once, and expires if
// the callback never comes back (abandoned login attempt).
type stateStore struct {
	mu     sync.Mutex
	issued map[string]time.Time
}

func newStateStore() *stateStore {
	return &stateStore{issued: make(map[string]time.Time)}
}

func (s *stateStore) Issue() (string, error) {
	token, err := randomToken()
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.issued[token] = time.Now()
	return token, nil
}

// Consume reports whether state is a valid, unexpired, not-yet-used state
// token, consuming it either way (a state can never be checked twice).
func (s *stateStore) Consume(state string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	issuedAt, ok := s.issued[state]
	delete(s.issued, state)
	if !ok {
		return false
	}
	return time.Since(issuedAt) <= stateTTL
}
