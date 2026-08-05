package main

import (
	"log"
	"net/http"

	"github.com/gorilla/websocket"
)

var wsUpgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	// The dashboard is a separate origin (e.g. localhost:3000 vs the
	// coordinator's :8080) and auth here is a bearer token, not a cookie —
	// there's no session to forge cross-origin, so allowing any origin
	// isn't the CSRF risk it would be for cookie-based auth.
	CheckOrigin: func(r *http.Request) bool { return true },
}

// handleTestLive upgrades to a WebSocket and pushes a new TestSnapshot
// every time the test's state changes, until done=true — replaces polling
// GET /tests/{id} for a live view (M10).
//
// Browsers can't set custom headers on the WebSocket handshake, so unlike
// every other protected route this one takes its bearer token as a query
// parameter instead of the Authorization header requireAuth expects. That
// means the token can end up in server access logs or browser history;
// acceptable for this project's threat model (an indie tool with
// short-lived, individually revocable sessions, not a compliance-bound
// product), but worth naming as a real tradeoff rather than an oversight.
func (s *apiServer) handleTestLive(w http.ResponseWriter, r *http.Request) {
	user, ok := s.users.UserForSession(r.URL.Query().Get("token"))
	if !ok {
		writeError(w, http.StatusUnauthorized, "missing or invalid token")
		return
	}

	id := r.PathValue("id")
	initial, ok := s.tests.Snapshot(id, user.ID)
	if !ok {
		writeError(w, http.StatusNotFound, "unknown test id")
		return
	}

	conn, err := wsUpgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("websocket upgrade failed: %v", err)
		return
	}
	defer conn.Close()

	// Subscribe before re-checking state, not after sending `initial` — a
	// fast test can finish in the gap between the pre-upgrade Snapshot
	// above and here. Re-fetching only after Subscribe guarantees no
	// Update() in between is missed: it's either already reflected in this
	// fresh snapshot, or it arrives on the channel because we're already
	// registered for it.
	updates, unsubscribe := s.tests.Subscribe(id)
	defer unsubscribe()

	if initial, ok = s.tests.Snapshot(id, user.ID); ok {
		if err := conn.WriteJSON(initial); err != nil {
			return
		}
		if initial.Done {
			closeGracefully(conn)
			return
		}
	}

	// gorilla requires reading the connection to notice a client-initiated
	// close; this loop's only job is to detect that and unblock the send
	// loop below. We never expect the client to send anything itself.
	closed := make(chan struct{})
	go func() {
		defer close(closed)
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}()

	for {
		select {
		case snap, ok := <-updates:
			if !ok {
				return
			}
			if err := conn.WriteJSON(snap); err != nil {
				return
			}
			if snap.Done {
				closeGracefully(conn)
				return
			}
		case <-closed:
			return
		}
	}
}

// closeGracefully sends a proper WebSocket close frame so the client sees
// a clean 1000 (normal closure) instead of an abrupt 1006 (abnormal
// closure) from the connection just being torn down — the deferred
// conn.Close() in the caller still runs afterward for the actual teardown.
func closeGracefully(conn *websocket.Conn) {
	_ = conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
}
