// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Harshal Gajjar

package client

import (
	"crypto/sha256"
	"os"
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
