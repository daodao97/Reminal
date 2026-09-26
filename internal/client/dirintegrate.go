package client

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/gorilla/websocket"

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

// handleDirIntegrate serves TypeDirIntegrate: an authenticated owner asks the
// machine to report, or run, reminal's integration with the coding agents on
// it. The work is delegated to this binary's own `integrate` subcommand in a
// child process — one implementation of the config formats, and the daemon
// never parses or writes a user's agent config in-process.
func (a *Agent) handleDirIntegrate(conn *websocket.Conn, data string) {
	var req struct {
		Check bool   `json:"check"`
		Apply string `json:"apply"`
		ReqID string `json:"req_id"`
	}
	if !a.decryptDir(data, &req) {
		return
	}
	// An apply forks a process that edits config files: the tight bucket. A
	// check is a read, priced like a directory query.
	act := dirActQuery
	if req.Apply != "" {
		act = dirActSpawn
	}
	if !a.allowDir(act) {
		return
	}
	ack := dirIntegrateAck{ReqID: req.ReqID}
	run := a.integrate
	if run == nil {
		run = runIntegrateCommand
	}
	if req.Apply != "" {
		name := strings.ToLower(strings.TrimSpace(req.Apply))
		if !integrateName.MatchString(name) {
			ack.Error = "not an agent name"
			a.sendWindowMsg(conn, protocol.TypeDirIntegrate, ack)
			return
		}
		ack.Applied = name
		out, err := run([]string{"integrate", "-y", name})
		ack.Output = tailLines(string(out), 12)
		if err != nil {
			ack.Error = firstNonBlank(strings.TrimSpace(lastLine(string(out))), err.Error())
		}
	}
	out, err := run([]string{"integrate", "--check", "--json"})
	if err != nil {
		if ack.Error == "" {
			ack.Error = firstNonBlank(strings.TrimSpace(lastLine(string(out))), err.Error())
		}
	} else if jerr := json.Unmarshal(out, &ack.Harnesses); jerr != nil && ack.Error == "" {
		ack.Error = "unreadable integrate report"
	}
	ack.OK = ack.Error == ""
	a.sendWindowMsg(conn, protocol.TypeDirIntegrate, ack)
}

// runIntegrateCommand runs this very binary with the given arguments and
// returns its combined output. Bounded: a harness's own `mcp add` that hangs
// (a login prompt, a broken install) must not pin the machine channel.
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
	out, err := cmd.CombinedOutput()
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return out, errors.New("integrate timed out")
	}
	return out, err
}

func tailLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
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
