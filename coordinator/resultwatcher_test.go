package main

import (
	"context"
	"sync"
	"testing"
	"time"
)

// notifyFinishedTest runs in its own goroutine (see onTestFinished, which
// deliberately doesn't block the results watcher on webhook delivery) —
// tests here poll briefly rather than asserting synchronously.
const testWebhookPollTimeout = time.Second

// fakeWebhookNotifier records every call instead of making a real HTTP
// request — same pattern as every other fake in this package.
type fakeWebhookNotifier struct {
	mu    sync.Mutex
	calls []fakeWebhookCall
	err   error
}

type fakeWebhookCall struct {
	webhookURL string
	snap       TestSnapshot
	reportURL  string
}

func (f *fakeWebhookNotifier) Notify(_ context.Context, webhookURL string, snap TestSnapshot, reportURL string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, fakeWebhookCall{webhookURL: webhookURL, snap: snap, reportURL: reportURL})
	return f.err
}

func (f *fakeWebhookNotifier) waitForCall(t *testing.T) fakeWebhookCall {
	t.Helper()
	deadline := time.Now().Add(testWebhookPollTimeout)
	for time.Now().Before(deadline) {
		f.mu.Lock()
		if len(f.calls) > 0 {
			call := f.calls[0]
			f.mu.Unlock()
			return call
		}
		f.mu.Unlock()
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("timed out waiting for webhook notifier to be called")
	return fakeWebhookCall{}
}

func (f *fakeWebhookNotifier) assertNeverCalled(t *testing.T) {
	t.Helper()
	time.Sleep(testWebhookPollTimeout)
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.calls) != 0 {
		t.Fatalf("expected the webhook notifier to never be called, got %d call(s)", len(f.calls))
	}
}

// setUpFinishedTest registers a single-sub-job test and marks it done,
// returning the test ID — the state onTestFinished expects to find via
// store.snapshotUnscoped.
func setUpFinishedTest(store *TestStore, ownerID, url string, circuitBroken bool) string {
	store.Register("test-1", ownerID, url, []string{"job-1"})
	store.Update("test-1", "job-1", 100, 2, 50.5, "10", "20", "30", true, circuitBroken)
	return "test-1"
}

func TestOnTestFinishedNotifiesConfiguredWebhook(t *testing.T) {
	store := NewTestStore()
	users := NewUserStore()
	user, err := users.FindOrCreate(1, "henry")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	users.SetWebhookURL(user.ID, "https://discord.com/api/webhooks/1/abc")
	webhooks := &fakeWebhookNotifier{}

	testID := setUpFinishedTest(store, user.ID, "http://allowed.example.com/fast", false)

	onTestFinished(context.Background(), store, nil, users, webhooks, "https://dashboard.example.com", testID)

	call := webhooks.waitForCall(t)
	if call.webhookURL != "https://discord.com/api/webhooks/1/abc" {
		t.Fatalf("got webhook URL %q, want the user's configured one", call.webhookURL)
	}
	if call.snap.TestID != testID {
		t.Fatalf("got snapshot for test %q, want %q", call.snap.TestID, testID)
	}
	// No history configured — nothing to build a share link from, so the
	// report URL must be empty rather than a broken link.
	if call.reportURL != "" {
		t.Fatalf("got report_url %q, want empty (no history configured)", call.reportURL)
	}
}

func TestOnTestFinishedSkipsUsersWithNoWebhookConfigured(t *testing.T) {
	store := NewTestStore()
	users := NewUserStore()
	user, _ := users.FindOrCreate(1, "henry") // never called SetWebhookURL
	webhooks := &fakeWebhookNotifier{}

	testID := setUpFinishedTest(store, user.ID, "http://allowed.example.com/fast", false)
	onTestFinished(context.Background(), store, nil, users, webhooks, "https://dashboard.example.com", testID)

	webhooks.assertNeverCalled(t)
}

func TestOnTestFinishedIncludesReportURLWhenHistoryConfigured(t *testing.T) {
	store := NewTestStore()
	users := NewUserStore()
	user, _ := users.FindOrCreate(1, "henry")
	users.SetWebhookURL(user.ID, "https://discord.com/api/webhooks/1/abc")
	webhooks := &fakeWebhookNotifier{}
	history := newFakeHistoryStore()

	testID := setUpFinishedTest(store, user.ID, "http://allowed.example.com/fast", false)
	onTestFinished(context.Background(), store, history, users, webhooks, "https://dashboard.example.com", testID)

	call := webhooks.waitForCall(t)
	want := "https://dashboard.example.com/reports/share-" + testID
	if call.reportURL != want {
		t.Fatalf("got report_url %q, want %q", call.reportURL, want)
	}
}

func TestOnTestFinishedSavesToHistoryIndependentlyOfWebhook(t *testing.T) {
	store := NewTestStore()
	users := NewUserStore()
	user, _ := users.FindOrCreate(1, "henry") // no webhook configured
	webhooks := &fakeWebhookNotifier{}
	history := newFakeHistoryStore()

	testID := setUpFinishedTest(store, user.ID, "http://allowed.example.com/fast", false)
	onTestFinished(context.Background(), store, history, users, webhooks, "https://dashboard.example.com", testID)

	if _, ok := history.byID[testID]; !ok {
		t.Fatal("expected the test to be saved to history even though no webhook was configured")
	}
}
