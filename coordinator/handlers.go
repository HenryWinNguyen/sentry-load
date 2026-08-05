package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// jobEnqueuer is the subset of enqueueing behavior handlers depend on,
// extracted as an interface so handler tests can inject a fake instead of
// hitting real Redis — same pattern as verify.go's txtLookuper/httpGetter.
type jobEnqueuer interface {
	Enqueue(ctx context.Context, testURL, rampPattern string, totalVUs, durationSeconds, fanout int) (testID string, subJobIDs []string, err error)
}

type apiServer struct {
	enqueuer   jobEnqueuer
	tests      *TestStore
	domains    *DomainStore
	allowlist  map[string]bool
	resolver   txtLookuper
	httpClient httpGetter

	users             *UserStore
	authStates        *stateStore
	githubClientID    string
	githubRedirectURL string
	oauthExchange     oauthExchanger
	githubUsers       githubUserFetcher

	testCooldown time.Duration
	history      testHistoryStore // nil if Postgres isn't configured (M10)
}

type errorResponse struct {
	Error string `json:"error"`
}

type contextKey int

const userContextKey contextKey = iota

func userFromContext(ctx context.Context) (*User, bool) {
	u, ok := ctx.Value(userContextKey).(*User)
	return u, ok
}

// requireAuth wraps a handler so it only runs for requests carrying a valid
// `Authorization: Bearer <token>` header from a completed GitHub login
// (M8), making the authenticated user available via userFromContext.
func (s *apiServer) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok || token == "" {
			writeError(w, http.StatusUnauthorized, "missing or malformed Authorization header")
			return
		}
		user, ok := s.users.UserForSession(token)
		if !ok {
			writeError(w, http.StatusUnauthorized, "invalid or expired token")
			return
		}
		next(w, r.WithContext(context.WithValue(r.Context(), userContextKey, user)))
	}
}

// handleGithubLogin starts the GitHub OAuth flow by redirecting to GitHub's
// consent screen with a fresh single-use CSRF state token.
func (s *apiServer) handleGithubLogin(w http.ResponseWriter, r *http.Request) {
	if s.githubClientID == "" {
		writeError(w, http.StatusNotImplemented, "GitHub OAuth is not configured on this server")
		return
	}
	state, err := s.authStates.Issue()
	if err != nil {
		log.Printf("failed to issue oauth state: %v", err)
		writeError(w, http.StatusInternalServerError, "failed to start login")
		return
	}
	q := url.Values{
		"client_id":    {s.githubClientID},
		"redirect_uri": {s.githubRedirectURL},
		"state":        {state},
	}
	http.Redirect(w, r, githubAuthorizeURL+"?"+q.Encode(), http.StatusFound)
}

type loginResponse struct {
	Token       string `json:"token"`
	GitHubLogin string `json:"github_login"`
}

// handleGithubCallback completes the OAuth flow: validates state, exchanges
// the code for a GitHub access token, resolves the GitHub identity behind
// it, finds-or-creates the corresponding user, and issues a coordinator
// session token for the caller to use as a bearer token on every other
// endpoint.
func (s *apiServer) handleGithubCallback(w http.ResponseWriter, r *http.Request) {
	if s.githubClientID == "" {
		writeError(w, http.StatusNotImplemented, "GitHub OAuth is not configured on this server")
		return
	}

	code := r.URL.Query().Get("code")
	state := r.URL.Query().Get("state")
	if code == "" || state == "" {
		writeError(w, http.StatusBadRequest, "missing code or state")
		return
	}
	if !s.authStates.Consume(state) {
		writeError(w, http.StatusBadRequest, "invalid or expired state")
		return
	}

	accessToken, err := s.oauthExchange.Exchange(r.Context(), code)
	if err != nil {
		log.Printf("github token exchange failed: %v", err)
		writeError(w, http.StatusBadGateway, "failed to complete GitHub login")
		return
	}

	githubID, githubLogin, err := s.githubUsers.FetchUser(r.Context(), accessToken)
	if err != nil {
		log.Printf("github user fetch failed: %v", err)
		writeError(w, http.StatusBadGateway, "failed to complete GitHub login")
		return
	}

	user, err := s.users.FindOrCreate(githubID, githubLogin)
	if err != nil {
		log.Printf("failed to create user: %v", err)
		writeError(w, http.StatusInternalServerError, "failed to complete GitHub login")
		return
	}
	token, err := s.users.IssueSession(user.ID)
	if err != nil {
		log.Printf("failed to issue session: %v", err)
		writeError(w, http.StatusInternalServerError, "failed to complete GitHub login")
		return
	}

	writeJSON(w, http.StatusOK, loginResponse{Token: token, GitHubLogin: user.GitHubLogin})
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, errorResponse{Error: msg})
}

