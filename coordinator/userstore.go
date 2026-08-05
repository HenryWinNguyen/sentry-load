package main

import (
	"sync"
)

// User is a coordinator identity, backed by a GitHub account. GitHubID
// (not login) is the stable key — GitHub logins can be renamed, IDs can't.
type User struct {
	ID          string
	GitHubID    int64
	GitHubLogin string
}

// UserStore tracks known users and issued session tokens in memory. Same
// tradeoff as DomainStore/TestStore: losing this on restart just means
// re-logging-in, not losing correctness. Persisting to Postgres is M10's
// job, not before.
type UserStore struct {
	mu       sync.Mutex
	byGitHub map[int64]*User   // GitHubID -> user
	byID     map[string]*User  // User.ID -> user (same *User values as byGitHub)
	sessions map[string]string // session token -> user ID
}

func NewUserStore() *UserStore {
	return &UserStore{
		byGitHub: make(map[int64]*User),
		byID:     make(map[string]*User),
		sessions: make(map[string]string),
	}
}

// FindOrCreate returns the existing user for a GitHub account, creating one
// on first login. A user's GitHub login is refreshed on every login in case
// they renamed their account.
func (s *UserStore) FindOrCreate(githubID int64, githubLogin string) (*User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if u, ok := s.byGitHub[githubID]; ok {
		u.GitHubLogin = githubLogin
		return u, nil
	}

	id, err := randomToken()
	if err != nil {
		return nil, err
	}
	u := &User{ID: id, GitHubID: githubID, GitHubLogin: githubLogin}
	s.byGitHub[githubID] = u
	s.byID[id] = u
	return u, nil
}

// IssueSession creates a fresh bearer token for a user and returns it. A
// user can hold multiple valid sessions at once (e.g. logged in from two
// terminals) — there's no single-session-per-user constraint.
func (s *UserStore) IssueSession(userID string) (string, error) {
	token, err := randomToken()
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions[token] = userID
	return token, nil
}

// UserForSession resolves a bearer token to a user, or ok=false if the
// token is unknown/invalid.
func (s *UserStore) UserForSession(token string) (*User, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	userID, ok := s.sessions[token]
	if !ok {
		return nil, false
	}
	u, ok := s.byID[userID]
	return u, ok
}
