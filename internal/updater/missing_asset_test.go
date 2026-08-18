// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Harshal Gajjar

package updater

import (
	"errors"
	"testing"
)

// A release is built one platform per job, so any single job failing publishes
// a release carrying every other platform's binary and none for yours. That is
// not the same thing as being up to date, and the two used to be reported
// identically: "reminal is already up to date", with the newer tag sitting
// right there unmentioned. Nothing would ever prompt anyone to look.
//
// This happened for real on v3.0.8 — a rate-limited job left the darwin/arm64
// build missing while the other five published — and the silence sent the
// investigation towards a redirect-propagation theory instead of the release.
func TestMissingPlatformAssetIsNotSilence(t *testing.T) {
	if errNoAssetForPlatform == nil {
		t.Fatal("no way to distinguish a missing build from being current")
	}
	if !errors.Is(errNoAssetForPlatform, errNoAssetForPlatform) {
		t.Fatal("sentinel is not matchable by errors.Is")
	}

	// It must not read as a hard failure either: nothing is wrong with the
	// installation, so an exit code and a "check for updates: …" wrapper would
	// misdescribe it as badly as silence did.
	if got := errNoAssetForPlatform.Error(); got == "" {
		t.Fatal("sentinel carries no explanation")
	}
	for _, want := range []string{"platform", "release"} {
		if !contains(errNoAssetForPlatform.Error(), want) {
			t.Fatalf("message %q does not mention %q", errNoAssetForPlatform.Error(), want)
		}
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
