package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
)

// Compile-time checks that the real implementations this package will use
// in production satisfy the interfaces above.
var (
	_ txtLookuper = (*net.Resolver)(nil)
	_ httpGetter  = (*http.Client)(nil)
)

const (
	verifyTXTPrefix     = "_sentryload-verify."
	verifyWellKnownPath = "/.well-known/sentry-load-verify.txt"
)

// txtLookuper is the subset of *net.Resolver this package needs, extracted
// as an interface so tests can inject a fake instead of hitting real DNS.
// *net.Resolver satisfies this already.
type txtLookuper interface {
	LookupTXT(ctx context.Context, name string) ([]string, error)
}

// VerifyDomainTXT reports whether domain has published token as a DNS TXT
// record at _sentryload-verify.<domain> — one of the two supported
// domain-ownership proofs (SCOPE.md M7).
func VerifyDomainTXT(ctx context.Context, resolver txtLookuper, domain, token string) (bool, error) {
	records, err := resolver.LookupTXT(ctx, verifyTXTPrefix+domain)
	if err != nil {
		return false, fmt.Errorf("TXT lookup for %s: %w", domain, err)
	}
	for _, r := range records {
		if r == token {
			return true, nil
		}
	}
	return false, nil
}

// httpGetter is the subset of *http.Client this package needs, extracted
// for the same testability reason. *http.Client satisfies this already.
type httpGetter interface {
	Get(url string) (*http.Response, error)
}

// VerifyDomainWellKnown reports whether domain serves exactly token (after
// trimming surrounding whitespace) at /.well-known/sentry-load-verify.txt —
// the second supported domain-ownership proof.
func VerifyDomainWellKnown(client httpGetter, domain, token string) (bool, error) {
	url := "https://" + domain + verifyWellKnownPath
	resp, err := client.Get(url)
	if err != nil {
		return false, fmt.Errorf("fetching %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return false, nil
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err != nil {
		return false, fmt.Errorf("reading response from %s: %w", url, err)
	}
	return strings.TrimSpace(string(body)) == token, nil
}
