package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"
)

type fakeEnqueuer struct {
	testID    string
	subJobIDs []string
	err       error

	calls        int
	lastURL      string
	lastRamp     string
	lastVUs      int
	lastDuration int
	lastFanout   int
}

func (f *fakeEnqueuer) Enqueue(_ context.Context, testURL, rampPattern string, totalVUs, durationSeconds, fanout int) (string, []string, error) {
	f.calls++
	f.lastURL, f.lastRamp = testURL, rampPattern
	f.lastVUs, f.lastDuration, f.lastFanout = totalVUs, durationSeconds, fanout
	if f.err != nil {
		return "", nil, f.err
	}
	return f.testID, f.subJobIDs, nil
}

type fakeOAuthExchanger struct {
	accessToken string
	err         error
}

func (f *fakeOAuthExchanger) Exchange(_ context.Context, _ string) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	return f.accessToken, nil
}

type fakeGitHubUserFetcher struct {
	githubID    int64
	githubLogin string
	err         error
}

func (f *fakeGitHubUserFetcher) FetchUser(_ context.Context, _ string) (int64, string, error) {
	if f.err != nil {
		return 0, "", f.err
	}
	return f.githubID, f.githubLogin, nil
}

// fakeHistoryStore is an in-memory stand-in for postgresHistory, so
// handler tests can exercise the history-backed paths (GET /tests,
// GET /tests/{id}'s Postgres fallback) without a real database.
type fakeHistoryStore struct {
	mu          sync.Mutex
	byID        map[string]TestSnapshot // testID -> snapshot
	owner       map[string]string       // testID -> ownerID
	shareTokens map[string]string       // testID -> share token
	err         error
}

func newFakeHistoryStore() *fakeHistoryStore {
	return &fakeHistoryStore{
		byID:        make(map[string]TestSnapshot),
		owner:       make(map[string]string),
		shareTokens: make(map[string]string),
	}
}

