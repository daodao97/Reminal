// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Harshal Gajjar

package main

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/reminal/reminal/internal/client"
	"github.com/reminal/reminal/internal/protocol"
)

// runMachines lists every machine this device owns and the live sessions running
// on each, or renames one.
// Usage: reminal machines [list | rename <id|name> <new-name>]
func runMachines(args []string) error {
	if len(args) > 0 {
		switch args[0] {
		case "list":
			// fall through to the listing below
		case "rename":
			if len(args) < 3 {
				return fmt.Errorf("usage: reminal machines rename <id|name> <new-name>")
			}
			target := args[1]
			newName := strings.TrimSpace(strings.Join(args[2:], " "))
			if newName == "" {
				return fmt.Errorf("the new name can't be empty")
			}
			om, err := client.RenameOwnedMachine(target, newName)
			if err != nil {
				return err
			}
			fmt.Printf("  %s Renamed → %s  %s\n", cGreen("✓"), cBold(om.Name), cDim("("+client.ShortMachineID(om.Key)+")"))
			return nil
		default:
			return fmt.Errorf("unknown: reminal machines [list | rename <id|name> <new-name>]")
		}
	}
	return listMachines()
}

type machineResult struct {
	m    client.OwnedMachine
	resp protocol.DirResponse
	err  error
}

func listMachines() error {
	machines, err := client.ListOwnedMachines()
	if err != nil {
		return err
	}
	if len(machines) == 0 {
		fmt.Println("  " + cBold("This machine isn't an owner of any other machines yet."))
		fmt.Println()
		fmt.Println("  Run " + cBold("reminal own") + " on this device to get its id, enroll it on another")
		fmt.Println("  machine (" + cBold("sudo reminal add owner <id>") + " there), and once you owner-connect")
		fmt.Println("  it shows up here.")
		return nil
	}

	// Reach every machine's directory channel in parallel — one slow/offline
	// machine shouldn't hold up the rest.
	results := make([]machineResult, len(machines))
	var wg sync.WaitGroup
	for i, m := range machines {
		wg.Add(1)
		go func(i int, m client.OwnedMachine) {
			defer wg.Done()
			resp, qerr := client.QueryDirectory(m.Key, client.DirectoryTimeout)
			results[i] = machineResult{m: m, resp: resp, err: qerr}
		}(i, m)
	}
	wg.Wait()

	online := 0
	for _, r := range results {
		if r.err == nil {
			online++
		}
	}
	// The machine we're running on, so we can flag it like `reminal list` flags
	// the current session.
	localKey, _ := client.MachinePub()

	fmt.Printf("  %s\n\n", cBold(fmt.Sprintf("Machines this device owns (%d) — %d online", len(machines), online)))
	for _, r := range results {
		printMachine(r, localKey != nil && r.m.Key.Equal(localKey))
		fmt.Println()
	}
	return nil
}

func printMachine(r machineResult, isLocal bool) {
	// Prefer the user's name, then the machine's reported hostname, then the id.
	name := r.m.Name
	if name == "" {
		if r.err == nil && r.resp.Hostname != "" {
			name = r.resp.Hostname
		} else {
			name = client.ShortMachineID(r.m.Key)
		}
	}
	name = cleanTerm(name) // may be the remote's reported hostname
	short := client.ShortMachineID(r.m.Key)
	// Don't repeat the id when it's already standing in for an (unnamed) name.
	idPart := ""
	if name != short {
		idPart = "   " + cDim(short)
	}
	localTag := ""
	if isLocal {
		localTag = "  " + cGreen("[this machine]")
	}

	if r.err != nil { // offline — nothing to enumerate
		fmt.Printf("  %s %s%s  %s%s\n", cDim("○"), cBold(name), idPart, cRed("(offline)"), localTag)
		return
	}
	count := ""
	if n := len(r.resp.Sessions); n > 0 {
		count = " " + cDim(fmt.Sprintf("· %d", n))
	}
	fmt.Printf("  %s %s%s%s%s\n", cGreen("●"), cBold(name), count, idPart, localTag)
	if len(r.resp.Sessions) == 0 {
		fmt.Println("      " + cDim("no sessions running"))
		return
	}

	// Shells before port forwards, then least-idle first, so the session you're
	// most likely to want is at the top.
	sess := append([]protocol.DirSession(nil), r.resp.Sessions...)
	sort.SliceStable(sess, func(i, j int) bool {
		pi, pj := sess[i].Kind == "port", sess[j].Kind == "port"
		if pi != pj {
			return !pi // shells first
		}
		return sess[i].IdleSecs < sess[j].IdleSecs
	})

	// Colour breaks tabwriter (it counts escape bytes as width), so pad manually:
	// id (session ids are a fixed 8 chars) + a label column sized to the widest,
	// capped so one very long title can't stretch the whole table.
	const maxLabelCap = 40
	labels := make([]string, len(sess))
	maxLabel := 0
	for i, s := range sess {
		// Labels come from a remote machine — sanitize before printing so a
		// hostile title can't inject terminal escapes.
		labels[i] = truncate(cleanTerm(sessionLabel(s)), maxLabelCap)
		if w := visLen(labels[i]); w > maxLabel {
			maxLabel = w
		}
	}
	plain := func(x string) string { return x }
	for i, s := range sess {
		fmt.Printf("      %s  %s  %s\n",
			padCol(s.ID, 8, cBold),
			padCol(labels[i], maxLabel, plain),
			cDim(sessionMeta(s)))
	}
}

// truncate shortens s to at most n runes, appending an ellipsis when it cuts.
func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

// sessionLabel picks the most recognisable one-liner for a session.
func sessionLabel(s protocol.DirSession) string {
	if s.Kind == "port" {
		return fmt.Sprintf("port :%d", s.Port)
	}
	switch {
	case s.Name != "":
		return s.Name
	case s.Title != "":
		return s.Title
	case s.Cwd != "":
		return abbrevHome(s.Cwd)
	case s.Headless:
		return "background shell"
	default:
		return "shell"
	}
}

// sessionMeta is the trailing "2 viewers · idle 3m" column.
func sessionMeta(s protocol.DirSession) string {
	var parts []string
	if s.Viewers > 0 {
		unit := "viewer"
		if s.Viewers != 1 {
			unit += "s"
		}
		parts = append(parts, fmt.Sprintf("%d %s", s.Viewers, unit))
	}
	if s.IdleSecs > 0 {
		parts = append(parts, "idle "+humanShort(time.Duration(s.IdleSecs)*time.Second))
	}
	return strings.Join(parts, "  ")
}
