// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Harshal Gajjar

package main

import (
	"fmt"
	"runtime"
	"strings"
	"time"

	"github.com/reminal/reminal/internal/client"
)

// runOwn prints this device's public owner id and the exact command to paste on
// the machine you want to own. The device keypair is minted on first run; the
// private key never leaves this machine.
func runOwn() error {
	id, err := client.MyOwnerID()
	if err != nil {
		return err
	}
	fmt.Println()
	fmt.Println("  " + cBold("This device's owner id") + cDim("  (safe to share — it's a public key)"))
	fmt.Println()
	fmt.Println("    " + cBold(id))
	if fp, err := client.MyDeviceFingerprint(); err == nil {
		fmt.Println()
		fmt.Println("  Shown as " + cGreen(fp) + " in " + cBold("reminal owners") + ".")
	}
	fmt.Println()
	fmt.Println("  " + cDim("On the machine you want to own, paste:"))
	fmt.Println()
	fmt.Println("    " + cBold("sudo reminal add owner "+id))
	fmt.Println()
	return nil
}

// runAddOwner records a pasted owner id in this machine's owners.json.
// Usage: reminal add owner <id> [--label <name>]
// It's forgiving about paste slips: if you paste the whole suggested line, the
// rmnl_… token is still picked out, and a bare label after the id is captured.
func runAddOwner(args []string) error {
	id, label := parseAddOwnerArgs(args)
	if id == "" {
		return fmt.Errorf("usage: reminal add owner <id> [--label <name>]")
	}

	o, res, err := client.AddOwner(id, label)
	if needsSudoRetry(err) {
		return sudoReexec() // writing /etc/reminal needs root — re-run under sudo
	}
	if err != nil {
		return err
	}
	name := o.Label
	if name == "" {
		name = o.ID
	}
	switch res {
	case client.OwnerAdded:
		fmt.Printf("  %s Added owner %s  %s\n", cGreen("✓"), cBold(name), cDim("("+o.ID+")"))
	case client.OwnerRelabeled:
		fmt.Printf("  %s Already an owner — relabeled to %s  %s\n", cGreen("✓"), cBold(o.Label), cDim("("+o.ID+")"))
	case client.OwnerUnchanged:
		fmt.Printf("  %s is already an owner  %s\n", cBold(name), cDim("("+o.ID+")"))
		if o.Label != "" {
			fmt.Println("  " + cDim("To relabel it:  reminal owners rename "+o.ID+" <new-name>"))
		}
	}
	// Now that this machine has an owner, make sure it keeps a presence even with
	// no live session — so it stays listable and "+"-spawnable from the owner's
	// phone. Runs here (the context that actually wrote the store, under sudo), so
	// it installs exactly once; the auto-escalating parent doesn't re-run it.
	enableBackgroundHost()
	return nil
}

// enableBackgroundHost installs (idempotently) the login service that keeps this
// machine's directory host running when idle. Best-effort: enrollment already
// succeeded, so a service hiccup must not fail the command — we just tell the
// user the machine will then only be reachable while a session is up.
func enableBackgroundHost() {
	if err := client.InstallDaemonService(); err != nil {
		fmt.Println("  " + cDim("(background host not enabled: "+err.Error()+")"))
		fmt.Println("  " + cDim("This machine stays reachable to you only while a session is running."))
		return
	}
	fmt.Println("  " + cDim("Background host enabled — this machine stays reachable to you even when idle."))
}

// disableBackgroundHostIfLastOwner tears the login service down once the machine
// has no owners left. On macOS the daemon is a PERMANENT local service — it
// performs all screen capture + input injection so one grant covers every session
// — so we keep it even with zero owners. Elsewhere the presence is only needed
// while someone owns the machine, so the old teardown stands.
func disableBackgroundHostIfLastOwner() {
	if runtime.GOOS == "darwin" {
		return
	}
	owners, err := client.ListOwners()
	if err != nil || len(owners) > 0 {
		return
	}
	_ = client.UninstallDaemonService()
}

// parseAddOwnerArgs pulls the owner id and label out of `add owner` arguments.
// It picks the rmnl_ token as the id even if extra words came along (a stray
// paste), but only treats trailing words as a bare label when the id was typed
// FIRST — so it never scavenges a label out of a longer pasted line. An
// explicit --label always wins.
func parseAddOwnerArgs(args []string) (id, label string) {
	var positionals []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--label" && i+1 < len(args):
			label = args[i+1]
			i++
		case strings.HasPrefix(a, "--label="):
			label = strings.TrimPrefix(a, "--label=")
		case strings.HasPrefix(a, "-"):
			// ignore stray flags
		default:
			positionals = append(positionals, a)
		}
	}
	idIdx := -1
	for i, p := range positionals {
		if client.LooksLikeOwnerID(p) {
			id, idIdx = p, i
			break
		}
	}
	if id == "" && len(positionals) > 0 {
		id, idIdx = positionals[0], 0
	}
	if label == "" && idIdx == 0 && len(positionals) > 1 {
		label = strings.Join(positionals[1:], " ")
	}
	return id, label
}