func (f *fakeHistoryStore) Save(_ context.Context, snap TestSnapshot, ownerID string) error {
	if f.err != nil {
		return f.err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.byID[snap.TestID] = snap
	f.owner[snap.TestID] = ownerID
	return nil
}

func (f *fakeHistoryStore) Get(_ context.Context, testID, ownerID string) (TestSnapshot, bool, error) {
	if f.err != nil {
		return TestSnapshot{}, false, f.err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	snap, ok := f.byID[testID]
	if !ok || f.owner[testID] != ownerID {
		return TestSnapshot{}, false, nil
	}
	return snap, true, nil
}

func (f *fakeHistoryStore) List(_ context.Context, ownerID string, limit int) ([]TestSnapshot, error) {
	if f.err != nil {
		return nil, f.err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []TestSnapshot
	for testID, snap := range f.byID {
		if f.owner[testID] == ownerID {
			out = append(out, snap)
		}
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (f *fakeHistoryStore) EnsureShareToken(_ context.Context, testID, ownerID string) (string, bool, error) {
	if f.err != nil {
		return "", false, f.err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.owner[testID] != ownerID {
		return "", false, nil
	}
	if token, ok := f.shareTokens[testID]; ok {
		return token, true, nil
	}
	token := "share-" + testID
	f.shareTokens[testID] = token
	return token, true, nil
}

func (f *fakeHistoryStore) GetByShareToken(_ context.Context, shareToken string) (TestSnapshot, bool, error) {
	if f.err != nil {
		return TestSnapshot{}, false, f.err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for testID, token := range f.shareTokens {
		if token == shareToken {
			return f.byID[testID], true, nil
		}
	}
	return TestSnapshot{}, false, nil
}

// testServer bundles a running httptest server with the stores it was
// built on, so tests can both drive it over HTTP and inspect state
// directly.
type testServer struct {
	*httptest.Server
	domains *DomainStore
	tests   *TestStore
	users   *UserStore
	history *fakeHistoryStore // nil unless built with newTestServerWithHistory
}

// newTestServer wires a fresh server with the given enqueuer/resolver/http
// client, a default (non-erroring) fake GitHub OAuth backend, and an
// allowlist containing only "allowed.example.com".
func newTestServer(t *testing.T, enqueuer jobEnqueuer, resolver txtLookuper, httpClient httpGetter) *testServer {
	t.Helper()
	return newTestServerWithOAuth(t, enqueuer, resolver, httpClient, &fakeOAuthExchanger{accessToken: "gh-token"}, &fakeGitHubUserFetcher{githubID: 1, githubLogin: "henry"})
}

// newTestServerWithCooldown is like newTestServer but with a configurable
// per-user test-submission cooldown (M9), for tests that need to observe
// the 429 path without waiting out the real default.
func newTestServerWithCooldown(t *testing.T, enqueuer jobEnqueuer, cooldown time.Duration) *testServer {
	t.Helper()
	domains := NewDomainStore()
	tests := NewTestStore()
	users := NewUserStore()
	handler := NewServer(ServerConfig{
		Enqueuer:          enqueuer,
		Tests:             tests,
		Domains:           domains,
		Allowlist:         map[string]bool{"allowed.example.com": true},
		Resolver:          fakeResolver{},
		HTTPClient:        http.DefaultClient,
		Users:             users,
		GitHubClientID:    "test-client-id",
		GitHubRedirectURL: "http://localhost/auth/github/callback",
		OAuthExchange:     &fakeOAuthExchanger{accessToken: "gh-token"},
		GitHubUsers:       &fakeGitHubUserFetcher{githubID: 1, githubLogin: "henry"},
		TestCooldown:      cooldown,
	})
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return &testServer{Server: server, domains: domains, tests: tests, users: users}
}

// newTestServerWithHistory is like newTestServer but with a fake Postgres
// history store wired in (M10), for tests of GET /tests and the
// GET /tests/{id} Postgres fallback.
func newTestServerWithHistory(t *testing.T, enqueuer jobEnqueuer) *testServer {
	t.Helper()
	domains := NewDomainStore()
	tests := NewTestStore()
	users := NewUserStore()
	history := newFakeHistoryStore()
	handler := NewServer(ServerConfig{
		Enqueuer:          enqueuer,
		Tests:             tests,
		Domains:           domains,
		Allowlist:         map[string]bool{"allowed.example.com": true},
		Resolver:          fakeResolver{},
		HTTPClient:        http.DefaultClient,
		Users:             users,
		GitHubClientID:    "test-client-id",
		GitHubRedirectURL: "http://localhost/auth/github/callback",
		OAuthExchange:     &fakeOAuthExchanger{accessToken: "gh-token"},
		GitHubUsers:       &fakeGitHubUserFetcher{githubID: 1, githubLogin: "henry"},
		History:           history,
	})
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return &testServer{Server: server, domains: domains, tests: tests, users: users, history: history}
}

func newTestServerWithOAuth(t *testing.T, enqueuer jobEnqueuer, resolver txtLookuper, httpClient httpGetter, exchanger oauthExchanger, fetcher githubUserFetcher) *testServer {
	t.Helper()
	domains := NewDomainStore()
	tests := NewTestStore()
	users := NewUserStore()
	allowlist := map[string]bool{"allowed.example.com": true}
	handler := NewServer(ServerConfig{
		Enqueuer:          enqueuer,
		Tests:             tests,
		Domains:           domains,
		Allowlist:         allowlist,
		Resolver:          resolver,
		HTTPClient:        httpClient,
		Users:             users,
		GitHubClientID:    "test-client-id",
		GitHubRedirectURL: "http://localhost/auth/github/callback",
		OAuthExchange:     exchanger,
		GitHubUsers:       fetcher,
	})
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return &testServer{Server: server, domains: domains, tests: tests, users: users}
}

// login logs in a fresh user directly against the UserStore (bypassing the
// real OAuth handshake, which handleGithubLogin/Callback have their own
// dedicated tests for) and returns a bearer token for it.
func (ts *testServer) login(t *testing.T) string {
	t.Helper()
	user, err := ts.users.FindOrCreate(1, "henry")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	token, err := ts.users.IssueSession(user.ID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	return token
}

func authedRequest(t *testing.T, method, url, token string, body interface{}) *http.Response {
	t.Helper()
	var r *bytes.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshaling request body: %v", err)
		}
		r = bytes.NewReader(b)
	} else {
		r = bytes.NewReader(nil)
	}
	req, err := http.NewRequest(method, url, r)
	if err != nil {
		t.Fatalf("building request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func TestAuthRequiredOnProtectedRoutes(t *testing.T) {
	server := newTestServer(t, &fakeEnqueuer{}, fakeResolver{}, http.DefaultClient)

	cases := []struct {
		method, path string
	}{
		{http.MethodPost, "/domains"},
		{http.MethodPost, "/domains/example.com/verify"},
		{http.MethodPost, "/tests"},
		{http.MethodGet, "/tests/some-id"},
	}
	for _, tc := range cases {
		resp := authedRequest(t, tc.method, server.URL+tc.path, "", createDomainRequest{})
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s %s with no token: got status %d, want 401", tc.method, tc.path, resp.StatusCode)
		}

		resp2 := authedRequest(t, tc.method, server.URL+tc.path, "not-a-real-token", createDomainRequest{})
		if resp2.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s %s with a bogus token: got status %d, want 401", tc.method, tc.path, resp2.StatusCode)
		}
	}
}

func TestHandleGithubLoginRedirects(t *testing.T) {
	server := newTestServer(t, &fakeEnqueuer{}, fakeResolver{}, http.DefaultClient)
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

	resp, err := client.Get(server.URL + "/auth/github/login")
	if err != nil {
		t.Fatalf("GET failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusFound {
		t.Fatalf("got status %d, want 302", resp.StatusCode)
	}
	loc, err := url.Parse(resp.Header.Get("Location"))
	if err != nil {
		t.Fatalf("parsing Location header: %v", err)
	}
	if loc.Scheme+"://"+loc.Host+loc.Path != githubAuthorizeURL {
		t.Fatalf("got redirect to %q, want %q", loc.String(), githubAuthorizeURL)
	}
	if loc.Query().Get("client_id") != "test-client-id" {
		t.Fatalf("got client_id %q, want test-client-id", loc.Query().Get("client_id"))
	}
	if loc.Query().Get("state") == "" {
		t.Fatal("expected a non-empty state parameter")
	}
}

func TestHandleGithubLoginDisabledWithoutClientID(t *testing.T) {
	// GITHUB_CLIENT_ID unset simulates OAuth not being configured on this
	// deploy — the rest of the API should still work (see main.go), just
	// not the login routes.
	handler := NewServer(ServerConfig{
		Enqueuer:   &fakeEnqueuer{},
		Tests:      NewTestStore(),
		Domains:    NewDomainStore(),
		Allowlist:  map[string]bool{},
		Resolver:   fakeResolver{},
		HTTPClient: http.DefaultClient,
		Users:      NewUserStore(),
		// GitHubClientID intentionally left empty.
		OAuthExchange: &fakeOAuthExchanger{},
		GitHubUsers:   &fakeGitHubUserFetcher{},
	})
	unconfigured := httptest.NewServer(handler)
	defer unconfigured.Close()

	resp, err := http.Get(unconfigured.URL + "/auth/github/login")
	if err != nil {
		t.Fatalf("GET failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotImplemented {
		t.Fatalf("got status %d, want 501", resp.StatusCode)
	}
}

func TestHandleGithubCallbackFullFlow(t *testing.T) {
	server := newTestServer(t, &fakeEnqueuer{}, fakeResolver{}, http.DefaultClient)
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

	loginResp, err := client.Get(server.URL + "/auth/github/login")
	if err != nil {
		t.Fatalf("login GET failed: %v", err)
	}
	loc, _ := url.Parse(loginResp.Header.Get("Location"))
	loginResp.Body.Close()
	state := loc.Query().Get("state")

	callbackURL := server.URL + "/auth/github/callback?code=some-code&state=" + state
	resp, err := client.Get(callbackURL)
	if err != nil {
		t.Fatalf("callback GET failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("got status %d, want 302 (redirect to the dashboard)", resp.StatusCode)
	}

	dest, err := url.Parse(resp.Header.Get("Location"))
	if err != nil {
		t.Fatalf("parsing Location header: %v", err)
	}
	token := dest.Query().Get("token")
	if token == "" {
		t.Fatal("expected a non-empty token in the redirect")
	}
	if got := dest.Query().Get("github_login"); got != "henry" {
		t.Fatalf("got github_login %q, want henry", got)
	}

	// The issued token should actually work against a protected route.
	testResp := authedRequest(t, http.MethodPost, server.URL+"/domains", token, createDomainRequest{Domain: "example.com"})
	if testResp.StatusCode != http.StatusOK {
		t.Fatalf("using the issued token: got status %d, want 200", testResp.StatusCode)
	}
}

func TestHandleGithubCallbackRejectsReusedState(t *testing.T) {
	server := newTestServer(t, &fakeEnqueuer{}, fakeResolver{}, http.DefaultClient)
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

	loginResp, _ := client.Get(server.URL + "/auth/github/login")
	loc, _ := url.Parse(loginResp.Header.Get("Location"))
	loginResp.Body.Close()
	state := loc.Query().Get("state")

	callbackURL := server.URL + "/auth/github/callback?code=some-code&state=" + state
	first, _ := http.Get(callbackURL)
	first.Body.Close()

	second, err := http.Get(callbackURL)
	if err != nil {
		t.Fatalf("GET failed: %v", err)
	}
	defer second.Body.Close()
	if second.StatusCode != http.StatusBadRequest {
		t.Fatalf("got status %d, want 400 for a reused state", second.StatusCode)
	}
}

func TestHandleGithubCallbackRejectsUnknownState(t *testing.T) {
	server := newTestServer(t, &fakeEnqueuer{}, fakeResolver{}, http.DefaultClient)

	resp, err := http.Get(server.URL + "/auth/github/callback?code=some-code&state=never-issued")
	if err != nil {
		t.Fatalf("GET failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("got status %d, want 400", resp.StatusCode)
	}
}

func TestHandleCreateDomainChallenge(t *testing.T) {
	server := newTestServer(t, &fakeEnqueuer{}, fakeResolver{}, http.DefaultClient)
	token := server.login(t)

	resp := authedRequest(t, http.MethodPost, server.URL+"/domains", token, createDomainRequest{Domain: "example.com"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("got status %d, want 200", resp.StatusCode)
	}

	var got createDomainResponse
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if got.Domain != "example.com" {
		t.Fatalf("got domain %q, want example.com", got.Domain)
	}
	if got.Token == "" {
		t.Fatal("expected a non-empty token")
	}
	if got.DNSRecord != "_sentryload-verify.example.com" {
		t.Fatalf("got dns_record %q, want _sentryload-verify.example.com", got.DNSRecord)
	}
	if got.WellKnownURL != "https://example.com/.well-known/sentry-load-verify.txt" {
		t.Fatalf("got well_known_url %q", got.WellKnownURL)
	}
}

func TestHandleVerifyDomainWithoutChallenge(t *testing.T) {
	server := newTestServer(t, &fakeEnqueuer{}, fakeResolver{}, http.DefaultClient)
	token := server.login(t)

	resp := authedRequest(t, http.MethodPost, server.URL+"/domains/example.com/verify", token, verifyDomainRequest{Method: "dns"})
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("got status %d, want 404", resp.StatusCode)
	}
}

func TestHandleVerifyDomainDNSSuccess(t *testing.T) {
	resolver := fakeResolver{} // filled in after we know the issued token
	server := newTestServer(t, &fakeEnqueuer{}, &resolver, http.DefaultClient)
	token := server.login(t)

	challengeResp := authedRequest(t, http.MethodPost, server.URL+"/domains", token, createDomainRequest{Domain: "example.com"})
	var challenge createDomainResponse
	json.NewDecoder(challengeResp.Body).Decode(&challenge)

	resolver.records = map[string][]string{
		"_sentryload-verify.example.com": {challenge.Token},
	}

	resp := authedRequest(t, http.MethodPost, server.URL+"/domains/example.com/verify", token, verifyDomainRequest{Method: "dns"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("got status %d, want 200", resp.StatusCode)
	}
	var got verifyDomainResponse
	json.NewDecoder(resp.Body).Decode(&got)
	if !got.Verified {
		t.Fatal("expected verified=true")
	}
	if !server.domains.IsVerified("example.com") {
		t.Fatal("expected DomainStore to record example.com as verified")
	}
}

func TestHandleVerifyDomainDNSWrongToken(t *testing.T) {
	resolver := fakeResolver{records: map[string][]string{
		"_sentryload-verify.example.com": {"someone-elses-token"},
	}}
	server := newTestServer(t, &fakeEnqueuer{}, &resolver, http.DefaultClient)
	token := server.login(t)
	authedRequest(t, http.MethodPost, server.URL+"/domains", token, createDomainRequest{Domain: "example.com"})

	resp := authedRequest(t, http.MethodPost, server.URL+"/domains/example.com/verify", token, verifyDomainRequest{Method: "dns"})
	var got verifyDomainResponse
	json.NewDecoder(resp.Body).Decode(&got)
	if got.Verified {
		t.Fatal("expected verified=false for a mismatched token")
	}
	if server.domains.IsVerified("example.com") {
		t.Fatal("expected DomainStore to not mark an unverified domain as verified")
	}
}

func TestHandleVerifyDomainDNSLookupErrorReadsAsUnverified(t *testing.T) {
	// A domain that hasn't set up its TXT record yet returns NXDOMAIN, not
	// empty records — this must read as verified=false, not a 502, the
	// same way a missing well-known file (404) already does.
	resolver := fakeResolver{err: &net.DNSError{Err: "no such host", IsNotFound: true}}
	server := newTestServer(t, &fakeEnqueuer{}, &resolver, http.DefaultClient)
	token := server.login(t)
	authedRequest(t, http.MethodPost, server.URL+"/domains", token, createDomainRequest{Domain: "example.com"})

	resp := authedRequest(t, http.MethodPost, server.URL+"/domains/example.com/verify", token, verifyDomainRequest{Method: "dns"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("got status %d, want 200 (a lookup failure should not be a server error)", resp.StatusCode)
	}
	var got verifyDomainResponse
	json.NewDecoder(resp.Body).Decode(&got)
	if got.Verified {
		t.Fatal("expected verified=false for a domain with no TXT record yet")
	}
	if server.domains.IsVerified("example.com") {
		t.Fatal("expected DomainStore to not mark the domain as verified")
	}
}

func TestHandleVerifyDomainWellKnownSuccess(t *testing.T) {
	var challengeToken string
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(challengeToken))
	}))
	defer target.Close()
	targetURL, _ := url.Parse(target.URL)

	server := newTestServer(t, &fakeEnqueuer{}, fakeResolver{}, rewriteHostClient{target: targetURL})
	token := server.login(t)

	challengeResp := authedRequest(t, http.MethodPost, server.URL+"/domains", token, createDomainRequest{Domain: "example.com"})
	var challenge createDomainResponse
	json.NewDecoder(challengeResp.Body).Decode(&challenge)
	challengeToken = challenge.Token

	resp := authedRequest(t, http.MethodPost, server.URL+"/domains/example.com/verify", token, verifyDomainRequest{Method: "well-known"})
	var got verifyDomainResponse
	json.NewDecoder(resp.Body).Decode(&got)
	if !got.Verified {
		t.Fatal("expected verified=true")
	}
	if !server.domains.IsVerified("example.com") {
		t.Fatal("expected DomainStore to record example.com as verified")
	}
}

func TestHandleCreateTestRejectsUnverifiedDomain(t *testing.T) {
	server := newTestServer(t, &fakeEnqueuer{}, fakeResolver{}, http.DefaultClient)
	token := server.login(t)

	resp := authedRequest(t, http.MethodPost, server.URL+"/tests", token, createTestRequest{
		URL: "http://not-allowed.example.com/fast", VUs: 10, DurationSeconds: 10, RampPattern: "steady",
	})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("got status %d, want 403", resp.StatusCode)
	}
}

func TestHandleCreateTestAllowlistedHostSucceeds(t *testing.T) {
	enqueuer := &fakeEnqueuer{testID: "test-123", subJobIDs: []string{"job-a", "job-b"}}
	server := newTestServer(t, enqueuer, fakeResolver{}, http.DefaultClient)
	token := server.login(t)

	resp := authedRequest(t, http.MethodPost, server.URL+"/tests", token, createTestRequest{
		URL: "http://allowed.example.com/fast", VUs: 20, DurationSeconds: 10, RampPattern: "steady", WorkerCount: 2,
	})
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("got status %d, want 202", resp.StatusCode)
	}
	var got createTestResponse
	json.NewDecoder(resp.Body).Decode(&got)
	if got.TestID != "test-123" {
		t.Fatalf("got test_id %q, want test-123", got.TestID)
	}
	if len(got.SubJobIDs) != 2 {
		t.Fatalf("got %d sub_job_ids, want 2", len(got.SubJobIDs))
	}
	if enqueuer.lastFanout != 2 || enqueuer.lastVUs != 20 {
		t.Fatalf("enqueuer called with fanout=%d vus=%d, want 2/20", enqueuer.lastFanout, enqueuer.lastVUs)
	}

	getResp := authedRequest(t, http.MethodGet, server.URL+"/tests/test-123", token, nil)
	if getResp.StatusCode != http.StatusOK {
		t.Fatalf("GET /tests/test-123: got status %d, want 200", getResp.StatusCode)
	}
}

func TestHandleCreateTestValidatesVUs(t *testing.T) {
	server := newTestServer(t, &fakeEnqueuer{}, fakeResolver{}, http.DefaultClient)
	token := server.login(t)

	resp := authedRequest(t, http.MethodPost, server.URL+"/tests", token, createTestRequest{
		URL: "http://allowed.example.com/fast", VUs: 99999, DurationSeconds: 10, RampPattern: "steady",
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("got status %d, want 400", resp.StatusCode)
	}
}

func TestHandleCreateTestClampsFanoutToVUs(t *testing.T) {
	enqueuer := &fakeEnqueuer{testID: "test-1", subJobIDs: []string{"job-a"}}
	server := newTestServer(t, enqueuer, fakeResolver{}, http.DefaultClient)
	token := server.login(t)

	authedRequest(t, http.MethodPost, server.URL+"/tests", token, createTestRequest{
		URL: "http://allowed.example.com/fast", VUs: 1, DurationSeconds: 10, RampPattern: "steady", WorkerCount: 5,
	})
	if enqueuer.lastFanout != 1 {
		t.Fatalf("got fanout %d, want 1 (clamped to VUs)", enqueuer.lastFanout)
	}
}

func TestHandleCreateTestEnforcesCooldown(t *testing.T) {
	enqueuer := &fakeEnqueuer{testID: "test-1", subJobIDs: []string{"job-a"}}
	server := newTestServerWithCooldown(t, enqueuer, time.Minute)
	token := server.login(t)

	body := createTestRequest{URL: "http://allowed.example.com/fast", VUs: 5, DurationSeconds: 5, RampPattern: "steady"}

	first := authedRequest(t, http.MethodPost, server.URL+"/tests", token, body)
	if first.StatusCode != http.StatusAccepted {
		t.Fatalf("first submission: got status %d, want 202", first.StatusCode)
	}

	second := authedRequest(t, http.MethodPost, server.URL+"/tests", token, body)
	if second.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("second submission: got status %d, want 429", second.StatusCode)
	}
	if retryAfter := second.Header.Get("Retry-After"); retryAfter == "" {
		t.Error("expected a Retry-After header on a 429")
	}
	if enqueuer.calls != 1 {
		t.Fatalf("got %d enqueuer calls, want 1 (the second submission should never reach it)", enqueuer.calls)
	}
}

func TestHandleCreateTestCooldownIsPerUser(t *testing.T) {
	enqueuer := &fakeEnqueuer{testID: "test-1", subJobIDs: []string{"job-a"}}
	server := newTestServerWithCooldown(t, enqueuer, time.Minute)
	body := createTestRequest{URL: "http://allowed.example.com/fast", VUs: 5, DurationSeconds: 5, RampPattern: "steady"}

	userA, err := server.users.FindOrCreate(1, "user-a")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	tokenA, err := server.users.IssueSession(userA.ID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	userB, err := server.users.FindOrCreate(2, "user-b")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	tokenB, err := server.users.IssueSession(userB.ID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	respA := authedRequest(t, http.MethodPost, server.URL+"/tests", tokenA, body)
	if respA.StatusCode != http.StatusAccepted {
		t.Fatalf("user A: got status %d, want 202", respA.StatusCode)
	}

	respB := authedRequest(t, http.MethodPost, server.URL+"/tests", tokenB, body)
	if respB.StatusCode != http.StatusAccepted {
		t.Fatalf("user B: got status %d, want 202 (a different user's cooldown shouldn't apply)", respB.StatusCode)
	}
}

func TestHandleGetTestUnknownID(t *testing.T) {
	server := newTestServer(t, &fakeEnqueuer{}, fakeResolver{}, http.DefaultClient)
	token := server.login(t)

	resp := authedRequest(t, http.MethodGet, server.URL+"/tests/unknown", token, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("got status %d, want 404", resp.StatusCode)
	}
}

func TestHandleGetTestNotVisibleToOtherUsers(t *testing.T) {
	enqueuer := &fakeEnqueuer{testID: "test-1", subJobIDs: []string{"job-a"}}
	server := newTestServer(t, enqueuer, fakeResolver{}, http.DefaultClient)
	owner := server.login(t)

	createResp := authedRequest(t, http.MethodPost, server.URL+"/tests", owner, createTestRequest{
		URL: "http://allowed.example.com/fast", VUs: 5, DurationSeconds: 5, RampPattern: "steady",
	})
	if createResp.StatusCode != http.StatusAccepted {
		t.Fatalf("setup: got status %d, want 202", createResp.StatusCode)
	}

	other, err := server.users.FindOrCreate(2, "someone-else")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	otherToken, err := server.users.IssueSession(other.ID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	resp := authedRequest(t, http.MethodGet, server.URL+"/tests/test-1", otherToken, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("got status %d, want 404 (another user's test should be invisible)", resp.StatusCode)
	}

	ownResp := authedRequest(t, http.MethodGet, server.URL+"/tests/test-1", owner, nil)
	if ownResp.StatusCode != http.StatusOK {
		t.Fatalf("owner request: got status %d, want 200", ownResp.StatusCode)
	}
}

func TestHandleListTestsWithoutHistoryConfigured(t *testing.T) {
	server := newTestServer(t, &fakeEnqueuer{}, fakeResolver{}, http.DefaultClient)
	token := server.login(t)

	resp := authedRequest(t, http.MethodGet, server.URL+"/tests", token, nil)
	if resp.StatusCode != http.StatusNotImplemented {
		t.Fatalf("got status %d, want 501 (Postgres not configured)", resp.StatusCode)
	}
}

func TestHandleListTestsReturnsOwnersHistory(t *testing.T) {
	server := newTestServerWithHistory(t, &fakeEnqueuer{})
	token := server.login(t)
	user, _ := server.users.FindOrCreate(1, "henry") // same identity server.login used

	server.history.byID["test-a"] = TestSnapshot{TestID: "test-a", URL: "http://allowed.example.com/fast", Done: true}
	server.history.owner["test-a"] = user.ID
	server.history.byID["test-b"] = TestSnapshot{TestID: "test-b", URL: "http://allowed.example.com/slow", Done: true}
	server.history.owner["test-b"] = "someone-else"

	resp := authedRequest(t, http.MethodGet, server.URL+"/tests", token, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("got status %d, want 200", resp.StatusCode)
	}
	var got []TestSnapshot
	json.NewDecoder(resp.Body).Decode(&got)
	if len(got) != 1 {
		t.Fatalf("got %d tests, want 1 (only the caller's own)", len(got))
	}
	if got[0].TestID != "test-a" {
		t.Fatalf("got test %q, want test-a", got[0].TestID)
	}
}

func TestHandleGetTestFallsBackToHistory(t *testing.T) {
	server := newTestServerWithHistory(t, &fakeEnqueuer{})
	token := server.login(t)
	user, _ := server.users.FindOrCreate(1, "henry")

	// Not registered in the in-memory TestStore at all — only in history,
	// simulating a test that finished before a coordinator restart.
	server.history.byID["old-test"] = TestSnapshot{TestID: "old-test", URL: "http://allowed.example.com/fast", Done: true, TotalRequests: 42}
	server.history.owner["old-test"] = user.ID

	resp := authedRequest(t, http.MethodGet, server.URL+"/tests/old-test", token, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("got status %d, want 200", resp.StatusCode)
	}
	var got TestSnapshot
	json.NewDecoder(resp.Body).Decode(&got)
	if got.TotalRequests != 42 {
		t.Fatalf("got total_requests %d, want 42", got.TotalRequests)
	}
}

func TestHandleShareTestWithoutHistoryConfigured(t *testing.T) {
	server := newTestServer(t, &fakeEnqueuer{}, fakeResolver{}, http.DefaultClient)
	token := server.login(t)

	resp := authedRequest(t, http.MethodPost, server.URL+"/tests/some-id/share", token, nil)
	if resp.StatusCode != http.StatusNotImplemented {
		t.Fatalf("got status %d, want 501 (Postgres not configured)", resp.StatusCode)
	}
}

func TestHandleShareTestUnknownOrNotOwned(t *testing.T) {
	server := newTestServerWithHistory(t, &fakeEnqueuer{})
	token := server.login(t)

	resp := authedRequest(t, http.MethodPost, server.URL+"/tests/unknown/share", token, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("got status %d, want 404", resp.StatusCode)
	}
}

func TestHandleShareTestReturnsStableLink(t *testing.T) {
	server := newTestServerWithHistory(t, &fakeEnqueuer{})
	token := server.login(t)
	user, _ := server.users.FindOrCreate(1, "henry")

	server.history.byID["test-1"] = TestSnapshot{TestID: "test-1", URL: "http://allowed.example.com/fast", Done: true}
	server.history.owner["test-1"] = user.ID

	first := authedRequest(t, http.MethodPost, server.URL+"/tests/test-1/share", token, nil)
	if first.StatusCode != http.StatusOK {
		t.Fatalf("got status %d, want 200", first.StatusCode)
	}
	var firstBody shareTestResponse
	json.NewDecoder(first.Body).Decode(&firstBody)
	if firstBody.ShareToken == "" {
		t.Fatal("expected a non-empty share token")
	}

	second := authedRequest(t, http.MethodPost, server.URL+"/tests/test-1/share", token, nil)
	var secondBody shareTestResponse
	json.NewDecoder(second.Body).Decode(&secondBody)
	if secondBody.ShareToken != firstBody.ShareToken {
		t.Fatalf("got a different token on the second call (%q vs %q), want the same stable link", secondBody.ShareToken, firstBody.ShareToken)
	}
}

func TestHandleShareTestAnotherUsersTest(t *testing.T) {
	server := newTestServerWithHistory(t, &fakeEnqueuer{})
	owner, _ := server.users.FindOrCreate(1, "owner")
	server.history.byID["test-1"] = TestSnapshot{TestID: "test-1", URL: "http://allowed.example.com/fast", Done: true}
	server.history.owner["test-1"] = owner.ID

	other, err := server.users.FindOrCreate(2, "someone-else")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	otherToken, err := server.users.IssueSession(other.ID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	resp := authedRequest(t, http.MethodPost, server.URL+"/tests/test-1/share", otherToken, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("got status %d, want 404 (can't share someone else's test)", resp.StatusCode)
	}
}

func TestHandlePublicReportWithoutHistoryConfigured(t *testing.T) {
	server := newTestServer(t, &fakeEnqueuer{}, fakeResolver{}, http.DefaultClient)

	resp, err := http.Get(server.URL + "/reports/some-token")
	if err != nil {
		t.Fatalf("GET failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotImplemented {
		t.Fatalf("got status %d, want 501 (Postgres not configured)", resp.StatusCode)
	}
}

func TestHandlePublicReportUnknownToken(t *testing.T) {
	server := newTestServerWithHistory(t, &fakeEnqueuer{})

	resp, err := http.Get(server.URL + "/reports/does-not-exist")
	if err != nil {
		t.Fatalf("GET failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("got status %d, want 404", resp.StatusCode)
	}
}

func TestHandlePublicReportServesSharedTestWithNoAuth(t *testing.T) {
	server := newTestServerWithHistory(t, &fakeEnqueuer{})
	token := server.login(t)
	user, _ := server.users.FindOrCreate(1, "henry")

	server.history.byID["test-1"] = TestSnapshot{TestID: "test-1", URL: "http://allowed.example.com/fast", Done: true, TotalRequests: 99}
	server.history.owner["test-1"] = user.ID

	shareResp := authedRequest(t, http.MethodPost, server.URL+"/tests/test-1/share", token, nil)
	var shareBody shareTestResponse
	json.NewDecoder(shareResp.Body).Decode(&shareBody)

	// No Authorization header at all — this is the point of a public link.
	resp, err := http.Get(server.URL + "/reports/" + shareBody.ShareToken)
	if err != nil {
		t.Fatalf("GET failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("got status %d, want 200", resp.StatusCode)
	}
	var got TestSnapshot
	json.NewDecoder(resp.Body).Decode(&got)
	if got.TotalRequests != 99 {
		t.Fatalf("got total_requests %d, want 99", got.TotalRequests)
	}
}
