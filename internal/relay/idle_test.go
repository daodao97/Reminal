package relay

import (
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// TestSilentConnDroppedByAuthDeadline proves a connection that authenticates
// nothing is dropped by the pre-auth read deadline instead of leaking a goroutine
// forever (slow-loris). It shrinks authWait so the test is fast.
func TestSilentConnDroppedByAuthDeadline(t *testing.T) {
	s := NewServer()
	// Set on this Server before it starts serving — happens-before the handler
	// goroutines, so no data race (and no shared global to restore).
	s.authWait = 150 * time.Millisecond
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		if len(parts) != 2 {
			http.Error(w, "bad path", http.StatusBadRequest)
			return
		}
		s.HandleSessionWS(w, r, parts[0], parts[1])
	}))
	defer srv.Close()
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")

	c, _, err := websocket.DefaultDialer.Dial(wsURL+"/TESTSESS/agent", nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.Close()

	// Never send anything. The server must close the conn shortly after authWait.
	c.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, _, err = c.ReadMessage()
	if err == nil {
		t.Fatal("expected the server to drop the silent connection, but the read succeeded")
	}
	// The drop should land near authWait (150ms), well before our 2s client
	// deadline — a client-side timeout would mean the server never dropped it.
	if ne, ok := err.(net.Error); ok && ne.Timeout() {
		t.Fatalf("connection was not server-dropped in time (client-side timeout): %v", err)
	}
}
