package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
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

// newTestServer wires a fresh server with the given enqueuer/resolver/http
// client and an allowlist containing only "allowed.example.com", returning
// the httptest server plus the DomainStore/TestStore it was built with so
// tests can inspect state directly.
func newTestServer(t *testing.T, enqueuer jobEnqueuer, resolver txtLookuper, httpClient httpGetter) (*httptest.Server, *DomainStore, *TestStore) {
	t.Helper()
	domains := NewDomainStore()
	tests := NewTestStore()
	allowlist := map[string]bool{"allowed.example.com": true}
	handler := NewServer(enqueuer, tests, domains, allowlist, resolver, httpClient)
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return server, domains, tests
}

func postJSON(t *testing.T, url string, body interface{}) *http.Response {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshaling request body: %v", err)
	}
	resp, err := http.Post(url, "application/json", bytes.NewReader(b))
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func TestHandleCreateDomainChallenge(t *testing.T) {
	server, _, _ := newTestServer(t, &fakeEnqueuer{}, fakeResolver{}, http.DefaultClient)

	resp := postJSON(t, server.URL+"/domains", createDomainRequest{Domain: "example.com"})
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
	server, _, _ := newTestServer(t, &fakeEnqueuer{}, fakeResolver{}, http.DefaultClient)

	resp := postJSON(t, server.URL+"/domains/example.com/verify", verifyDomainRequest{Method: "dns"})
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("got status %d, want 404", resp.StatusCode)
	}
}

func TestHandleVerifyDomainDNSSuccess(t *testing.T) {
	resolver := fakeResolver{} // filled in after we know the issued token
	server, domains, _ := newTestServer(t, &fakeEnqueuer{}, &resolver, http.DefaultClient)

	challengeResp := postJSON(t, server.URL+"/domains", createDomainRequest{Domain: "example.com"})
	var challenge createDomainResponse
	json.NewDecoder(challengeResp.Body).Decode(&challenge)

	resolver.records = map[string][]string{
		"_sentryload-verify.example.com": {challenge.Token},
	}

	resp := postJSON(t, server.URL+"/domains/example.com/verify", verifyDomainRequest{Method: "dns"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("got status %d, want 200", resp.StatusCode)
	}
	var got verifyDomainResponse
	json.NewDecoder(resp.Body).Decode(&got)
	if !got.Verified {
		t.Fatal("expected verified=true")
	}
	if !domains.IsVerified("example.com") {
		t.Fatal("expected DomainStore to record example.com as verified")
	}
}

func TestHandleVerifyDomainDNSWrongToken(t *testing.T) {
	resolver := fakeResolver{records: map[string][]string{
		"_sentryload-verify.example.com": {"someone-elses-token"},
	}}
	server, domains, _ := newTestServer(t, &fakeEnqueuer{}, &resolver, http.DefaultClient)
	postJSON(t, server.URL+"/domains", createDomainRequest{Domain: "example.com"})

	resp := postJSON(t, server.URL+"/domains/example.com/verify", verifyDomainRequest{Method: "dns"})
	var got verifyDomainResponse
	json.NewDecoder(resp.Body).Decode(&got)
	if got.Verified {
		t.Fatal("expected verified=false for a mismatched token")
	}
	if domains.IsVerified("example.com") {
		t.Fatal("expected DomainStore to not mark an unverified domain as verified")
	}
}

func TestHandleVerifyDomainDNSLookupErrorReadsAsUnverified(t *testing.T) {
	// A domain that hasn't set up its TXT record yet returns NXDOMAIN, not
	// empty records — this must read as verified=false, not a 502, the
	// same way a missing well-known file (404) already does.
	resolver := fakeResolver{err: &net.DNSError{Err: "no such host", IsNotFound: true}}
	server, domains, _ := newTestServer(t, &fakeEnqueuer{}, &resolver, http.DefaultClient)
	postJSON(t, server.URL+"/domains", createDomainRequest{Domain: "example.com"})

	resp := postJSON(t, server.URL+"/domains/example.com/verify", verifyDomainRequest{Method: "dns"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("got status %d, want 200 (a lookup failure should not be a server error)", resp.StatusCode)
	}
	var got verifyDomainResponse
	json.NewDecoder(resp.Body).Decode(&got)
	if got.Verified {
		t.Fatal("expected verified=false for a domain with no TXT record yet")
	}
	if domains.IsVerified("example.com") {
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

	server, domains, _ := newTestServer(t, &fakeEnqueuer{}, fakeResolver{}, rewriteHostClient{target: targetURL})

	challengeResp := postJSON(t, server.URL+"/domains", createDomainRequest{Domain: "example.com"})
	var challenge createDomainResponse
	json.NewDecoder(challengeResp.Body).Decode(&challenge)
	challengeToken = challenge.Token

	resp := postJSON(t, server.URL+"/domains/example.com/verify", verifyDomainRequest{Method: "well-known"})
	var got verifyDomainResponse
	json.NewDecoder(resp.Body).Decode(&got)
	if !got.Verified {
		t.Fatal("expected verified=true")
	}
	if !domains.IsVerified("example.com") {
		t.Fatal("expected DomainStore to record example.com as verified")
	}
}

func TestHandleCreateTestRejectsUnverifiedDomain(t *testing.T) {
	server, _, _ := newTestServer(t, &fakeEnqueuer{}, fakeResolver{}, http.DefaultClient)

	resp := postJSON(t, server.URL+"/tests", createTestRequest{
		URL: "http://not-allowed.example.com/fast", VUs: 10, DurationSeconds: 10, RampPattern: "steady",
	})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("got status %d, want 403", resp.StatusCode)
	}
}

func TestHandleCreateTestAllowlistedHostSucceeds(t *testing.T) {
	enqueuer := &fakeEnqueuer{testID: "test-123", subJobIDs: []string{"job-a", "job-b"}}
	server, _, tests := newTestServer(t, enqueuer, fakeResolver{}, http.DefaultClient)

	resp := postJSON(t, server.URL+"/tests", createTestRequest{
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

	if _, ok := tests.Snapshot("test-123"); !ok {
		t.Fatal("expected the test to be registered in TestStore")
	}
}

func TestHandleCreateTestValidatesVUs(t *testing.T) {
	server, _, _ := newTestServer(t, &fakeEnqueuer{}, fakeResolver{}, http.DefaultClient)

	resp := postJSON(t, server.URL+"/tests", createTestRequest{
		URL: "http://allowed.example.com/fast", VUs: 99999, DurationSeconds: 10, RampPattern: "steady",
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("got status %d, want 400", resp.StatusCode)
	}
}

func TestHandleCreateTestClampsFanoutToVUs(t *testing.T) {
	enqueuer := &fakeEnqueuer{testID: "test-1", subJobIDs: []string{"job-a"}}
	server, _, _ := newTestServer(t, enqueuer, fakeResolver{}, http.DefaultClient)

	postJSON(t, server.URL+"/tests", createTestRequest{
		URL: "http://allowed.example.com/fast", VUs: 1, DurationSeconds: 10, RampPattern: "steady", WorkerCount: 5,
	})
	if enqueuer.lastFanout != 1 {
		t.Fatalf("got fanout %d, want 1 (clamped to VUs)", enqueuer.lastFanout)
	}
}

func TestHandleGetTestUnknownID(t *testing.T) {
	server, _, _ := newTestServer(t, &fakeEnqueuer{}, fakeResolver{}, http.DefaultClient)

	resp, err := http.Get(server.URL + "/tests/unknown")
	if err != nil {
		t.Fatalf("GET failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("got status %d, want 404", resp.StatusCode)
	}
}