type createDomainRequest struct {
	Domain string `json:"domain"`
}

type createDomainResponse struct {
	Domain       string `json:"domain"`
	Token        string `json:"token"`
	DNSRecord    string `json:"dns_record"`
	DNSValue     string `json:"dns_value"`
	WellKnownURL string `json:"well_known_url"`
}

// handleCreateDomainChallenge issues a fresh verification token for a
// domain and returns both proof options (SCOPE.md M7) — the caller picks
// whichever is easier for them to set up.
func (s *apiServer) handleCreateDomainChallenge(w http.ResponseWriter, r *http.Request) {
	var req createDomainRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	domain := strings.TrimSpace(req.Domain)
	if domain == "" {
		writeError(w, http.StatusBadRequest, "domain is required")
		return
	}

	token, err := s.domains.IssueChallenge(domain)
	if err != nil {
		log.Printf("failed to issue challenge for %s: %v", domain, err)
		writeError(w, http.StatusInternalServerError, "failed to issue challenge")
		return
	}

	writeJSON(w, http.StatusOK, createDomainResponse{
		Domain:       domain,
		Token:        token,
		DNSRecord:    verifyTXTPrefix + domain,
		DNSValue:     token,
		WellKnownURL: "https://" + domain + verifyWellKnownPath,
	})
}

type verifyDomainRequest struct {
	Method string `json:"method"` // "dns" or "well-known"
}

type verifyDomainResponse struct {
	Domain   string `json:"domain"`
	Verified bool   `json:"verified"`
}

// handleVerifyDomain checks the outstanding challenge for a domain against
// whichever proof method the caller picked, and marks the domain verified
// on success.
func (s *apiServer) handleVerifyDomain(w http.ResponseWriter, r *http.Request) {
	domain := r.PathValue("domain")
	if domain == "" {
		writeError(w, http.StatusBadRequest, "domain is required")
		return
	}
	var req verifyDomainRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	token, issued := s.domains.Challenge(domain)
	if !issued {
		writeError(w, http.StatusNotFound, "no outstanding challenge for this domain; POST /domains first")
		return
	}

	var (
		verified bool
		err      error
	)
	switch req.Method {
	case "dns":
		verified, err = VerifyDomainTXT(r.Context(), s.resolver, domain, token)
	case "well-known":
		verified, err = VerifyDomainWellKnown(s.httpClient, domain, token)
	default:
		writeError(w, http.StatusBadRequest, `method must be "dns" or "well-known"`)
		return
	}
	// A lookup error here (NXDOMAIN, connection refused, etc.) reads to the
	// caller exactly like "the record isn't there yet" — the well-known
	// method already folds its equivalent case (a 404) into verified=false
	// without an error, so DNS does the same instead of forcing callers to
	// parse DNS error taxonomy just to know whether to try again.
	if err != nil {
		log.Printf("verification check for %s failed, treating as unverified: %v", domain, err)
		verified = false
	}
	if verified {
		s.domains.MarkVerified(domain)
	}

	writeJSON(w, http.StatusOK, verifyDomainResponse{Domain: domain, Verified: verified})
}

type createTestRequest struct {
	URL             string `json:"url"`
	VUs             int    `json:"vus"`
	DurationSeconds int    `json:"duration_seconds"`
	RampPattern     string `json:"ramp_pattern"`
	WorkerCount     int    `json:"worker_count"`
}

type createTestResponse struct {
	TestID    string   `json:"test_id"`
	SubJobIDs []string `json:"sub_job_ids"`
}

