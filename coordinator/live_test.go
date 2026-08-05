package main

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func dialTestLive(t *testing.T, server *testServer, testID, token string) *websocket.Conn {
	t.Helper()
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/tests/" + testID + "/live?token=" + url.QueryEscape(token)
	conn, resp, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		status := 0
		if resp != nil {
			status = resp.StatusCode
		}
		t.Fatalf("dialing %s: %v (status %d)", wsURL, err, status)
	}
	t.Cleanup(func() { conn.Close() })
	return conn
}

func TestHandleTestLiveRejectsMissingToken(t *testing.T) {
	enqueuer := &fakeEnqueuer{testID: "test-1", subJobIDs: []string{"job-a"}}
	server := newTestServer(t, enqueuer, fakeResolver{}, http.DefaultClient)
	owner := server.login(t)
	authedRequest(t, http.MethodPost, server.URL+"/tests", owner, createTestRequest{
		URL: "http://allowed.example.com/fast", VUs: 5, DurationSeconds: 5, RampPattern: "steady",
	})

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/tests/test-1/live"
	_, resp, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err == nil {
		t.Fatal("expected the dial to fail without a token")
	}
	if resp == nil || resp.StatusCode != http.StatusUnauthorized {
		status := 0
		if resp != nil {
			status = resp.StatusCode
		}
		t.Fatalf("got status %d, want 401", status)
	}
}

func TestHandleTestLiveRejectsUnknownTest(t *testing.T) {
	server := newTestServer(t, &fakeEnqueuer{}, fakeResolver{}, http.DefaultClient)
	token := server.login(t)

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/tests/unknown/live?token=" + url.QueryEscape(token)
	_, resp, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err == nil {
		t.Fatal("expected the dial to fail for an unknown test")
	}
	if resp == nil || resp.StatusCode != http.StatusNotFound {
		status := 0
		if resp != nil {
			status = resp.StatusCode
		}
		t.Fatalf("got status %d, want 404", status)
	}
}

func TestHandleTestLiveStreamsUpdatesThenCloses(t *testing.T) {
	enqueuer := &fakeEnqueuer{testID: "test-1", subJobIDs: []string{"job-a"}}
	server := newTestServer(t, enqueuer, fakeResolver{}, http.DefaultClient)
	token := server.login(t)

	createResp := authedRequest(t, http.MethodPost, server.URL+"/tests", token, createTestRequest{
		URL: "http://allowed.example.com/fast", VUs: 5, DurationSeconds: 5, RampPattern: "steady",
	})
	if createResp.StatusCode != http.StatusAccepted {
		t.Fatalf("setup: got status %d, want 202", createResp.StatusCode)
	}

	conn := dialTestLive(t, server, "test-1", token)

	var initial TestSnapshot
	if err := conn.ReadJSON(&initial); err != nil {
		t.Fatalf("reading initial snapshot: %v", err)
	}
	if initial.Done {
		t.Fatal("expected the initial snapshot to not be done yet")
	}

	server.tests.Update("test-1", "job-a", 42, 0, 10.0, "1", "2", "3", false, false)

	var mid TestSnapshot
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	if err := conn.ReadJSON(&mid); err != nil {
		t.Fatalf("reading mid-test update: %v", err)
	}
	if mid.TotalRequests != 42 {
		t.Fatalf("got total requests %d, want 42", mid.TotalRequests)
	}

	server.tests.Update("test-1", "job-a", 100, 0, 20.0, "1", "2", "3", true, false)

	var final TestSnapshot
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	if err := conn.ReadJSON(&final); err != nil {
		t.Fatalf("reading final update: %v", err)
	}
	if !final.Done {
		t.Fatal("expected the final update to report done=true")
	}

	// The server sends a proper close frame once done=true is sent, not
	// just an abrupt TCP teardown — gorilla surfaces that as a
	// *websocket.CloseError with the normal-closure code, not a generic
	// I/O error.
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, _, err := conn.ReadMessage()
	closeErr, ok := err.(*websocket.CloseError)
	if !ok {
		t.Fatalf("got error %v (%T), want a *websocket.CloseError", err, err)
	}
	if closeErr.Code != websocket.CloseNormalClosure {
		t.Fatalf("got close code %d, want %d (CloseNormalClosure)", closeErr.Code, websocket.CloseNormalClosure)
	}
}

func TestHandleTestLiveScopedToOwner(t *testing.T) {
	enqueuer := &fakeEnqueuer{testID: "test-1", subJobIDs: []string{"job-a"}}
	server := newTestServer(t, enqueuer, fakeResolver{}, http.DefaultClient)
	owner := server.login(t)
	authedRequest(t, http.MethodPost, server.URL+"/tests", owner, createTestRequest{
		URL: "http://allowed.example.com/fast", VUs: 5, DurationSeconds: 5, RampPattern: "steady",
	})

	other, err := server.users.FindOrCreate(2, "someone-else")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	otherToken, err := server.users.IssueSession(other.ID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/tests/test-1/live?token=" + url.QueryEscape(otherToken)
	_, resp, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err == nil {
		t.Fatal("expected the dial to fail for another user's test")
	}
	if resp == nil || resp.StatusCode != http.StatusNotFound {
		status := 0
		if resp != nil {
			status = resp.StatusCode
		}
		t.Fatalf("got status %d, want 404 (not 403 — shouldn't reveal the test exists)", status)
	}
}
