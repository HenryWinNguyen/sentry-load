package main

import "net/http"

// NewServer wires the coordinator's HTTP API surface: domain verification
// (M7) and test submission/status (job-submission API, pulled forward from
// M10 to give M7/M8 something to actually gate). No auth yet — that's M8.
func NewServer(enqueuer jobEnqueuer, tests *TestStore, domains *DomainStore, allowlist map[string]bool, resolver txtLookuper, httpClient httpGetter) http.Handler {
	s := &apiServer{
		enqueuer:   enqueuer,
		tests:      tests,
		domains:    domains,
		allowlist:  allowlist,
		resolver:   resolver,
		httpClient: httpClient,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	mux.HandleFunc("POST /domains", s.handleCreateDomainChallenge)
	mux.HandleFunc("POST /domains/{domain}/verify", s.handleVerifyDomain)
	mux.HandleFunc("POST /tests", s.handleCreateTest)
	mux.HandleFunc("GET /tests/{id}", s.handleGetTest)
	return mux
}
