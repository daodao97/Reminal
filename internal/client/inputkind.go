// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Harshal Gajjar

package client

import "regexp"

// terminalReplyRe matches what a terminal sends on its own, not a person:
// answers to the program's queries (cursor position, device attributes,
// status, colours, version) and focus in/out. A program that asks every
// prompt (fish), or a window switched to and from, would otherwise read as
// someone typing into a tab nobody is at.
var terminalReplyRe = regexp.MustCompile(
	`\x1b\[\d*(;\d*)?R` + // cursor position report
		`|\x1b\[[?>=]?[\d;]*c` + // device attributes
		`|\x1b\[\??\d*(;\d*)*n` + // device status report
		`|\x1b\[[IO]` + // focus in / out
		`|\x1b\][^\x07\x1b]*(\x07|\x1b\\)` + // OSC reply (colour queries …)
		`|\x1bP[^\x1b]*\x1b\\`) // DCS reply (version, settings)

// typedByPerson says whether keys from a viewer or the host terminal hold
// anything a person sent, once the terminal's own replies are taken out.
func typedByPerson(b []byte) bool {
	return len(terminalReplyRe.ReplaceAll(b, nil)) > 0
}
