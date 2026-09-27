package client

import (
	"crypto/ed25519"
	"crypto/rand"
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
	integrateChecked.Lock()
	integrateChecked.at = time.Time{}
	integrateChecked.Unlock()
	return &Agent{sessionID: "DIRCHAN1", box: box, machine: true, dirLimits: newDirLimits(), integrate: run}, box
}

// An enrolled owner's signature over an apply, the way the viewer makes it.
func integrateProof(t *testing.T, priv ed25519.PrivateKey, name string) ownerProof {
	t.Helper()
	return signAction(t, priv, "DIRCHAN1", IntegrateAction(name), time.Now().Unix())
}

func enrolTestOwner(t *testing.T) ed25519.PrivateKey {
	t.Helper()
	isolateHome(t)
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	if _, _, err := AddOwner(ownerID(pub), "test device"); err != nil {
		t.Skipf("cannot enrol an owner in this environment: %v", err)
	}
	return priv
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

// TestDirIntegrateApplyNeedsTheOwnersSignature: the channel key alone (which
// the relay can see used, and repeat) does not set an agent up. A check
// needs no signature; an apply needs the owner's, over that agent's name.
func TestDirIntegrateApplyNeedsTheOwnersSignature(t *testing.T) {
	priv := enrolTestOwner(t)
	var calls [][]string
	a, box := integrateAgent(t, func(args []string) ([]byte, error) { calls = append(calls, args); return []byte("[]"), nil })
	// No proof: refused, nothing runs, and the refusal is said.
	pt, _ := json.Marshal(map[string]any{"apply": "claude", "req_id": "p1"})
	enc, _ := box.Encrypt(pt)
	got := captureDirReply(t, a, box, enc)
	if got.OK || got.Error == "" || len(calls) != 0 {
		t.Fatalf("apply without a proof: ack=%+v calls=%v", got, calls)
	}
	// A proof for another agent: refused too.
	pt, _ = json.Marshal(map[string]any{"apply": "claude", "proof": integrateProof(t, priv, "codex"), "req_id": "p2"})
	enc, _ = box.Encrypt(pt)
	got = captureDirReply(t, a, box, enc)
	if got.OK || len(calls) != 0 {
		t.Fatalf("apply with a proof for another agent: ack=%+v calls=%v", got, calls)
	}
	// The right proof: runs. The same proof again: a replay, refused.
	proof := integrateProof(t, priv, "claude")
	pt, _ = json.Marshal(map[string]any{"apply": "claude", "proof": proof, "req_id": "p3"})
	enc, _ = box.Encrypt(pt)
	got = captureDirReply(t, a, box, enc)
	if !got.OK || len(calls) != 2 || strings.Join(calls[0], " ") != "integrate -y claude" {
		t.Fatalf("apply with the right proof: ack=%+v calls=%v", got, calls)
	}
	calls = nil
	got = captureDirReply(t, a, box, enc)
	if got.OK || len(calls) != 0 {
		t.Fatalf("a replayed apply ran: ack=%+v calls=%v", got, calls)
	}
	// A check needs no proof.
	pt, _ = json.Marshal(map[string]any{"check": true, "req_id": "p4"})
	enc, _ = box.Encrypt(pt)
	if got = captureDirReply(t, a, box, enc); !got.OK {
		t.Fatalf("check: %+v", got)
	}
}

// TestDirIntegrateChecksAreCachedAndRefusalsAreSaid: a burst of checks runs
// integrate once, and a request the bucket refuses gets an answer rather
// than silence.
func TestDirIntegrateChecksAreCachedAndRefusalsAreSaid(t *testing.T) {
	runs := 0
	a, box := integrateAgent(t, func(args []string) ([]byte, error) { runs++; return []byte("[]"), nil })
	pt, _ := json.Marshal(map[string]any{"check": true, "req_id": "c"})
	enc, _ := box.Encrypt(pt)
	for i := 0; i < 5; i++ {
		if got := captureDirReply(t, a, box, enc); !got.OK {
			t.Fatalf("check %d: %+v", i, got)
		}
	}
	if runs != 1 {
		t.Fatalf("five checks in a burst ran integrate %d times, want 1", runs)
	}
	a.dirLimits.query = newTokenBucket(0, 0)
	if got := captureDirReply(t, a, box, enc); got.OK || !strings.Contains(got.Error, "too many") {
		t.Fatalf("a refused check should say so: %+v", got)
	}
}

// TestDirIntegrateAppliesThenReports: an apply runs `integrate -y <name>` and
// then the check, and the reply carries both the output and the report. The
// name is bounded to a bare agent name, never a flag.
func TestDirIntegrateAppliesThenReports(t *testing.T) {
	priv := enrolTestOwner(t)
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
	pt, _ := json.Marshal(map[string]any{"apply": "Claude", "proof": integrateProof(t, priv, "claude"), "req_id": "r9"})
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
	pt, _ = json.Marshal(map[string]any{"apply": "--remove", "proof": integrateProof(t, priv, "--remove"), "req_id": "r10"})
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
	pt, _ = json.Marshal(map[string]any{"apply": "codex", "proof": integrateProof(t, priv, "codex"), "req_id": "r11"})
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
