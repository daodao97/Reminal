// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Harshal Gajjar

package client

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// The viewer turns text in the terminal into links it will open. Only
// http(s) is a link: anything else a program prints is text.
func TestTerminalLinksAreHTTPOnly(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed")
	}
	src, err := os.ReadFile("web/index.html")
	if err != nil {
		t.Fatal(err)
	}
	s := string(src)
	start := strings.Index(s, "const reminalLinks = (function () {")
	if start < 0 {
		t.Fatal("reminalLinks is no longer where this test looks for it — fix the test, do not delete it")
	}
	end := strings.Index(s[start:], "\n    })();\n")
	if end < 0 {
		t.Fatal("reminalLinks' end is no longer where this test looks for it")
	}
	block := s[start : start+end+len("\n    })();\n")]
	js := block + `
const rows = (t) => [{ text: t, wrapped: false }];
const found = (t) => reminalLinks.linksInRows(rows(t), 0).map(k => k.url);
const out = {};
for (const t of [
  'run javascript:alert(1) now', 'see data:text/html,<b>x</b>', 'vbscript:MsgBox(1)',
  'JaVaScRiPt:alert(1)', '   javascript:alert(1)', 'file:///etc/passwd',
  '\u001b]8;;javascript:alert(1)\u0007click\u001b]8;;\u0007', 'ftp://example.com/x',
]) out[t] = found(t);
out['https://example.com/a'] = found('open https://example.com/a now');
out['http://example.com'] = found('http://example.com');
process.stdout.write(JSON.stringify(out));
`
	out, err := exec.Command(node, "-e", js).CombinedOutput()
	if err != nil {
		t.Fatalf("node: %v\n%s", err, out)
	}
	var res map[string][]string
	if err := json.Unmarshal(out, &res); err != nil {
		t.Fatalf("node output: %v\n%s", err, out)
	}
	for text, links := range res {
		switch text {
		case "https://example.com/a", "http://example.com":
			if len(links) != 1 || links[0] != text {
				t.Errorf("%q: found %v, want itself", text, links)
			}
		default:
			if len(links) != 0 {
				t.Errorf("%q was offered as a link: %v", text, links)
			}
		}
	}
}

func TestIsNotReady(t *testing.T) {
	if isNotReady(nil) {
		t.Fatal("nil error is not 'not ready'")
	}
	for _, m := range []string{"session not ready", "session not found or not ready", "auth: session not ready"} {
		if !isNotReady(errors.New(m)) {
			t.Errorf("%q should be temporary", m)
		}
	}
	if isNotReady(errors.New("handshake failed: PIN mismatch")) {
		t.Fatal("a PIN mismatch is not temporary")
	}
}
