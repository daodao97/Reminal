// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Harshal Gajjar

package client

import (
	"bufio"
	"strings"

	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"fmt"
	"golang.org/x/term"
	"os"
	"sync"
	"time"
)

// errMachineNotConfirmed is returned when an owner connect reaches a machine
// this device has not connected to before and nobody confirmed it.
var errMachineNotConfirmed = errors.New("owner: not connected — this device has not connected to that machine before, and it was not confirmed. Run `reminal connect` in a terminal to confirm it")

// sessionHomeTimeout bounds the lookup of which machine a session is on.
const sessionHomeTimeout = 5 * time.Second

// findSessionHome asks this device's machines, over their directory channels,
// which of them hosts sessionID. A var so tests can stand in for the directory.
var findSessionHome = func(sessionID string, machines []OwnedMachine) (ed25519.PublicKey, bool) {
	type hit struct{ key ed25519.PublicKey }
	found := make(chan hit, len(machines))
	var wg sync.WaitGroup
	for _, m := range machines {
		wg.Add(1)
		go func(m OwnedMachine) {
			defer wg.Done()
			resp, err := QueryDirectory(m.Key, sessionHomeTimeout)
			if err != nil {
				return
			}
			for _, s := range resp.Sessions {
				if s.ID == sessionID {
					found <- hit{m.Key}
					return
				}
			}
		}(m)
	}
	go func() { wg.Wait(); close(found) }()
	h, ok := <-found
	return h.key, ok
}

// checkMachineIdentity decides whether the machine that answered an owner
// handshake (its signature already verified) is the one this device expects.
//
// Trust is held per machine, not per session: a machine this device has
// connected to before is recognised by its key on any of its sessions. A key
// this device has not seen is accepted only when the session is not one of
// this device's machines' sessions, and the person confirms it.
func (v *Viewer) checkMachineIdentity(machinePub ed25519.PublicKey) error {
	pinned, known, err := PinnedMachineKey(v.sessionID)
	if err != nil {
		return fmt.Errorf("owner: can't read pinned machine key: %w", err)
	}
	if known {
		if !bytes.Equal(pinned, machinePub) {
			return fmt.Errorf("owner: this machine's identity changed since you first connected — refusing (possible impersonation). If you re-provisioned this machine, remove it from ~/.reminal/known_machines.json and reconnect")
		}
		return nil
	}
	machines, err := ListOwnedMachines()
	// Unreadable: treat as none and ask, but still say that a machine already
	// in use would not have asked.
	unreadable := err != nil
	if unreadable {
		machines = nil
	}
	for _, m := range machines {
		if bytes.Equal(m.Key, machinePub) {
			_, _ = RecordMachineKey(v.sessionID, machinePub) // best-effort
			return nil
		}
	}
	// A key this device pinned for another session, before trust was held
	// per machine, is a machine it already trusts.
	if pinnedForAnySession(machinePub) {
		_ = RecordOwnedMachine(machinePub)
		_, _ = RecordMachineKey(v.sessionID, machinePub)
		return nil
	}
	if home, ok := findSessionHome(v.sessionID, machines); ok {
		name := ShortMachineID(home)
		for _, m := range machines {
			if bytes.Equal(m.Key, home) && m.Name != "" {
				name = m.Name
			}
		}
		return fmt.Errorf("owner: session %s is on your machine %s, but a different machine answered — not connecting", v.sessionID, name)
	}
	if !v.askTrustMachine(machinePub, len(machines) > 0 || unreadable) {
		return errMachineNotConfirmed
	}
	_, _ = RecordMachineKey(v.sessionID, machinePub) // best-effort
	return nil
}

// askTrustMachine asks, on the viewer's own terminal, whether to connect to a
// machine this device has not connected to before. Anything but y is a no,
// and so is having no terminal to ask on.
func (v *Viewer) askTrustMachine(machinePub ed25519.PublicKey, haveMachines bool) bool {
	if v.promptIn == nil {
		// Before the terminal is raw (the first connection), ask on the
		// terminal itself when there is one; with no terminal, nobody can say yes.
		if !term.IsTerminal(int(os.Stdin.Fd())) {
			return false
		}
		fmt.Fprintf(os.Stderr, "\n  This device has not connected to this machine before.\n  Machine: %s\n", MachineID(machinePub))
		if haveMachines {
			fmt.Fprint(os.Stderr, "  If you expected one of the machines you already use, answer n.\n")
		}
		fmt.Fprint(os.Stderr, "  Connect, and remember it? [y/N] ")
		line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		line = strings.TrimSpace(line)
		return line == "y" || line == "Y"
	}
	// Only keys pressed after the question count: drop anything typed before.
	for drained := false; !drained; {
		select {
		case <-v.promptIn:
		default:
			drained = true
		}
	}
	fmt.Fprintf(os.Stderr, "\r\n  This device has not connected to this machine before.\r\n  Machine: %s\r\n", MachineID(machinePub))
	if haveMachines {
		fmt.Fprint(os.Stderr, "  If you expected one of the machines you already use, answer n.\r\n")
	}
	fmt.Fprint(os.Stderr, "  Connect, and remember it? [y/N] ")
	for {
		select {
		case b, ok := <-v.promptIn:
			if !ok {
				return false
			}
			// The first key that means anything decides; the rest of a pasted
			// chunk does not.
			for _, c := range b {
				if c == ' ' || c == '\t' {
					continue
				}
				if c == 'y' || c == 'Y' {
					fmt.Fprint(os.Stderr, "y\r\n")
					return true
				}
				fmt.Fprint(os.Stderr, "\r\n")
				return false
			}
		case <-v.promptEsc:
			return false
		}
	}
}

// pinnedForAnySession reports whether known_machines.json pins pub for any
// session.
func pinnedForAnySession(pub ed25519.PublicKey) bool {
	m, err := loadKnownMachines()
	if err != nil {
		return false
	}
	for _, enc := range m {
		if raw, err := base64.RawURLEncoding.DecodeString(enc); err == nil && bytes.Equal(raw, pub) {
			return true
		}
	}
	return false
}
