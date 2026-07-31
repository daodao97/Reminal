// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Harshal Gajjar

package config

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// Settings holds the small set of persistent, user-toggleable preferences
// shared by every session on the machine, stored at ~/.reminal/settings.json.
type Settings struct {
	// StayUnlocked keeps the host's display awake for the whole session so it
	// can't idle-lock. A locked Mac drops synthetic input, so without this you
	// can leave reminal running, walk away, and find remote window control dead
	// on return. Costs a lit screen; can't beat a closed lid (clamshell sleep).
	StayUnlocked bool `json:"stay_unlocked"`
	// ClosedLid is "leave & forget" mode: the settings page disables lid-close
	// sleep via `sudo pmset -a disablesleep` when this is toggled, and running
	// agents watch the display census — when the machine goes fully headless
	// (lid shut, no monitor) they spin up a virtual display so windows keep a
	// coordinate space and remote view/control keep working. Off by default:
	// it costs battery when unplugged and a closed hot laptop must not be
	// bagged, so it's an explicit opt-in.
	ClosedLid bool `json:"closed_lid,omitempty"`
}

func settingsPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".reminal", "settings.json"), nil
}

// LoadSettings returns the saved settings, or the zero value if none exist or
// the file can't be read/parsed.
func LoadSettings() Settings {
	var s Settings
	p, err := settingsPath()
	if err != nil {
		return s
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return s
	}
	_ = json.Unmarshal(data, &s)
	return s
}

// SaveSettings persists settings to ~/.reminal/settings.json (0600).
func SaveSettings(s Settings) error {
	p, err := settingsPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p, data, 0o600)
}
