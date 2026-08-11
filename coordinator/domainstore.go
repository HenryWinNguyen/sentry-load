package main

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strings"
	"sync"
)

// DomainStore tracks per-domain verification challenges and verified
// status in memory. Good enough for the walking-skeleton stage — losing
// this on restart just means re-verifying, not losing test history.
// Persisting it to Postgres is M10's job (SCOPE.md), not before.
//
// verified is keyed by domain, then by the ID of whichever user actually
// completed that domain's verification — proving ownership of a domain
// authorizes *that user* to target it, not every user on the platform.
// The outstanding challenge/token itself stays domain-only keyed (not
// per-user): the token alone proves nothing, only a matching DNS TXT
// record or well-known file does, so there's nothing to gain by knowing
// someone else's outstanding token.
type DomainStore struct {
	mu         sync.Mutex
	challenges map[string]string
	verified   map[string]map[string]bool
}

func NewDomainStore() *DomainStore {
	return &DomainStore{
		challenges: make(map[string]string),
		verified:   make(map[string]map[string]bool),
	}
}

// IssueChallenge generates a fresh random token for domain and remembers
// it, overwriting any previous unclaimed challenge for that domain.
func (s *DomainStore) IssueChallenge(domain string) (string, error) {
	token, err := randomToken()
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.challenges[domain] = token
	return token, nil
}

// Challenge returns the outstanding token for domain, if any.
func (s *DomainStore) Challenge(domain string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.challenges[domain]
	return t, ok
}

// MarkVerified records domain as verified by ownerID specifically — it
// does not authorize any other user to target domain.
func (s *DomainStore) MarkVerified(domain, ownerID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.verified[domain] == nil {
		s.verified[domain] = make(map[string]bool)
	}
	s.verified[domain][ownerID] = true
}

// IsVerified reports whether ownerID specifically has verified domain —
// another user having verified the same domain doesn't count.
func (s *DomainStore) IsVerified(domain, ownerID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.verified[domain][ownerID]
}

// hostnameRE matches a plausible public DNS hostname: dot-separated
// labels of alphanumerics/hyphens (never leading/trailing hyphen), at
// least two labels so a bare single-word internal hostname can't sneak
// through.
var hostnameRE = regexp.MustCompile(`^[a-zA-Z0-9]([a-zA-Z0-9-]*[a-zA-Z0-9])?(\.[a-zA-Z0-9]([a-zA-Z0-9-]*[a-zA-Z0-9])?)+$`)

// isValidDomainName rejects the obvious SSRF vectors for the domain
// verification flow (handleCreateDomainChallenge, then
// VerifyDomainTXT/VerifyDomainWellKnown) — an IP literal or a
// loopback/internal-looking name would make the coordinator itself
// originate a DNS lookup or HTTP request at attacker-chosen infrastructure
// (e.g. localhost, a cloud metadata address) under the guise of "domain
// verification." Doesn't attempt to catch DNS-rebinding-style attacks
// (resolving to a private IP at fetch time despite a public-looking
// name) — out of proportion for this project's threat model; this closes
// the cheap, obvious cases.
func isValidDomainName(domain string) bool {
	if len(domain) == 0 || len(domain) > 253 {
		return false
	}
	if net.ParseIP(domain) != nil {
		return false
	}
	lower := strings.ToLower(domain)
	if lower == "localhost" || strings.HasSuffix(lower, ".localhost") {
		return false
	}
	return hostnameRE.MatchString(domain)
}

// isValidWebhookURL applies the same host-shape check as
// isValidDomainName to a webhook URL's hostname — a webhook is another
// case of the coordinator making an outbound request to a user-supplied
// destination (see handleSetWebhook), so it's exposed to the same SSRF
// class as domain verification and gets the same defense.
func isValidWebhookURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" {
		return false
	}
	return isValidDomainName(u.Hostname())
}

func randomToken() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generating verification token: %w", err)
	}
	return hex.EncodeToString(b), nil
}
