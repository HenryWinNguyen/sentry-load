package main

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// fakeIdentity is an in-memory identityPersister standing in for Postgres,
// so the restart-survival behavior can be tested without a database. fail,
// if set, makes every Save call return it.
type fakeIdentity struct {
	mu       sync.Mutex
	users    map[string]User
	sessions map[string]persistedSession
	domains  map[verifiedDomain]bool
	fail     error
}

func newFakeIdentity() *fakeIdentity {
	return &fakeIdentity{
		users:    make(map[string]User),
		sessions: make(map[string]persistedSession),
		domains:  make(map[verifiedDomain]bool),
	}
}

func (f *fakeIdentity) SaveUser(_ context.Context, u User) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail != nil {
		return f.fail
	}
	f.users[u.ID] = u
	return nil
}

func (f *fakeIdentity) LoadUsers(context.Context) ([]User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []User
	for _, u := range f.users {
		out = append(out, u)
	}
	return out, nil
}

func (f *fakeIdentity) SaveSession(_ context.Context, s persistedSession) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail != nil {
		return f.fail
	}
	f.sessions[s.TokenHash] = s
	return nil
}

func (f *fakeIdentity) LoadSessions(_ context.Context, now time.Time) ([]persistedSession, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []persistedSession
	for _, s := range f.sessions {
		if now.Before(s.ExpiresAt) {
			out = append(out, s)
		}
	}
	return out, nil
}

func (f *fakeIdentity) SaveVerifiedDomain(_ context.Context, v verifiedDomain) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail != nil {
		return f.fail
	}
	f.domains[v] = true
	return nil
}

func (f *fakeIdentity) LoadVerifiedDomains(context.Context) ([]verifiedDomain, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []verifiedDomain
	for v := range f.domains {
		out = append(out, v)
	}
	return out, nil
}

// The bug this whole change exists for: a restart used to mint a brand-new
// user ID for every returning user, orphaning their Postgres history.
func TestUserIDIsStableAcrossFreshStores(t *testing.T) {
	a, _ := NewUserStore().FindOrCreate(42, "henry")
	b, _ := NewUserStore().FindOrCreate(42, "henry")
	if a.ID != b.ID {
		t.Fatalf("same GitHub account got IDs %q and %q from two fresh stores", a.ID, b.ID)
	}
}

