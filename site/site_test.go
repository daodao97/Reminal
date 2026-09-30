// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Harshal Gajjar

package site

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// Every page carries the same header and footer as the home page, byte for
// byte. There is no build step stamping them in, so an edit to one page's nav
// or footer and not the others makes the site's chrome drift page by page.
// When this fails, copy the home page's <nav>…</nav> or <footer>…</footer>
// over the page it names.
func TestPagesShareHomeNavAndFooter(t *testing.T) {
	pages, err := filepath.Glob("public/*/index.html")
	if err != nil {
		t.Fatal(err)
	}
	guides, _ := filepath.Glob("public/guides/*/index.html")
	pages = append(pages, guides...)
	if len(pages) < 3 {
		t.Fatalf("found only %d pages under public/; is the test running from site/?", len(pages))
	}
	home := read(t, "public/index.html")
	for _, part := range []struct {
		name string
		re   *regexp.Regexp
	}{
		{"nav", regexp.MustCompile(`(?s)<nav\b.*?</nav>`)},
		{"footer", regexp.MustCompile(`(?s)<footer\b.*?</footer>`)},
	} {
		want := part.re.FindString(home)
		if want == "" {
			t.Fatalf("public/index.html has no <%s>", part.name)
		}
		for _, p := range pages {
			if got := part.re.FindString(read(t, p)); got != want {
				t.Errorf("%s: <%s> differs from the home page's; copy it from public/index.html", p, part.name)
			}
		}
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
