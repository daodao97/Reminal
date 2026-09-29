// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Harshal Gajjar

//go:build darwin || linux

package client

import (
	"os/exec"
	"reflect"
	"testing"
	"time"
)

// A value with a space in it (`--append-system-prompt "be brief"`) is one
// argument, and has to come back as one to be carried over on a resume.
func TestProcessArgsKeepsArgumentsWhole(t *testing.T) {
	cmd := exec.Command("/bin/sh", "-c", "sleep 30; :", "be brief; really", "--model=x y") // two commands: sh does not exec away
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill(); _, _ = cmd.Process.Wait() }()
	want := []string{"/bin/sh", "-c", "sleep 30; :", "be brief; really", "--model=x y"}
	time.Sleep(300 * time.Millisecond) // well after sh has settled
	if got := processArgs(cmd.Process.Pid); !reflect.DeepEqual(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
}
