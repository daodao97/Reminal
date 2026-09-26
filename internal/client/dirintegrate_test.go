package client

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"reminal/internal/crypto"
	"reminal/internal/protocol"
)

func integrateAgent(t *testing.T, run func(args []string) ([]byte, error)) (*Agent, *crypto.Box) {
	t.Helper()
	box, err := crypto.NewBox(mustSessionKey(t))
	if err != nil {
		t.Fatal(err)
	}
	return &Agent{box: box, machine: true, dirLimits: newDirLimits(), integrate: run}, box
}

// TestDirIntegrateRequiresOwnerKey: a request that does not decrypt under the
// channel key — anyone who merely knows the channel id — must run nothing.
func TestDirIntegrateRequiresOwnerKey(t *testing.T) {
	ran := 0
	a, _ := integrateAgent(t, func([]string) ([]byte, error) { ran++; return []byte("[]"), nil })
	a.handleDirIntegrate(nil, "")
	a.handleDirIntegrate(nil, base64.StdEncoding.EncodeToString([]byte("junk")))
	other, _ := crypto.NewBox(mustSessionKey(t))
	bad, _ := other.Encrypt([]byte(`{"apply":"claude"}`))
	a.handleDirIntegrate(nil, bad)
	if ran != 0 {
		t.Fatalf("integrate ran %d times for unauthenticated requests", ran)
	}
}

// TestDirIntegrateAppliesThenReports: an apply runs `integrate -y <name>` and
// then the check, and the reply carries both the output and the report. The
// name is bounded to a bare agent name, never a flag.
func TestDirIntegrateAppliesThenReports(t *testing.T) {
	var calls [][]string
	a, box := integrateAgent(t, func(args []string) ([]byte, error) {
		calls = append(calls, args)
		if args[1] == "-y" {
			return []byte("  ✓ Claude Code  via claude mcp add\n\nDone. Restart any running agent to pick it up.\n"), nil
		}
		rep := []protocol.IntegrationStatus{{Bin: "claude", Name: "Claude Code", Installed: true, Integrated: true, Current: true, Known: true, Restart: "quit, then claude --continue"}}
		raw, _ := json.Marshal(rep)
		return raw, nil
	})
	pt, _ := json.Marshal(map[string]any{"apply": "Claude", "req_id": "r9"})
	enc, _ := box.Encrypt(pt)
	got := captureDirReply(t, a, box, enc)
	if len(calls) != 2 || strings.Join(calls[0], " ") != "integrate -y claude" || strings.Join(calls[1], " ") != "integrate --check --json" {
		t.Fatalf("calls: %v", calls)
	}
	if !got.OK || got.ReqID != "r9" || got.Applied != "claude" || !strings.Contains(got.Output, "Done.") {
		t.Fatalf("ack: %+v", got)
	}
	if len(got.Harnesses) != 1 || !got.Harnesses[0].Current || got.Harnesses[0].Restart == "" {
		t.Fatalf("harnesses: %+v", got.Harnesses)
	}

	// A flag smuggled as the name is refused before anything runs.
	calls = nil
	pt, _ = json.Marshal(map[string]any{"apply": "--remove", "req_id": "r10"})
	enc, _ = box.Encrypt(pt)
	got = captureDirReply(t, a, box, enc)
	if len(calls) != 0 || got.OK || got.Error == "" {
		t.Fatalf("flag as name: calls=%v ack=%+v", calls, got)
	}

	// A failing apply reports the command's last line and still checks.
	calls = nil
	a.integrate = func(args []string) ([]byte, error) {
		calls = append(calls, args)
		if args[1] == "-y" {
			return []byte("  ✗ Codex CLI  codex: command not found\n1 of 1 failed\n"), errors.New("exit status 1")
		}
		return []byte("[]"), nil
	}
	pt, _ = json.Marshal(map[string]any{"apply": "codex", "req_id": "r11"})
	enc, _ = box.Encrypt(pt)
	got = captureDirReply(t, a, box, enc)
	if got.OK || !strings.Contains(got.Error, "failed") || len(calls) != 2 {
		t.Fatalf("failed apply: ack=%+v calls=%v", got, calls)
	}
}

// captureDirReply runs the handler against a real websocket whose far end
// keeps what the agent writes, and decrypts the reply.
func captureDirReply(t *testing.T, a *Agent, box *crypto.Box, enc string) dirIntegrateAck {
	t.Helper()
	got := make(chan protocol.Message, 4)
	up := websocket.Upgrader{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()
		for {
			var m protocol.Message
			if err := c.ReadJSON(&m); err != nil {
				return
			}
			got <- m
		}
	}))
	defer srv.Close()
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	a.handleDirIntegrate(conn, enc)
	var msg protocol.Message
	select {
	case msg = <-got:
	case <-time.After(3 * time.Second):
		t.Fatal("no reply written")
	}
	if msg.Type != protocol.TypeDirIntegrate {
		t.Fatalf("reply type %q", msg.Type)
	}
	pt, err := box.Decrypt(msg.Data)
	if err != nil {
		t.Fatal(err)
	}
	var ack dirIntegrateAck
	if err := json.Unmarshal(pt, &ack); err != nil {
		t.Fatal(err)
	}
	return ack
}
