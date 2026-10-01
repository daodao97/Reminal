// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Harshal Gajjar

package client

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// A dropped or doubled brace in the viewer's stylesheet does not fail anything
// at load: the browser quietly folds every rule after it into the still-open
// block. A merge once lost the closing brace of a `@media (max-width: 560px)`
// block, and the onboarding tour's styles, which follow it, applied only on
// narrow screens; everywhere else the tour rendered as unstyled text. Every
// <style> block must close every block it opens.
func TestViewerStyleBracesBalance(t *testing.T) {
	b, err := os.ReadFile("web/index.html")
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	comment := regexp.MustCompile(`(?s)/\*.*?\*/`)
	quoted := regexp.MustCompile(`"(?:[^"\\\n]|\\.)*"|'(?:[^'\\\n]|\\.)*'`)
	for n := 0; ; n++ {
		open := strings.Index(s, "<style>")
		if open < 0 {
			if n == 0 {
				t.Fatal("viewer has no <style> block")
			}
			return
		}
		s = s[open+len("<style>"):]
		end := strings.Index(s, "</style>")
		if end < 0 {
			t.Fatalf("<style> block %d never closes", n)
		}
		css := quoted.ReplaceAllString(comment.ReplaceAllString(s[:end], ""), `""`)
		depth := 0
		for i, line := range strings.Split(css, "\n") {
			depth += strings.Count(line, "{") - strings.Count(line, "}")
			if depth < 0 {
				t.Fatalf("<style> block %d closes a brace it never opened (line %d of the block)", n, i+1)
			}
		}
		if depth != 0 {
			t.Fatalf("<style> block %d leaves %d block(s) open: every rule after the unclosed one is scoped inside it", n, depth)
		}
		s = s[end:]
	}
}
