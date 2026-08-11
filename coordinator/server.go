package main

import (
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// ServerConfig is everything NewServer needs to wire up the coordinator's
// HTTP API. A struct instead of a long parameter list since M8 pushed this
// past a dozen dependencies once GitHub OAuth joined domain verification
// and test submission.
type ServerConfig struct {
	Enqueuer   jobEnqueuer
	Tests      *TestStore
	Domains    *DomainStore
	Allowlist  map[string]bool
	Resolver   txtLookuper
	HTTPClient httpGetter

	Users             *UserStore
	GitHubClientID    string // empty disables the /auth/github/* routes
	GitHubRedirectURL string
	OAuthExchange     oauthExchanger
	GitHubUsers       githubUserFetcher
	DashboardURL      string // where handleGithubCallback redirects after login, and the allowed CORS origin (M10)

	TestCooldown time.Duration    // minimum spacing between one user's test submissions (M9); zero disables it
	History      testHistoryStore // nil disables /tests history persistence/listing (M10) — Postgres not configured
	Workers      workerCounter    // capacity-aware admission control (M12)
}

// NewServer wires the coordinator's HTTP API surface: GitHub login (M8),
// domain verification (M7), and test submission/status (pulled forward
// from M10 to give M7/M8 something to actually gate). Every route except
// /healthz and /auth/github/* requires a valid session from login.
func NewServer(cfg ServerConfig) http.Handler {
	s := &apiServer{
		enqueuer:          cfg.Enqueuer,
		tests:             cfg.Tests,
		domains:           cfg.Domains,
		allowlist:         cfg.Allowlist,
		resolver:          cfg.Resolver,
		httpClient:        cfg.HTTPClient,
		users:             cfg.Users,
		authStates:        newStateStore(),
		githubClientID:    cfg.GitHubClientID,
		githubRedirectURL: cfg.GitHubRedirectURL,
		oauthExchange:     cfg.OAuthExchange,
		githubUsers:       cfg.GitHubUsers,
		dashboardURL:      cfg.DashboardURL,
		testCooldown:      cfg.TestCooldown,
		history:           cfg.History,
		workers:           cfg.Workers,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	// Unauthenticated, standard practice for Prometheus scraping (M13) — no
	// sensitive data here, just request/latency counters and Go runtime
	// stats (goroutines, GC, memory).
	mux.Handle("GET /metrics", promhttp.Handler())
	mux.HandleFunc("GET /auth/github/login", s.handleGithubLogin)
	mux.HandleFunc("GET /auth/github/callback", s.handleGithubCallback)
	mux.HandleFunc("POST /domains", s.requireAuth(s.handleCreateDomainChallenge))
	mux.HandleFunc("POST /domains/{domain}/verify", s.requireAuth(s.handleVerifyDomain))
	mux.HandleFunc("POST /tests", s.requireAuth(s.handleCreateTest))
	mux.HandleFunc("GET /tests", s.requireAuth(s.handleListTests))
	// Registered before /tests/{id} — Go's ServeMux prefers the more
	// specific literal segment over the wildcard for the same position,
	// but keeping the literal route physically first here too for anyone
	// reading top-to-bottom rather than relying purely on that.
	mux.HandleFunc("GET /tests/trend", s.requireAuth(s.handleTestTrend))
	mux.HandleFunc("GET /tests/{id}", s.requireAuth(s.handleGetTest))
	mux.HandleFunc("GET /me/webhook", s.requireAuth(s.handleGetWebhook))
	mux.HandleFunc("PUT /me/webhook", s.requireAuth(s.handleSetWebhook))
	mux.HandleFunc("POST /tests/{id}/share", s.requireAuth(s.handleShareTest))
	// Deliberately not wrapped in requireAuth — a share link's whole point
	// is that anyone holding it can view the report without logging in.
	mux.HandleFunc("GET /reports/{token}", s.handlePublicReport)
	// Also unauthenticated, same reasoning — meant to be embedded as an
	// <img src="..."> in a README, which can't attach a bearer token.
	mux.HandleFunc("GET /badge/{token}", s.handleBadge)
	// Not wrapped in requireAuth: browsers can't set custom headers on a
	// WebSocket handshake, so this route does its own query-param-based
	// auth instead (see live.go).
	mux.HandleFunc("GET /tests/{id}/live", s.handleTestLive)

	return metricsMiddleware(withCORS(cfg.DashboardURL, mux))
}

// withCORS lets the dashboard (a different origin — localhost:3000 vs the
// coordinator's :8080, and a different domain entirely once deployed) call
// this API from the browser. Auth is a bearer token, not a cookie, so
// there's no credentialed-request/CSRF concern in allowing it; the
// WebSocket route isn't affected by this at all (CORS doesn't apply to the
// WS handshake, see live.go's CheckOrigin instead).
func withCORS(allowedOrigin string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if allowedOrigin != "" {
			w.Header().Set("Access-Control-Allow-Origin", allowedOrigin)
			w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, ngrok-skip-browser-warning")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
