// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Harshal Gajjar

package client

import (
	"net"
	"syscall"
	"testing"
)

// A relay nobody is listening on is unreachable, on every OS — the error a
// real dial returns is what is checked, not a hand-made one.
func TestRelayUnreachableOnARealRefusedDial(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close() // nothing listens there now
	_, err = net.Dial("tcp", addr)
	if err == nil {
		t.Skip("something is listening on a just-freed port")
	}
	if !isRelayUnreachable(err) {
		t.Errorf("a refused dial is not taken for unreachable: %v (%T)", err, err)
	}
}

// Windows' Winsock codes, as a dial on Windows returns them.
func TestRelayUnreachableWinsockCodes(t *testing.T) {
	for _, e := range []syscall.Errno{10061, 10051, 10065} {
		if !isRelayUnreachable(&net.OpError{Op: "dial", Net: "tcp", Err: e}) {
			t.Errorf("errno %d not taken for unreachable", e)
		}
	}
}
