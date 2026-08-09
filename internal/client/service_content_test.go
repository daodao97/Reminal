// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Harshal Gajjar

package client

import (
	"encoding/xml"
	"io"
	"strings"
	"testing"
)

// launchdPlist must be well-formed XML and carry the exact ProgramArguments the
// daemon needs. These are string-templated, so a stray character or a dropped key
// would otherwise only blow up at install time on a real Mac.
func TestLaunchdPlistWellFormed(t *testing.T) {
	plist := launchdPlist("/usr/local/bin/reminal", "/home/u/.reminal/daemon.log")

	// Well-formed XML (parse every token to EOF).
	dec := xml.NewDecoder(strings.NewReader(plist))
	for {
		_, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("plist is not well-formed XML: %v\n%s", err, plist)
		}
	}

	for _, want := range []string{
		"<string>" + daemonLabel + "</string>",
		"<string>/usr/local/bin/reminal</string>",
		"<string>daemon</string>",
		"<key>RunAtLoad</key><true/>",
		"<key>KeepAlive</key><true/>",
	} {
		if !strings.Contains(plist, want) {
			t.Errorf("plist missing %q\n%s", want, plist)
		}
	}
	// Must NOT be ProcessType=Background: it clamps the daemon (and the sessions it
	// spawns) to throttled efficiency-core QoS — the cause of ~5fps streaming and
	// sluggish "+"-spawned sessions. Guard against it creeping back.
	if strings.Contains(plist, "ProcessType") {
		t.Errorf("plist must not set ProcessType (throttles spawned sessions)\n%s", plist)
	}
}

// A path with XML metacharacters must be escaped, and the plist must stay
// well-formed — a home dir like /home/a&b or a quote in a path would break it.
func TestLaunchdPlistEscapesPath(t *testing.T) {
	plist := launchdPlist(`/opt/a&b/re"m'l<>`, "/log")
	if strings.Contains(plist, `a&b`) && !strings.Contains(plist, `a&amp;b`) {
		t.Errorf("ampersand not escaped:\n%s", plist)
	}
	if err := xml.Unmarshal([]byte(plist), new(struct {
		XMLName xml.Name
	})); err != nil {
		t.Fatalf("escaped plist not well-formed: %v", err)
	}
}

// systemdUnit must run the daemon and — critically — carry KillMode=process, or
// restarting the daemon would SIGKILL every "+"-spawned session sharing its
// cgroup. This is the regression guard for that fix.
func TestSystemdUnitHasKillModeProcess(t *testing.T) {
	unit := systemdUnit("/usr/bin/reminal")
	for _, want := range []string{
		"ExecStart=/usr/bin/reminal daemon",
		"KillMode=process",
		"Restart=always",
		"WantedBy=default.target",
	} {
		if !strings.Contains(unit, want) {
			t.Errorf("systemd unit missing %q\n%s", want, unit)
		}
	}
}

func TestXMLEscape(t *testing.T) {
	cases := map[string]string{
		"plain": "plain",
		"a&b":   "a&amp;b",
		"<x>":   "&lt;x&gt;",
		`"q"`:   "&quot;q&quot;",
		"it's":  "it&apos;s",
	}
	for in, want := range cases {
		if got := xmlEscape(in); got != want {
			t.Errorf("xmlEscape(%q) = %q, want %q", in, got, want)
		}
	}
}
