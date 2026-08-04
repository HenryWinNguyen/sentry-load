package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

type fakeResolver struct {
	records map[string][]string
	err     error
}

func (f fakeResolver) LookupTXT(_ context.Context, name string) ([]string, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.records[name], nil
}

func TestVerifyDomainTXT(t *testing.T) {
	tests := []struct {
		name      string
		token     string
		records   map[string][]string
		lookupErr error
		want      bool
		wantErr   bool
	}{
		{
			name:  "matching token",
			token: "abc123",
			records: map[string][]string{
				"_sentryload-verify.example.com": {"abc123"},
			},
			want: true,
		},
		{
			name:  "wrong token",
			token: "abc123",
			records: map[string][]string{
				"_sentryload-verify.example.com": {"someone-elses-token"},
			},
			want: false,
		},
		{
			name:    "no records at all",
			token:   "abc123",
			records: map[string][]string{},
			want:    false,
		},
		{
			name:  "multiple records, one matches",
			token: "abc123",
			records: map[string][]string{
				"_sentryload-verify.example.com": {"unrelated-record", "abc123"},
			},
			want: true,
		},
		{
			name:      "lookup error propagates",
			token:     "abc123",
			lookupErr: errors.New("no such host"),
			wantErr:   true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			resolver := fakeResolver{records: tc.records, err: tc.lookupErr}
			got, err := VerifyDomainTXT(context.Background(), resolver, "example.com", tc.token)
			if (err != nil) != tc.wantErr {
				t.Fatalf("error = %v, wantErr = %v", err, tc.wantErr)
			}
			if got != tc.want {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}

// rewriteHostClient forwards Get() to a local httptest server regardless of
// the domain baked into the requested URL, so VerifyDomainWellKnown's
// hardcoded "https://<domain>/..." construction can be tested against a
// real local HTTP server without needing real DNS/TLS for a fake domain.
type rewriteHostClient struct {
	target *url.URL
}

func (c rewriteHostClient) Get(rawURL string) (*http.Response, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, err
	}
	u.Scheme = c.target.Scheme
	u.Host = c.target.Host
	return http.Get(u.String())
}

func TestVerifyDomainWellKnown(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		body       string
		token      string
		want       bool
	}{
		{name: "matching token", statusCode: http.StatusOK, body: "abc123", token: "abc123", want: true},
		{name: "matching token with surrounding whitespace", statusCode: http.StatusOK, body: "  abc123\n", token: "abc123", want: true},
		{name: "wrong token", statusCode: http.StatusOK, body: "someone-elses-token", token: "abc123", want: false},
		{name: "challenge file missing (404)", statusCode: http.StatusNotFound, body: "", token: "abc123", want: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var requestedPath string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requestedPath = r.URL.Path
				w.WriteHeader(tc.statusCode)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()

			target, err := url.Parse(server.URL)
			if err != nil {
				t.Fatalf("parsing test server URL: %v", err)
			}

			got, err := VerifyDomainWellKnown(rewriteHostClient{target: target}, "example.com", tc.token)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
			if requestedPath != verifyWellKnownPath {
				t.Fatalf("requested path = %q, want %q", requestedPath, verifyWellKnownPath)
			}
		})
	}
}
