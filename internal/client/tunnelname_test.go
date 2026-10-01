// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Harshal Gajjar

package client

import (
	"strings"
	"testing"
)

// The name has to reach the session record, because that record is what
// `reminal list` prints and what the Machines panel is sent.
func TestTunnelRecordCarriesTheName(t *testing.T) {
	tun := &Tunnel{sessionID: "PORT1234", port: 8080, name: "Quarterly report"}
	rec := tun.activeRecord()
	if rec.Name != "Quarterly report" {
		t.Fatalf("record name = %q, want the name the forward was given", rec.Name)
	}
	if !rec.IsPort() || rec.Port != 8080 {
		t.Fatalf("record = %+v, want a port forward on 8080", rec)
	}
}

// A nameless forward must stay nameless rather than gaining an empty label,
// so the list keeps showing it the way it always has.
func TestTunnelRecordWithoutANameIsUnchanged(t *testing.T) {
	rec := (&Tunnel{sessionID: "PORT1234", port: 8080}).activeRecord()
	if rec.Name != "" {
		t.Fatalf("record name = %q, want empty", rec.Name)
	}
}

// Names are typed by people and travel to other machines' Machines panels, so
// control characters have no business in one.
func TestTunnelNameIsSanitised(t *testing.T) {
	opts := TunnelOptions{Port: 8080, Name: "  Quarterly\x07 report\x1b[31m  "}
	got := sanitizeTitle(opts.Name)
	if strings.ContainsAny(got, "\x07\x1b") {
		t.Fatalf("sanitised name still holds control characters: %q", got)
	}
	if !strings.HasPrefix(got, "Quarterly") || strings.HasSuffix(got, " ") {
		t.Fatalf("sanitised name = %q, want it trimmed and readable", got)
	}
}
