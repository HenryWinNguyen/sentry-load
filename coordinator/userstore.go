package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"sync"
	"time"
)

// sessionTTL bounds how long a bearer token stays valid. Sessions used to
// live exactly as long as the coordinator process did; now that they
// survive restarts (identityPersister), they need an expiry of their own
// or a leaked token would be good forever. The dashboard already handles a
// 401 by dropping the token and sending the user back to log in.
const sessionTTL = 30 * 24 * time.Hour

// persistTimeout caps each write-through call to Postgres, so a slow or
// unreachable database fails a login/verify request quickly instead of
// hanging it.
const persistTimeout = 5 * time.Second

// User is a coordinator identity, backed by a GitHub account. GitHubID
// (not login) is the stable key — GitHub logins can be renamed, IDs can't.
type User struct {
	ID          string
	GitHubID    int64
	GitHubLogin string
	// WebhookURL, if set, is a Discord/Slack incoming-webhook URL the
	// coordinator POSTs a short summary to whenever one of this user's
	// tests finishes.
	WebhookURL string
}

// userIDForGitHub derives a user's ID from their GitHub account ID. It
// used to be a random token minted on first login, which meant every
// coordinator restart gave every returning user a *new* ID — orphaning
// their Postgres test history (keyed by owner_id) with no way back to it.
// Deriving it makes the ID stable across restarts even with no database
// configured at all.
func userIDForGitHub(githubID int64) string {
	return "gh-" + strconv.FormatInt(githubID, 10)
}

// hashSessionToken is how a session token is keyed everywhere it's
// stored, in memory and in Postgres — a database dump never contains a
// usable bearer token, only its hash. Plain SHA-256 (not bcrypt) is the
// right tool here: the token is 128 bits of randomness, not a guessable
// password, so there's nothing for a slow hash to protect against.
func hashSessionToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

type session struct {
	userID    string
	expiresAt time.Time
}

// UserStore tracks known users and issued session tokens. Reads are always
// served from memory; if a persister is attached (Postgres configured, see
// LoadUserStore) every write goes through to it first, so users, sessions,
// and webhook settings survive a coordinator restart instead of logging
// everyone out and forgetting their settings.
type UserStore struct {
	mu       sync.Mutex
	byGitHub map[int64]*User    // GitHubID -> user
	byID     map[string]*User   // User.ID -> user (same *User values as byGitHub)
	sessions map[string]session // hashSessionToken(token) -> session
	persist  identityPersister  // nil = in-memory only
	now      func() time.Time
}

func NewUserStore() *UserStore {
	return &UserStore{
		byGitHub: make(map[int64]*User),
		byID:     make(map[string]*User),
		sessions: make(map[string]session),
		now:      time.Now,
	}
}

// LoadUserStore builds a UserStore backed by p, pre-populated with every
// persisted user and every still-unexpired session.
func LoadUserStore(ctx context.Context, p identityPersister) (*UserStore, error) {
	s := NewUserStore()
	s.persist = p

	users, err := p.LoadUsers(ctx)
	if err != nil {
		return nil, fmt.Errorf("loading users: %w", err)
	}
	for i := range users {
		u := users[i]
		s.byGitHub[u.GitHubID] = &u
		s.byID[u.ID] = &u
	}

	sessions, err := p.LoadSessions(ctx, s.now())
	if err != nil {
		return nil, fmt.Errorf("loading sessions: %w", err)
	}
	for _, ps := range sessions {
		s.sessions[ps.TokenHash] = session{userID: ps.UserID, expiresAt: ps.ExpiresAt}
	}
	return s, nil
}

// FindOrCreate returns the existing user for a GitHub account, creating one
// on first login. A user's GitHub login is refreshed on every login in case
// they renamed their account.
func (s *UserStore) FindOrCreate(githubID int64, githubLogin string) (*User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	u, ok := s.byGitHub[githubID]
	if !ok {
		u = &User{ID: userIDForGitHub(githubID), GitHubID: githubID}
	}
	updated := *u
	updated.GitHubLogin = githubLogin
	if err := s.save(updated); err != nil {
		return nil, err
	}

	u.GitHubLogin = githubLogin
	s.byGitHub[githubID] = u
	s.byID[u.ID] = u
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
	hash := hashSessionToken(token)
	expiresAt := s.now().Add(sessionTTL)

	if s.persist != nil {
		ctx, cancel := context.WithTimeout(context.Background(), persistTimeout)
		defer cancel()
		if err := s.persist.SaveSession(ctx, persistedSession{TokenHash: hash, UserID: userID, ExpiresAt: expiresAt}); err != nil {
			return "", fmt.Errorf("persisting session: %w", err)
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions[hash] = session{userID: userID, expiresAt: expiresAt}
	return token, nil
}

// UserForSession resolves a bearer token to a user, or ok=false if the
// token is unknown, invalid, or expired.
func (s *UserStore) UserForSession(token string) (*User, bool) {
	hash := hashSessionToken(token)

	s.mu.Lock()
	defer s.mu.Unlock()

	sess, ok := s.sessions[hash]
	if !ok {
		return nil, false
	}
	if !s.now().Before(sess.expiresAt) {
		// Lazy cleanup: the in-memory entry goes on first use after
		// expiry. Postgres rows are filtered out on load instead, so
		// nothing ever needs a background sweeper.
		delete(s.sessions, hash)
		return nil, false
	}
	u, ok := s.byID[sess.userID]
	return u, ok
}

// GetByID looks up a user by their stable ID, independent of any session
// token — needed by the results watcher to resolve a finished test's
// owner for webhook delivery, where there's no HTTP request/session in
// play at all.
func (s *UserStore) GetByID(userID string) (*User, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	u, ok := s.byID[userID]
	return u, ok
}

// SetWebhookURL updates userID's configured chat webhook. An empty string
// clears it. Returns ok=false if userID doesn't exist.
func (s *UserStore) SetWebhookURL(userID, webhookURL string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	u, ok := s.byID[userID]
	if !ok {
		return false, nil
	}
	updated := *u
	updated.WebhookURL = webhookURL
	if err := s.save(updated); err != nil {
		return true, err
	}
	u.WebhookURL = webhookURL
	return true, nil
}

// save writes u through to the persister, if one is attached. Called with
// s.mu held — logins and settings changes are rare enough that serializing
// them behind one database round trip is simpler than anything cleverer,
// and it rules out two concurrent logins racing to create the same user.
func (s *UserStore) save(u User) error {
	if s.persist == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), persistTimeout)
	defer cancel()
	if err := s.persist.SaveUser(ctx, u); err != nil {
		return fmt.Errorf("persisting user %s: %w", u.ID, err)
	}
	return nil
}
