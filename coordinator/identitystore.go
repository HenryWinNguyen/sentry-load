package main

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// identityPersister is the durable backing for UserStore and DomainStore:
// users (and their webhook settings), sessions, and per-user domain
// verifications. Both stores keep serving reads from memory and write
// through to this on every change, loading it all back on startup — so a
// coordinator restart no longer logs everyone out, forgets their webhook,
// and forces them to re-verify every domain. Extracted as an interface for
// the same reason testHistoryStore is: unit tests use a fake.
type identityPersister interface {
	SaveUser(ctx context.Context, u User) error
	LoadUsers(ctx context.Context) ([]User, error)

	SaveSession(ctx context.Context, s persistedSession) error
	// LoadSessions returns only sessions still valid at now — expired rows
	// are dropped on load rather than by a background sweeper.
	LoadSessions(ctx context.Context, now time.Time) ([]persistedSession, error)

	SaveVerifiedDomain(ctx context.Context, v verifiedDomain) error
	LoadVerifiedDomains(ctx context.Context) ([]verifiedDomain, error)
}

type persistedSession struct {
	TokenHash string // hashSessionToken(token), never the raw token
	UserID    string
	ExpiresAt time.Time
}

type verifiedDomain struct {
	Domain  string
	OwnerID string
}

// postgresIdentity is the production identityPersister. It shares the
// history store's connection pool — one database, one pool.
type postgresIdentity struct {
	pool *pgxpool.Pool
}

func (p *postgresIdentity) SaveUser(ctx context.Context, u User) error {
	_, err := p.pool.Exec(ctx, `
		INSERT INTO users (id, github_id, github_login, webhook_url)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (id) DO UPDATE SET
			github_login = EXCLUDED.github_login,
			webhook_url  = EXCLUDED.webhook_url
	`, u.ID, u.GitHubID, u.GitHubLogin, u.WebhookURL)
	return err
}

func (p *postgresIdentity) LoadUsers(ctx context.Context) ([]User, error) {
	rows, err := p.pool.Query(ctx, `SELECT id, github_id, github_login, webhook_url FROM users`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var users []User
	for rows.Next() {
		var u User
		if err := rows.Scan(&u.ID, &u.GitHubID, &u.GitHubLogin, &u.WebhookURL); err != nil {
			return nil, fmt.Errorf("scanning user row: %w", err)
		}
		users = append(users, u)
	}
	return users, rows.Err()
}

func (p *postgresIdentity) SaveSession(ctx context.Context, s persistedSession) error {
	_, err := p.pool.Exec(ctx, `
		INSERT INTO sessions (token_hash, user_id, expires_at) VALUES ($1, $2, $3)
	`, s.TokenHash, s.UserID, s.ExpiresAt)
	return err
}

func (p *postgresIdentity) LoadSessions(ctx context.Context, now time.Time) ([]persistedSession, error) {
	// Expired rows are useless from here on; clearing them on each startup
	// keeps the table from growing forever without a separate cleanup job.
	if _, err := p.pool.Exec(ctx, `DELETE FROM sessions WHERE expires_at <= $1`, now); err != nil {
		return nil, fmt.Errorf("pruning expired sessions: %w", err)
	}
	rows, err := p.pool.Query(ctx, `SELECT token_hash, user_id, expires_at FROM sessions`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var sessions []persistedSession
	for rows.Next() {
		var s persistedSession
		if err := rows.Scan(&s.TokenHash, &s.UserID, &s.ExpiresAt); err != nil {
			return nil, fmt.Errorf("scanning session row: %w", err)
		}
		sessions = append(sessions, s)
	}
	return sessions, rows.Err()
}

func (p *postgresIdentity) SaveVerifiedDomain(ctx context.Context, v verifiedDomain) error {
	_, err := p.pool.Exec(ctx, `
		INSERT INTO verified_domains (domain, owner_id, verified_at) VALUES ($1, $2, now())
		ON CONFLICT (domain, owner_id) DO UPDATE SET verified_at = now()
	`, v.Domain, v.OwnerID)
	return err
}

func (p *postgresIdentity) LoadVerifiedDomains(ctx context.Context) ([]verifiedDomain, error) {
	rows, err := p.pool.Query(ctx, `SELECT domain, owner_id FROM verified_domains`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []verifiedDomain
	for rows.Next() {
		var v verifiedDomain
		if err := rows.Scan(&v.Domain, &v.OwnerID); err != nil {
			return nil, fmt.Errorf("scanning verified domain row: %w", err)
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
