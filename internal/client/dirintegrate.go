package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"reminal/internal/procgroup"
	"reminal/internal/protocol"
)

// dirIntegrateAck is the machine channel's reply to a TypeDirIntegrate
// request: the outcome of an apply (if one was asked for) and, either way,
// what `reminal integrate --check` now says about every agent it knows.
type dirIntegrateAck struct {
	ReqID string `json:"req_id,omitempty"`
	OK    bool   `json:"ok,omitempty"`
	Error string `json:"error,omitempty"`
	// Applied echoes the agent that was set up, "" on a plain check.
	Applied string `json:"applied,omitempty"`
	// Output is the tail of the integrate command's output on apply — what
	// the user would have seen running it by hand.
	Output    string                       `json:"output,omitempty"`
	Harnesses []protocol.IntegrationStatus `json:"harnesses,omitempty"`
}

// integrateName bounds what an owner may ask to set up: a binary name as
// `reminal integrate` itself accepts (it matches against its own table and
// ignores anything unknown), never a flag or a path.
var integrateName = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)

// IntegrateAction is what an owner device signs to have this machine set an
// agent up: the agent's name is inside the signature, so a proof for one
// cannot be spent on another. The viewer builds the same string.
func IntegrateAction(name string) string { return "integrate\n" + name }

// One integrate at a time on a machine. Two applies at once would each
// rewrite the same agent configs; and the relay, which cannot author a
// request, can still repeat one it has seen — a repeat must wait its turn,
// not fork another shell.
var integrateRun sync.Mutex

// The last check, kept for a moment: a burst of checks (several owner
// devices, a repeated message) is answered from it rather than by running
// the user's login shell each time.
var integrateChecked struct {
	sync.Mutex
	at  time.Time
	out []byte
}

const (
	integrateCheckFor = 5 * time.Second
	integrateOutMax   = 4 << 10  // what the reply carries of integrate's output
	integrateReadMax  = 64 << 10 // what the daemon keeps of the child's output
)

// handleDirIntegrate serves TypeDirIntegrate: an authenticated owner asks the
// machine to report, or run, reminal's integration with the coding agents on
// it. The work is delegated to this binary's own `integrate` subcommand in a
// child process — one implementation of the config formats, and the daemon
// never parses or writes a user's agent config in-process. An apply also
// needs the owner's signature over the agent's name: it edits the user's
// files, so a message the relay merely repeats must not be enough.
func (a *Agent) handleDirIntegrate(conn *websocket.Conn, data string) {
	var req struct {
		Check bool       `json:"check"`
		Apply string     `json:"apply"`
		Proof ownerProof `json:"proof"`
		ReqID string     `json:"req_id"`
	}
	if !a.decryptDir(data, &req) {
		return
	}
	ack := dirIntegrateAck{ReqID: req.ReqID}
	reply := func() { a.sendWindowMsg(conn, protocol.TypeDirIntegrate, ack) }
	// An apply forks a process that edits config files: the tight bucket. A
	// check is a read, priced like a directory query. Either way a refusal
	// is said, so the asker is not left to wait out a timeout.
	act := dirActQuery
	if req.Apply != "" {
		act = dirActSpawn
	}
	if !a.allowDir(act) {
		ack.Error = "too many requests; try again in a moment"
		reply()
		return
	}
	name := ""
	if req.Apply != "" {
		name = strings.ToLower(strings.TrimSpace(req.Apply))
		if !integrateName.MatchString(name) {
			ack.Error = "not an agent name"
			reply()
			return
		}
		if why := a.verifyOwnerAction(req.Proof, IntegrateAction(name)); why != "" {
			ack.Error = why
			reply()
			return
		}
	}
	if !integrateRun.TryLock() {
		ack.Error = "a setup is already running on this machine; try again in a moment"
		reply()
		return
	}
	defer integrateRun.Unlock()

	run := a.integrate
	if run == nil {
		run = runIntegrateCommand
	}
	if name != "" {
		ack.Applied = name
		out, err := run([]string{"integrate", "-y", name})
		ack.Output = tailOf(string(out), 12, integrateOutMax)
		if err != nil {
			ack.Error = capped(firstNonBlank(strings.TrimSpace(lastLine(string(out))), err.Error()), 512)
		}
		integrateChecked.Lock()
		integrateChecked.at = time.Time{} // the world changed; the next check runs
		integrateChecked.Unlock()
	}
	out, err := cachedCheck(run)
	if err != nil {
		if ack.Error == "" {
			ack.Error = capped(firstNonBlank(strings.TrimSpace(lastLine(string(out))), err.Error()), 512)
		}
	} else if jerr := json.Unmarshal(out, &ack.Harnesses); jerr != nil && ack.Error == "" {
		ack.Error = "unreadable integrate report"
	}
	ack.OK = ack.Error == ""
	reply()
}

// cachedCheck runs `integrate --check --json`, or hands back the last run's
// report if it is a moment old.
func cachedCheck(run func([]string) ([]byte, error)) ([]byte, error) {
	integrateChecked.Lock()
	defer integrateChecked.Unlock()
	if !integrateChecked.at.IsZero() && time.Since(integrateChecked.at) < integrateCheckFor {
		return integrateChecked.out, nil
	}
	out, err := run([]string{"integrate", "--check", "--json"})
	if err == nil {
		integrateChecked.at, integrateChecked.out = time.Now(), out
	}
	return out, err
}

// runIntegrateCommand runs this very binary with the given arguments and
// returns what it printed. Bounded in time — a harness's own `mcp add` that
// hangs (a login prompt, a broken install) must not pin the machine channel
// — and in size. A check's report is its stdout alone: stderr noise from an
// agent's CLI must not turn a good report into an unreadable one.
func runIntegrateCommand(args []string) ([]byte, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, args...)
	cmd.Env = append(os.Environ(), "NO_COLOR=1")
	procgroup.Bound(cmd)
	out := &cappedBuffer{max: integrateReadMax}
	cmd.Stdout = out
	if len(args) > 1 && args[1] == "--check" {
		cmd.Stderr = &cappedBuffer{max: 4 << 10}
	} else {
		cmd.Stderr = out
	}
	err = cmd.Run()
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return out.Bytes(), errors.New("integrate timed out")
	}
	return out.Bytes(), err
}

// cappedBuffer keeps the first max bytes written and drops the rest.
type cappedBuffer struct {
	bytes.Buffer
	max int
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	if room := b.max - b.Len(); room > 0 {
		if len(p) > room {
			p = p[:room]
		}
		b.Buffer.Write(p)
	}
	return len(p), nil
}

// tailOf is the last n lines of s, and no more than max bytes of them.
func tailOf(s string, n, max int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	out := strings.Join(lines, "\n")
	if len(out) > max {
		out = out[len(out)-max:]
	}
	return out
}

func capped(s string, max int) string {
	if len(s) > max {
		return s[:max]
	}
	return s
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.TrimSpace(lines[i]) != "" {
			return lines[i]
		}
	}
	return ""
}

func firstNonBlank(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
