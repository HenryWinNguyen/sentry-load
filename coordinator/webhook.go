package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// webhookNotifier posts a short summary to a user's configured chat
// webhook when one of their tests finishes. Extracted as an interface for
// the same reason as every other outbound dependency in this package
// (verify.go's httpGetter, githubauth.go's oauthExchanger) — tests use a
// fake instead of making a real HTTP call.
type webhookNotifier interface {
	Notify(ctx context.Context, webhookURL string, snap TestSnapshot, reportURL string) error
}

type chatWebhookNotifier struct {
	httpClient *http.Client
}

// webhookPayload sets both Discord's ("content") and Slack's ("text")
// incoming-webhook message fields to the same string. Each platform
// ignores JSON fields it doesn't recognize, so one payload shape works
// for both without needing the user to say which service they're using —
// they just paste whichever webhook URL their chat tool gave them.
type webhookPayload struct {
	Content string `json:"content"`
	Text    string `json:"text"`
}

func (n *chatWebhookNotifier) Notify(ctx context.Context, webhookURL string, snap TestSnapshot, reportURL string) error {
	msg := formatWebhookMessage(snap, reportURL)
	body, err := json.Marshal(webhookPayload{Content: msg, Text: msg})
	if err != nil {
		return fmt.Errorf("encoding webhook payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, webhookURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("building webhook request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := n.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("sending webhook: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("webhook endpoint returned status %d", resp.StatusCode)
	}
	return nil
}

// formatWebhookMessage is plain text, not Markdown — Discord and Slack
// use different bold/italic syntax (** vs *), and the same message is
// sent to both without knowing which one a given URL belongs to, so
// relying on either's markdown dialect would render wrong on one of them.
func formatWebhookMessage(snap TestSnapshot, reportURL string) string {
	status := "finished"
	if snap.CircuitBroken {
		status = "circuit-broken — target error rate spiked, aborted early"
	}
	errRate := 0.0
	if snap.TotalRequests > 0 {
		errRate = float64(snap.TotalErrors) / float64(snap.TotalRequests) * 100
	}
	msg := fmt.Sprintf(
		"Sentry Load — test %s\n%s\n%d requests, %.1f%% errors, %.1f RPS",
		status, snap.URL, snap.TotalRequests, errRate, snap.CombinedRPS,
	)
	if reportURL != "" {
		msg += "\n" + reportURL
	}
	return msg
}

// webhookTimeout bounds how long notifying one user's webhook can take —
// notifyFinishedTest runs this off the hot path (see resultwatcher.go),
// but an unbounded hang would still leak a goroutine per slow/dead
// endpoint indefinitely.
const webhookTimeout = 5 * time.Second