// handleCreateTest validates and enqueues a new test, gated on the target
// domain being either pre-allowlisted (V1's fixed guinea-pig set, plus
// localhost for dev) or independently verified (M7). This is the line
// between a load tester and a DDoS-as-a-service tool (SCOPE.md) — it's
// enforced here, not left to the worker.
func (s *apiServer) handleCreateTest(w http.ResponseWriter, r *http.Request) {
	user, ok := userFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}

	if remaining := s.tests.CooldownRemaining(user.ID, s.testCooldown); remaining > 0 {
		w.Header().Set("Retry-After", strconv.Itoa(int(remaining.Seconds())+1))
		writeError(w, http.StatusTooManyRequests, fmt.Sprintf("submitting too fast; wait %s before your next test", remaining.Round(time.Second)))
		return
	}

	var req createTestRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	parsed, err := url.Parse(req.URL)
	if err != nil || parsed.Hostname() == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		writeError(w, http.StatusBadRequest, "url must be a valid absolute http(s) URL")
		return
	}
	host := parsed.Hostname()

	if !s.allowlist[host] && !s.domains.IsVerified(host) {
		writeError(w, http.StatusForbidden, fmt.Sprintf("domain %q is not verified; POST /domains to start verification", host))
		return
	}

	if req.VUs <= 0 || req.VUs > maxVUsPerTest {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("vus must be between 1 and %d", maxVUsPerTest))
		return
	}
	if req.DurationSeconds <= 0 || req.DurationSeconds > maxDurationSeconds {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("duration_seconds must be between 1 and %d", maxDurationSeconds))
		return
	}
	switch req.RampPattern {
	case "steady", "ramp":
	default:
		writeError(w, http.StatusBadRequest, `ramp_pattern must be "steady" or "ramp"`)
		return
	}

	fanout := req.WorkerCount
	if fanout <= 0 {
		fanout = 1
	}
	if fanout > maxFanout {
		fanout = maxFanout
	}
	if fanout > req.VUs {
		fanout = req.VUs // never split into a zero-VU sub-job
	}

	testID, subJobIDs, err := s.enqueuer.Enqueue(r.Context(), req.URL, req.RampPattern, req.VUs, req.DurationSeconds, fanout)
	if err != nil {
		log.Printf("failed to enqueue test: %v", err)
		writeError(w, http.StatusInternalServerError, "failed to enqueue test")
		return
	}
	s.tests.Register(testID, user.ID, req.URL, subJobIDs)

	writeJSON(w, http.StatusAccepted, createTestResponse{TestID: testID, SubJobIDs: subJobIDs})
}

// handleGetTest returns the current merged status of a previously
// submitted test — poll this until done=true, or use GET /tests/{id}/live
// for push updates instead. Scoped to the caller: a test belonging to
// someone else 404s exactly like an unknown ID would.
//
// Checks the in-memory TestStore first (covers anything still running or
// recently finished), falling back to Postgres (M10) for a test that
// finished before a restart — if Postgres isn't configured, only the
// in-memory view is available.
func (s *apiServer) handleGetTest(w http.ResponseWriter, r *http.Request) {
	user, ok := userFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}

	id := r.PathValue("id")
	if snap, ok := s.tests.Snapshot(id, user.ID); ok {
		writeJSON(w, http.StatusOK, snap)
		return
	}

	if s.history != nil {
		snap, ok, err := s.history.Get(r.Context(), id, user.ID)
		if err != nil {
			log.Printf("failed to load test %s from history: %v", id, err)
			writeError(w, http.StatusInternalServerError, "failed to load test")
			return
		}
		if ok {
			writeJSON(w, http.StatusOK, snap)
			return
		}
	}

	writeError(w, http.StatusNotFound, "unknown test id")
}

const testHistoryListLimit = 50

// handleListTests returns the caller's most recent finished tests, newest
// first. Requires Postgres (M10) — there's no in-memory equivalent to fall
// back to, since TestStore doesn't keep a bounded history, just whatever's
// currently tracked.
func (s *apiServer) handleListTests(w http.ResponseWriter, r *http.Request) {
	user, ok := userFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	if s.history == nil {
		writeError(w, http.StatusNotImplemented, "test history is not available (Postgres not configured on this server)")
		return
	}

	snaps, err := s.history.List(r.Context(), user.ID, testHistoryListLimit)
	if err != nil {
		log.Printf("failed to list test history for %s: %v", user.ID, err)
		writeError(w, http.StatusInternalServerError, "failed to load test history")
		return
	}
	if snaps == nil {
		snaps = []TestSnapshot{}
	}
	writeJSON(w, http.StatusOK, snaps)
}
