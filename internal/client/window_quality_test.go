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

func TestLegacyViewerKeepsExistingCaptureProfile(t *testing.T) {
	// Viewers predating quality requests send no profile. Keep their capture
	// behavior unchanged until updated viewer HTML explicitly asks to sharpen.
	got := (windowQuality{}).normalized()
	want := windowQuality{MaxWidth: winMaxWidth, Quality: winCaptureQuality}
	if got != want {
		t.Fatalf("legacy viewer profile = %+v, want %+v", got, want)
	}
}