func TestUserStoreSurvivesRestart(t *testing.T) {
	ctx := context.Background()
	p := newFakeIdentity()

	before, err := LoadUserStore(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	user, err := before.FindOrCreate(42, "henry")
	if err != nil {
		t.Fatal(err)
	}
	token, err := before.IssueSession(user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := before.SetWebhookURL(user.ID, "https://discord.com/api/webhooks/1/abc"); err != nil {
		t.Fatal(err)
	}

	after, err := LoadUserStore(ctx, p) // simulated coordinator restart
	if err != nil {
		t.Fatal(err)
	}
	got, ok := after.UserForSession(token)
	if !ok {
		t.Fatal("session issued before restart was not valid after it")
	}
	if got.ID != user.ID || got.GitHubLogin != "henry" {
		t.Fatalf("got user %+v after restart, want ID %q login henry", got, user.ID)
	}
	if got.WebhookURL != "https://discord.com/api/webhooks/1/abc" {
		t.Fatalf("webhook URL lost across restart: %q", got.WebhookURL)
	}
}

func TestSessionTokensArePersistedOnlyAsHashes(t *testing.T) {
	p := newFakeIdentity()
	s, _ := LoadUserStore(context.Background(), p)
	u, _ := s.FindOrCreate(1, "henry")
	token, _ := s.IssueSession(u.ID)

	if _, raw := p.sessions[token]; raw {
		t.Fatal("raw session token was persisted")
	}
	if _, hashed := p.sessions[hashSessionToken(token)]; !hashed {
		t.Fatal("hashed session token was not persisted")
	}
}

func TestSessionExpires(t *testing.T) {
	s := NewUserStore()
	now := time.Now()
	s.now = func() time.Time { return now }

	u, _ := s.FindOrCreate(1, "henry")
	token, _ := s.IssueSession(u.ID)
	if _, ok := s.UserForSession(token); !ok {
		t.Fatal("fresh session rejected")
	}

	now = now.Add(sessionTTL)
	if _, ok := s.UserForSession(token); ok {
		t.Fatal("session still valid at its expiry time")
	}
}

func TestExpiredSessionsAreNotReloaded(t *testing.T) {
	p := newFakeIdentity()
	p.users["gh-1"] = User{ID: "gh-1", GitHubID: 1, GitHubLogin: "henry"}
	p.sessions["stale"] = persistedSession{TokenHash: "stale", UserID: "gh-1", ExpiresAt: time.Now().Add(-time.Minute)}

	s, err := LoadUserStore(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.sessions) != 0 {
		t.Fatalf("expired session was loaded: %v", s.sessions)
	}
}

func TestIssueSessionFailsWhenPersistFails(t *testing.T) {
	p := newFakeIdentity()
	s, _ := LoadUserStore(context.Background(), p)
	u, _ := s.FindOrCreate(1, "henry")

	p.fail = errors.New("db down")
	if _, err := s.IssueSession(u.ID); err == nil {
		t.Fatal("expected an error when the session can't be persisted")
	}
	if len(s.sessions) != 0 {
		t.Fatal("an unpersisted session was still handed out from memory")
	}
}

func TestDomainVerificationSurvivesRestart(t *testing.T) {
	ctx := context.Background()
	p := newFakeIdentity()

	before, _ := LoadDomainStore(ctx, p)
	if err := before.MarkVerified("example.com", "gh-1"); err != nil {
		t.Fatal(err)
	}

	after, err := LoadDomainStore(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	if !after.IsVerified("example.com", "gh-1") {
		t.Fatal("verification lost across restart")
	}
	if after.IsVerified("example.com", "gh-2") {
		t.Fatal("reloaded verification leaked to a different user")
	}
}

func TestMarkVerifiedFailsClosedWhenPersistFails(t *testing.T) {
	p := newFakeIdentity()
	s, _ := LoadDomainStore(context.Background(), p)
	p.fail = errors.New("db down")

	if err := s.MarkVerified("example.com", "gh-1"); err == nil {
		t.Fatal("expected an error when verification can't be persisted")
	}
	if s.IsVerified("example.com", "gh-1") {
		t.Fatal("memory claims a verification that was never persisted")
	}
}

// TestPostgresIdentityRoundTrip runs the real SQL against a real database.
// Skipped unless TEST_POSTGRES_URL is set (CI sets it; locally:
// `docker compose up -d postgres` and
// TEST_POSTGRES_URL=postgres://sentryload:sentryload@localhost:5432/sentryload?sslmode=disable).
func TestPostgresIdentityRoundTrip(t *testing.T) {
	url := testPostgresURL(t)
	ctx := context.Background()
	h, err := newPostgresHistory(ctx, url) // runs migrate
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	resetIdentityTables(t, h.pool)

	users, err := LoadUserStore(ctx, h.identity())
	if err != nil {
		t.Fatal(err)
	}
	u, err := users.FindOrCreate(4242, "henry")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := users.FindOrCreate(4242, "henry-renamed"); err != nil {
		t.Fatal(err)
	}
	token, err := users.IssueSession(u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := users.SetWebhookURL(u.ID, "https://hooks.slack.com/services/x"); err != nil {
		t.Fatal(err)
	}
	domains, err := LoadDomainStore(ctx, h.identity())
	if err != nil {
		t.Fatal(err)
	}
	if err := domains.MarkVerified("example.com", u.ID); err != nil {
		t.Fatal(err)
	}
	if err := domains.MarkVerified("example.com", u.ID); err != nil { // re-verify is idempotent
		t.Fatal(err)
	}

	usersAfter, err := LoadUserStore(ctx, h.identity())
	if err != nil {
		t.Fatal(err)
	}
	got, ok := usersAfter.UserForSession(token)
	if !ok {
		t.Fatal("session not valid after reload from Postgres")
	}
	if got.GitHubLogin != "henry-renamed" || got.WebhookURL != "https://hooks.slack.com/services/x" {
		t.Fatalf("reloaded user = %+v", got)
	}
	domainsAfter, err := LoadDomainStore(ctx, h.identity())
	if err != nil {
		t.Fatal(err)
	}
	if !domainsAfter.IsVerified("example.com", u.ID) {
		t.Fatal("verification not reloaded from Postgres")
	}
}

func resetIdentityTables(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), `TRUNCATE users, sessions, verified_domains`); err != nil {
		t.Fatal(err)
	}
}
