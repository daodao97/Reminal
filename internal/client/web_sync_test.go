// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Harshal Gajjar

package client

import (
	"crypto/sha256"
	"os"
	"strings"
	"testing"
)

// The viewer web app ships in two places that MUST stay byte-identical: the copy
// embedded in the agent (web/index.html, served for direct/LAN connections) and
// the copy the Cloudflare Worker serves as a static asset
// (cloudflare/public/index.html, what phones load over the cloud relay). There's
// no build step syncing them, so an edit to one and not the other silently makes
// the two front-ends diverge. This guard fails the moment they drift.
func TestWebIndexCopiesInSync(t *testing.T) {
	const embedded = "web/index.html"
	const worker = "../../cloudflare/public/index.html"

	a, err := os.ReadFile(embedded)
	if err != nil {
		t.Fatalf("read %s: %v", embedded, err)
	}
	b, err := os.ReadFile(worker)
	if err != nil {
		if os.IsNotExist(err) {
			t.Skipf("%s not present in this checkout — nothing to compare", worker)
		}
		t.Fatalf("read %s: %v", worker, err)
	}
	if sha256.Sum256(a) != sha256.Sum256(b) {
		t.Fatalf("%s and %s have diverged (%d vs %d bytes).\n"+
			"After editing the viewer, copy it across:\n"+
			"  cp %s %s", embedded, worker, len(a), len(b), embedded, worker)
	}
}

// These are event-order contracts in the shipped viewer, not styling details:
// the dropdown listener must run before a pane fences clicks, and a two-finger
// swipe must preserve both local pan and edge-chained remote scrolling.
func TestPaneGestureContracts(t *testing.T) {
	b, err := os.ReadFile("web/index.html")
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, required := range []string{
		"runs in capture phase so pane click isolation cannot hide an outside tap",
		"}, true);\n    // While the Host dropdown is open",
		"mode: null, scaled: false",
		"const residualX = fingerDX - (pane.panX - beforeX)",
		"queueScroll(f.fx, f.fy, -residualX * scaleX, -residualY * scaleY)",
		"targets here before preventing touchmove's browser default",
	} {
		if !strings.Contains(s, required) {
			t.Fatalf("viewer is missing pane gesture contract %q", required)
		}
	}
	if strings.Contains(s, "mode: pane.zoom > 1.001 ? 'view' : null") {
		t.Fatal("zoomed panes still force local-only view mode; remote edge scrolling is unreachable")
	}
}
