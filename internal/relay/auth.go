// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Harshal Gajjar

package relay

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
)

// authState tracks who has authenticated on a room.
//
// The relay intentionally does NO PIN verification of its own. A 6-digit PIN
// it could check would be offline-brute-forceable, and — worse — a relay that
// knew the PIN could unblind both ephemeral keys and MITM the EKE. So there is
// no failure counter / lockout here: viewers authenticate END-TO-END via the
// EKE (a wrong PIN fails the AES-GCM session-key unwrap), and the only online
// brute-force surface — forged kex handshakes against the agent — is bounded by
// the agent's own kex throttle. The relay merely records that an agent proved
// control of the session (via its pin_hash / reattach token) so it won't route
// a viewer into a session no agent is holding.
type authState struct {
	pinHash     string // legacy credential; superseded by token (see server.go)
	token       string // high-entropy reattach credential; empty on legacy sessions
	agentAuthed bool
	// ownerHash, on a room for a session ID that was cleared, is the hash of
	// the credential that had it (credentialHash); only that may claim it.
	ownerHash string
}

// credentialHash is what is kept of an agent's credential once its room is
// cleared: SHA-256 of the token, or of the legacy pin_hash. "" for neither.
func credentialHash(token, pinHash string) string {
	switch {
	case token != "":
		return hashHex("token:" + token)
	case pinHash != "":
		return hashHex("pin_hash:" + pinHash)
	}
	return ""
}

// credentialMatches reports whether an agent presenting token / pinHash is
// the one whose credential hashed to want.
func credentialMatches(want, token, pinHash string) bool {
	for _, h := range []string{credentialHash(token, ""), credentialHash("", pinHash)} {
		if h != "" && subtle.ConstantTimeCompare([]byte(h), []byte(want)) == 1 {
			return true
		}
	}
	return false
}

func hashHex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}
