package main

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func readBody(t *testing.T, resp *http.Response) string {
	t.Helper()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading response body: %v", err)
	}
	return string(body)
}

func TestHandleBadgeWithoutHistoryConfigured(t *testing.T) {
	server := newTestServer(t, &fakeEnqueuer{}, fakeResolver{}, http.DefaultClient)

	resp, err := http.Get(server.URL + "/badge/some-token")
	if err != nil {
		t.Fatalf("GET failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("got status %d, want 200 (a badge should always render, never error)", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "image/svg+xml" {
		t.Fatalf("got Content-Type %q, want image/svg+xml", ct)
	}
	body := readBody(t, resp)
	if !strings.Contains(body, "unavailable") {
		t.Fatalf("expected badge to say 'unavailable', got: %s", body)
	}
}

func TestHandleBadgeUnknownToken(t *testing.T) {
	server := newTestServerWithHistory(t, &fakeEnqueuer{})

	resp, err := http.Get(server.URL + "/badge/does-not-exist")
	if err != nil {
		t.Fatalf("GET failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("got status %d, want 200", resp.StatusCode)
	}
	body := readBody(t, resp)
	if !strings.Contains(body, "not found") {
		t.Fatalf("expected badge to say 'not found', got: %s", body)
	}
}

func TestHandleBadgeHealthyTest(t *testing.T) {
	server := newTestServerWithHistory(t, &fakeEnqueuer{})
	token := server.login(t)
	user, _ := server.users.FindOrCreate(1, "henry")

	server.history.byID["test-1"] = TestSnapshot{
		TestID: "test-1", URL: "http://allowed.example.com/fast", Done: true,
		TotalRequests: 1000, TotalErrors: 0, CombinedRPS: 500.0,
	}
	server.history.owner["test-1"] = user.ID
	shareResp := authedRequest(t, http.MethodPost, server.URL+"/tests/test-1/share", token, nil)
	var shareBody shareTestResponse
	json.NewDecoder(shareResp.Body).Decode(&shareBody)

	resp, err := http.Get(server.URL + "/badge/" + shareBody.ShareToken)
	if err != nil {
		t.Fatalf("GET failed: %v", err)
	}
	defer resp.Body.Close()

	body := readBody(t, resp)
	if !strings.Contains(body, badgeColorGreen) {
		t.Fatalf("expected a healthy test's badge to use the green fill, got: %s", body)
	}
	if !strings.Contains(body, "500") {
		t.Fatalf("expected the badge to mention the RPS, got: %s", body)
	}
}

func TestHandleBadgeCircuitBrokenTest(t *testing.T) {
	server := newTestServerWithHistory(t, &fakeEnqueuer{})
	token := server.login(t)
	user, _ := server.users.FindOrCreate(1, "henry")

	server.history.byID["test-1"] = TestSnapshot{
		TestID: "test-1", URL: "http://allowed.example.com/fast", Done: true,
		TotalRequests: 100, TotalErrors: 80, CircuitBroken: true,
	}
	server.history.owner["test-1"] = user.ID
	shareResp := authedRequest(t, http.MethodPost, server.URL+"/tests/test-1/share", token, nil)
	var shareBody shareTestResponse
	json.NewDecoder(shareResp.Body).Decode(&shareBody)

	resp, err := http.Get(server.URL + "/badge/" + shareBody.ShareToken)
	if err != nil {
		t.Fatalf("GET failed: %v", err)
	}
	defer resp.Body.Close()

	body := readBody(t, resp)
	if !strings.Contains(body, badgeColorRed) {
		t.Fatalf("expected a circuit-broken test's badge to use the red fill, got: %s", body)
	}
}

func TestHandleBadgeElevatedErrorRate(t *testing.T) {
	server := newTestServerWithHistory(t, &fakeEnqueuer{})
	token := server.login(t)
	user, _ := server.users.FindOrCreate(1, "henry")

	server.history.byID["test-1"] = TestSnapshot{
		TestID: "test-1", URL: "http://allowed.example.com/fast", Done: true,
		TotalRequests: 100, TotalErrors: 10, // 10% error rate, above the amber threshold
	}
	server.history.owner["test-1"] = user.ID
	shareResp := authedRequest(t, http.MethodPost, server.URL+"/tests/test-1/share", token, nil)
	var shareBody shareTestResponse
	json.NewDecoder(shareResp.Body).Decode(&shareBody)

	resp, err := http.Get(server.URL + "/badge/" + shareBody.ShareToken)
	if err != nil {
		t.Fatalf("GET failed: %v", err)
	}
	defer resp.Body.Close()

	body := readBody(t, resp)
	if !strings.Contains(body, badgeColorAmber) {
		t.Fatalf("expected an elevated-error-rate test's badge to use the amber fill, got: %s", body)
	}
}

func TestHandleBadgeSupportsSvgSuffix(t *testing.T) {
	server := newTestServerWithHistory(t, &fakeEnqueuer{})
	token := server.login(t)
	user, _ := server.users.FindOrCreate(1, "henry")

	server.history.byID["test-1"] = TestSnapshot{TestID: "test-1", URL: "http://allowed.example.com/fast", Done: true}
	server.history.owner["test-1"] = user.ID
	shareResp := authedRequest(t, http.MethodPost, server.URL+"/tests/test-1/share", token, nil)
	var shareBody shareTestResponse
	json.NewDecoder(shareResp.Body).Decode(&shareBody)

	// Conventional badge URLs end in .svg — {token} in the route pattern
	// captures it, and the handler needs to strip it back off.
	resp, err := http.Get(server.URL + "/badge/" + shareBody.ShareToken + ".svg")
	if err != nil {
		t.Fatalf("GET failed: %v", err)
	}
	defer resp.Body.Close()

	body := readBody(t, resp)
	if strings.Contains(body, "not found") {
		t.Fatalf("expected the .svg suffix to be stripped and the token still resolve, got: %s", body)
	}
}
