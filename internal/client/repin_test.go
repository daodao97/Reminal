// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Harshal Gajjar

package client

import "testing"

// A plain restart must hand the successor the PIN it already has; a repin
// must hand it the replacement. Both restarts read the PIN through here.
func TestCarriedPIN(t *testing.T) {
	a := &Agent{pin: "111111", pinHash: "old-hash", token: "tok"}
	if got := a.carriedPIN(); got != "111111" {
		t.Fatalf("restart carries %q, want the current PIN", got)
	}
	if got := a.carriedPinHash(); got != "old-hash" {
		t.Fatalf("restart carries hash %q, want the current one", got)
	}
	a.nextPIN, a.nextPinHash = "222222", "new-hash"
	if got := a.carriedPIN(); got != "222222" {
		t.Fatalf("repin carries %q, want the replacement PIN", got)
	}
	if got := a.carriedPinHash(); got != "new-hash" {
		t.Fatalf("repin carries hash %q, want the replacement", got)
	}
}

// A session that has not finished moving to its token still proves itself to
// the relay with its original hash; replacing that would orphan the session.
func TestCarriedPinHashKeptWhileMigrating(t *testing.T) {
	a := &Agent{pin: "111111", pinHash: "old-hash", token: "tok", sendPinHash: true}
	a.nextPIN, a.nextPinHash = "222222", "new-hash"
	if got := a.carriedPinHash(); got != "old-hash" {
		t.Fatalf("migrating session carries hash %q, want the original", got)
	}
}
