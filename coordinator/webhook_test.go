package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFormatWebhookMessage(t *testing.T) {
	tests := []struct {
		name      string
		snap      TestSnapshot
		reportURL string
		want      string
	}{
		{
			name: "finished, no report url",
			snap: TestSnapshot{URL: "http://allowed.example.com/fast", TotalRequests: 1000, TotalErrors: 5, CombinedRPS: 502.3},
			want: "Sentry Load — test finished\nhttp://allowed.example.com/fast\n1000 requests, 0.5% errors, 502.3 RPS",
		},
		{
			name:      "finished, with report url",
			snap:      TestSnapshot{URL: "http://allowed.example.com/fast", TotalRequests: 1000, TotalErrors: 0, CombinedRPS: 100},
			reportURL: "https://sentry-load.vercel.app/reports/abc123",
			want:      "Sentry Load — test finished\nhttp://allowed.example.com/fast\n1000 requests, 0.0% errors, 100.0 RPS\nhttps://sentry-load.vercel.app/reports/abc123",
		},
		{
			name: "circuit broken",
			snap: TestSnapshot{URL: "http://allowed.example.com/fast", TotalRequests: 40, TotalErrors: 25, CombinedRPS: 12.5, CircuitBroken: true},
			want: "Sentry Load — test circuit-broken — target error rate spiked, aborted early\nhttp://allowed.example.com/fast\n40 requests, 62.5% errors, 12.5 RPS",
		},
		{
			name: "zero requests doesn't divide by zero",
			snap: TestSnapshot{URL: "http://allowed.example.com/fast", TotalRequests: 0, TotalErrors: 0, CombinedRPS: 0},
			want: "Sentry Load — test finished\nhttp://allowed.example.com/fast\n0 requests, 0.0% errors, 0.0 RPS",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := formatWebhookMessage(tc.snap, tc.reportURL); got != tc.want {
				t.Fatalf("got:\n%s\nwant:\n%s", got, tc.want)
			}
		})
	}
}

// TestChatWebhookNotifierSendsBothFormats verifies the real implementation
// end to end against a real HTTP server (httptest, same pattern verify.go
// uses for VerifyDomainWellKnown) — the fake used everywhere else in this
// package intentionally never exercises the actual JSON encoding/HTTP
// round trip, so this is the one place that does.
func TestChatWebhookNotifierSendsBothFormats(t *testing.T) {
	var gotMethod, gotContentType string
	var gotBody webhookPayload
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotContentType = r.Header.Get("Content-Type")
		json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusNoContent) // Discord/Slack both 204 on success
	}))
	defer srv.Close()

	n := &chatWebhookNotifier{httpClient: http.DefaultClient}
	snap := TestSnapshot{URL: "http://allowed.example.com/fast", TotalRequests: 100, TotalErrors: 1, CombinedRPS: 50}
	if err := n.Notify(context.Background(), srv.URL, snap, "https://sentry-load.vercel.app/reports/abc"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if gotMethod != http.MethodPost {
		t.Fatalf("got method %q, want POST", gotMethod)
	}
	if gotContentType != "application/json" {
		t.Fatalf("got Content-Type %q, want application/json", gotContentType)
	}
	want := formatWebhookMessage(snap, "https://sentry-load.vercel.app/reports/abc")
	if gotBody.Content != want || gotBody.Text != want {
		t.Fatalf("got content=%q text=%q, want both to equal %q", gotBody.Content, gotBody.Text, want)
	}
}

func TestChatWebhookNotifierReturnsErrorOnNonSuccessStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound) // e.g. a deleted/revoked webhook
	}))
	defer srv.Close()

	n := &chatWebhookNotifier{httpClient: http.DefaultClient}
	err := n.Notify(context.Background(), srv.URL, TestSnapshot{URL: "http://allowed.example.com/fast"}, "")
	if err == nil {
		t.Fatal("expected an error for a 404 response, got nil")
	}
}

func TestIsValidWebhookURL(t *testing.T) {
	tests := []struct {
		url  string
		want bool
	}{
		{"https://discord.com/api/webhooks/123/abc", true},
		{"https://hooks.slack.com/services/T00/B00/xxx", true},
		{"http://discord.com/api/webhooks/123/abc", false}, // not https
		{"https://127.0.0.1/webhook", false},
		{"https://169.254.169.254/webhook", false},
		{"https://localhost/webhook", false},
		{"https://internal.localhost/webhook", false},
		{"not-a-url", false},
		{"", false},
	}
	for _, tc := range tests {
		t.Run(tc.url, func(t *testing.T) {
			if got := isValidWebhookURL(tc.url); got != tc.want {
				t.Fatalf("isValidWebhookURL(%q) = %v, want %v", tc.url, got, tc.want)
			}
		})
	}
}
