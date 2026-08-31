// SPDX-License-Identifier: AGPL-3.0-or-later

package client

import "testing"

func TestWindowQualityNormalized(t *testing.T) {
	tests := []struct {
		name string
		in   windowQuality
		want windowQuality
	}{
		{"defaults", windowQuality{}, windowQuality{MaxWidth: winMaxWidth, Quality: winCaptureQuality}},
		{"low bounds", windowQuality{MaxWidth: 100, Quality: 1}, windowQuality{MaxWidth: 720, Quality: 35}},
		{"high bounds", windowQuality{MaxWidth: 9000, Quality: 100}, windowQuality{MaxWidth: 2880, Quality: 82}},
		{"requested", windowQuality{MaxWidth: 1440, Quality: 64}, windowQuality{MaxWidth: 1440, Quality: 64}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.in.normalized(); got != tt.want {
				t.Fatalf("normalized() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestTuneWindowStreamKeepsNewestProfile(t *testing.T) {
	ch := make(chan windowQuality, 1)
	a := &Agent{winQuality: map[string]chan windowQuality{"w1": ch}}
	a.tuneWindowStream("w1", windowQuality{MaxWidth: 1100, Quality: 48})
	a.tuneWindowStream("w1", windowQuality{MaxWidth: 2880, Quality: 80})
	if got := <-ch; got != (windowQuality{MaxWidth: 2880, Quality: 80}) {
		t.Fatalf("queued profile = %+v, want newest", got)
	}
}

func TestAutomaticWindowQualityUsesSharperRelayTier(t *testing.T) {
	s := &winStream{a: &Agent{}, profile: (windowQuality{}).normalized()}
	s.applyAutomaticQuality()
	want := windowQuality{MaxWidth: 1920, Quality: 68}
	if s.profile != want {
		t.Fatalf("automatic relay profile = %+v, want %+v", s.profile, want)
	}

	// An explicit browser preference (for example Save-Data) must not be
	// overwritten by the host's transport heuristic.
	s.profile = windowQuality{MaxWidth: 800, Quality: 40}
	s.profileExplicit = true
	s.applyAutomaticQuality()
	if s.profile != (windowQuality{MaxWidth: 800, Quality: 40}) {
		t.Fatalf("automatic profile overwrote explicit request: %+v", s.profile)
	}
}