// runOwners lists the machine's owner devices, or renames/revokes one.
// Usage: reminal owners [rename <id|label> <new-label> | revoke <id|label>]
func runOwners(args []string) error {
	if len(args) == 0 {
		return listOwners()
	}
	switch args[0] {
	case "list":
		return listOwners()
	case "rename":
		if len(args) < 3 {
			return fmt.Errorf("usage: reminal owners rename <id|label> <new-label>")
		}
		target := args[1]
		newLabel := strings.TrimSpace(strings.Join(args[2:], " "))
		if newLabel == "" {
			return fmt.Errorf("the new label can't be empty")
		}
		o, n, err := client.RenameOwner(target, newLabel)
		if needsSudoRetry(err) {
			return sudoReexec()
		}
		if err != nil {
			return err
		}
		switch {
		case n == 0:
			fmt.Printf("  %s No owner matching %q — run %s to see them.\n", cRed("!"), target, cBold("reminal owners"))
		case n > 1:
			printAmbiguous(target)
		default:
			fmt.Printf("  %s Renamed %s → %s\n", cGreen("✓"), cDim(o.ID), cBold(o.Label))
		}
		return nil

	case "revoke":
		if len(args) < 2 {
			return fmt.Errorf("usage: reminal owners revoke <id|label>")
		}
		target := args[1]
		o, n, err := client.RemoveOwner(target)
		if needsSudoRetry(err) {
			return sudoReexec()
		}
		if err != nil {
			return err
		}
		switch {
		case n == 0:
			fmt.Printf("  %s No owner matching %q — run %s to see them.\n", cRed("!"), target, cBold("reminal owners"))
		case n > 1:
			printAmbiguous(target)
		default:
			name := o.Label
			if name == "" {
				name = o.ID
			}
			fmt.Printf("  %s Revoked %s  %s\n", cGreen("✓"), cBold(name), cDim("("+o.ID+")"))
			// If that was the last owner, the machine no longer needs a standing
			// presence — remove the login service.
			disableBackgroundHostIfLastOwner()
		}
		return nil

	case "restore":
		if len(args) < 2 {
			return fmt.Errorf("usage: reminal owners restore <id|label>")
		}
		target := args[1]
		o, n, wasRevoked, err := client.RestoreOwnerTarget(target)
		if err != nil {
			return err
		}
		switch {
		case n == 0:
			fmt.Printf("  %s No owner matching %q — run %s to see them.\n", cRed("!"), target, cBold("reminal owners"))
		case n > 1:
			printAmbiguous(target)
		default:
			name := o.Label
			if name == "" {
				name = o.ID
			}
			if wasRevoked {
				fmt.Printf("  %s Restored %s — PIN-free access re-enabled  %s\n", cGreen("✓"), cBold(name), cDim("("+o.ID+")"))
			} else {
				fmt.Printf("  %s %s wasn't revoked — already active  %s\n", cDim("·"), cBold(name), cDim("("+o.ID+")"))
			}
		}
		return nil

	default:
		return fmt.Errorf("unknown: reminal owners [rename|revoke|restore]")
	}
}

// printAmbiguous tells the user a label matched multiple devices and lists their
// ids so they can target one precisely.
func printAmbiguous(label string) {
	ids := client.OwnersWithLabel(label)
	fmt.Printf("  %d devices are labeled %q — target one by id:\n", len(ids), label)
	for _, id := range ids {
		fmt.Printf("    %s\n", id)
	}
}

func listOwners() error {
	owners, err := client.ListOwners()
	if err != nil {
		return err
	}
	if len(owners) == 0 {
		fmt.Println("  " + cBold("No owner devices yet."))
		fmt.Println("  On a device, run " + cBold("reminal own") + " to get its id, then here:")
		fmt.Println("    " + cBold("sudo reminal add owner <id>"))
		return nil
	}
	revoked, _ := client.RevokedIDs() // best-effort; nil map is fine

	type row struct {
		id, name, added, status string
		unlabeled, revoked      bool
	}
	rows := make([]row, 0, len(owners))
	wID, wName, wAdded := len("ID"), len("NAME"), len("ADDED")
	anyRevoked := false
	for _, o := range owners {
		name := o.Label
		unlabeled := name == ""
		if unlabeled {
			name = "(unlabeled)"
		}
		added := o.AddedAt
		if t, perr := time.Parse(time.RFC3339, o.AddedAt); perr == nil {
			added = t.Local().Format("Jan 2, 2006")
		}
		rv := revoked[o.Pubkey]
		anyRevoked = anyRevoked || rv
		st := "active"
		if rv {
			st = "self-revoked"
		}
		rows = append(rows, row{o.ID, name, added, st, unlabeled, rv})
		wID = maxInt(wID, len(o.ID))
		wName = maxInt(wName, visLen(name))
		wAdded = maxInt(wAdded, len(added))
	}

	fmt.Printf("  %s\n\n", cBold(fmt.Sprintf("Owner devices (%d)", len(owners))))
	fmt.Printf("  %s  %s  %s  %s\n",
		padCol("ID", wID, cDim), padCol("NAME", wName, cDim), padCol("ADDED", wAdded, cDim), cDim("STATUS"))
	for _, r := range rows {
		nameColor := func(x string) string { return x }
		if r.unlabeled {
			nameColor = cDim
		}
		statusCol := cGreen(r.status)
		if r.revoked {
			statusCol = cRed(r.status)
		}
		fmt.Printf("  %s  %s  %s  %s\n",
			padCol(r.id, wID, cBold), padCol(r.name, wName, nameColor), padCol(r.added, wAdded, cDim), statusCol)
	}
	if anyRevoked {
		fmt.Println("\n  " + cRed("A self-revoked device has no PIN-free access until you run:"))
		fmt.Println("    " + cBold("reminal owners restore <id|label>"))
	}
	return nil
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
