// Command coordinator runs the always-on HTTP API: domain-ownership
// verification (M7) and load-test submission/status, fanned out across
// WORKER_COUNT sub-jobs sharing one test ID. No auth yet (M8), no
// persisted history (M10) — see SCOPE.md.
package main

import (
	"context"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/redis/go-redis/v9"
)

const defaultTestCooldown = 30 * time.Second

func main() {
	addr := os.Getenv("REDIS_ADDR")
	if addr == "" {
		addr = "localhost:6379"
	}

	rdb := redis.NewClient(&redis.Options{Addr: addr})
	pingCtx, cancelPing := context.WithTimeout(context.Background(), 5*time.Second)
	if err := rdb.Ping(pingCtx).Err(); err != nil {
		log.Fatalf("could not reach redis at %s: %v", addr, err)
	}
	cancelPing()

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	tests := NewTestStore()
	domains := NewDomainStore()
	users := NewUserStore()
	enqueuer := &redisEnqueuer{rdb: rdb}
	allowlist := parseAllowlist(os.Getenv("ALLOWLISTED_HOSTS"))

	githubClientID := os.Getenv("GITHUB_CLIENT_ID")
	githubClientSecret := os.Getenv("GITHUB_CLIENT_SECRET")
	githubRedirectURL := os.Getenv("GITHUB_REDIRECT_URL")
	if githubRedirectURL == "" {
		githubRedirectURL = "http://localhost:" + port + "/auth/github/callback"
	}
	if githubClientID == "" || githubClientSecret == "" {
		log.Print("GITHUB_CLIENT_ID/GITHUB_CLIENT_SECRET not set — /auth/github/* routes disabled, rest of the API still works")
	}
	githubClient := &githubOAuthClient{
		clientID:     githubClientID,
		clientSecret: githubClientSecret,
		redirectURL:  githubRedirectURL,
		httpClient:   http.DefaultClient,
	}

	testCooldown := defaultTestCooldown
	if v := os.Getenv("TEST_COOLDOWN_SECONDS"); v != "" {
		secs, err := strconv.Atoi(v)
		if err != nil || secs < 0 {
			log.Fatalf("invalid TEST_COOLDOWN_SECONDS %q: must be a non-negative integer", v)
		}
		testCooldown = time.Duration(secs) * time.Second
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var history testHistoryStore
	if url := os.Getenv("POSTGRES_URL"); url != "" {
		h, err := newPostgresHistory(ctx, url)
		if err != nil {
			log.Fatalf("failed to connect to postgres: %v", err)
		}
		defer h.Close()
		history = h
	} else {
		log.Print("POSTGRES_URL not set — test history (GET /tests) disabled, rest of the API still works")
	}

	go watchResults(ctx, rdb, tests, history)

	handler := NewServer(ServerConfig{
		Enqueuer:          enqueuer,
		Tests:             tests,
		Domains:           domains,
		Allowlist:         allowlist,
		Resolver:          net.DefaultResolver,
		HTTPClient:        http.DefaultClient,
		Users:             users,
		GitHubClientID:    githubClientID,
		GitHubRedirectURL: githubRedirectURL,
		OAuthExchange:     githubClient,
		GitHubUsers:       githubClient,
		TestCooldown:      testCooldown,
		History:           history,
	})
	srv := &http.Server{Addr: ":" + port, Handler: handler}

	go func() {
		<-ctx.Done()
		log.Print("shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			log.Printf("graceful shutdown failed: %v", err)
		}
	}()

	log.Printf("coordinator listening on :%s (redis %s)", port, addr)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("server error: %v", err)
	}
}

// parseAllowlist builds the set of hosts a test can target without domain
// verification: V1's fixed guinea-pig set, expressed as a comma-separated
// env var so it's configurable per deploy, plus localhost for local dev.
func parseAllowlist(v string) map[string]bool {
	hosts := map[string]bool{"localhost": true, "127.0.0.1": true}
	for _, h := range strings.Split(v, ",") {
		h = strings.TrimSpace(h)
		if h != "" {
			hosts[h] = true
		}
	}
	return hosts
}
