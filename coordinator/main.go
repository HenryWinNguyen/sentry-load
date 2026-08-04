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
	"strings"
	"syscall"
	"time"

	"github.com/redis/go-redis/v9"
)

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
	enqueuer := &redisEnqueuer{rdb: rdb}
	allowlist := parseAllowlist(os.Getenv("ALLOWLISTED_HOSTS"))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go watchResults(ctx, rdb, tests)

	handler := NewServer(enqueuer, tests, domains, allowlist, net.DefaultResolver, http.DefaultClient)
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
