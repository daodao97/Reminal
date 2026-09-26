package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func targetNamed(t *testing.T, bin string) agentTarget {
	t.Helper()
	for _, tg := range agentTargets() {
		if tg.Bin == bin {
			return tg
		}
	}
	t.Fatalf("no target %q", bin)
	return agentTarget{}
}

func writeJSON(t *testing.T, path string, v any) {
	t.Helper()
	raw, _ := json.Marshal(v)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestCheckReadsBackWhatIntegrateWrote round-trips through the real writers:
// whatever applyViaFile/applyHooks put down, checkIntegration must see — so a
// change to one format cannot silently make --check lie.
func TestCheckReadsBackWhatIntegrateWrote(t *testing.T) {
	home := t.TempDir()
	exe := filepath.Join(home, "reminal")
	if err := os.WriteFile(exe, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, bin := range []string{"gemini", "qwen", "opencode", "cursor-agent", "amp"} {
		tg := targetNamed(t, bin)
		before := checkIntegration(tg, home, exe)
		if before.Integrated || !before.Known || before.ConfigMtime != 0 {
			t.Fatalf("%s before: %+v", bin, before)
		}
		if err := applyViaFile(tg, home, exe, false); err != nil {
			t.Fatalf("%s apply: %v", bin, err)
		}
		if tg.hooks != nil {
			if err := applyHooks(tg.hooks, home, exe, false); err != nil {
				t.Fatalf("%s hooks: %v", bin, err)
			}
		}
		after := checkIntegration(tg, home, exe)
		if !after.Integrated || !after.Current || after.ConfigMtime == 0 || after.Error != "" {
			t.Fatalf("%s after apply: %+v", bin, after)
		}
		if tg.hooks != nil && !after.Hooks {
			t.Fatalf("%s: hooks written but not seen: %+v", bin, after)
		}
		if after.Restart == "" {
			t.Fatalf("%s: no restart hint", bin)
		}
		// A registration left by another reminal is set up, but not for us.
		other := checkIntegration(tg, home, filepath.Join(home, "elsewhere", "reminal"))
		if !other.Integrated || other.Current {
			t.Fatalf("%s other exe: %+v", bin, other)
		}
		if err := applyViaFile(tg, home, exe, true); err != nil {
			t.Fatal(err)
		}
		if tg.hooks != nil {
			_ = applyHooks(tg.hooks, home, exe, true)
		}
		gone := checkIntegration(tg, home, exe)
		if gone.Integrated || gone.Hooks {
			t.Fatalf("%s after remove: %+v", bin, gone)
		}
	}
}

// TestCheckReadsCLIRouteConfigs covers the agents whose own `mcp add` writes
// the file: we read the formats they produce (fixtures copied from real
// installs), not something we wrote ourselves.
func TestCheckReadsCLIRouteConfigs(t *testing.T) {
	home := t.TempDir()
	exe := "/home/u/.local/bin/reminal"

	writeJSON(t, filepath.Join(home, ".claude.json"), map[string]any{
		"numStartups": 3,
		"mcpServers":  map[string]any{"reminal": map[string]any{"type": "stdio", "command": exe, "args": []string{"mcp"}}},
	})
	claude := checkIntegration(targetNamed(t, "claude"), home, exe)
	if !claude.Integrated || !claude.Current || claude.Hooks || !claude.HooksWanted {
		t.Fatalf("claude: %+v", claude)
	}
	if err := applyHooks(targetNamed(t, "claude").hooks, home, exe, false); err != nil {
		t.Fatal(err)
	}
	if c := checkIntegration(targetNamed(t, "claude"), home, exe); !c.Hooks {
		t.Fatalf("claude hooks not seen: %+v", c)
	}

	writeJSON(t, filepath.Join(home, ".gemini", "config", "mcp_config.json"), map[string]any{
		"mcpServers": map[string]any{"reminal": map[string]any{"args": []string{"mcp"}, "command": exe, "disabled": false}},
	})
	agy := checkIntegration(targetNamed(t, "agy"), home, exe)
	if !agy.Integrated || !agy.Current {
		t.Fatalf("agy: %+v", agy)
	}

	toml := "model = \"o3\"\n\n[mcp_servers.reminal]\ncommand = \"" + exe + "\"\nargs = [\"mcp\"]\n\n[mcp_servers.other]\ncommand = \"/x\"\n"
	if err := os.MkdirAll(filepath.Join(home, ".codex"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".codex", "config.toml"), []byte(toml), 0o600); err != nil {
		t.Fatal(err)
	}
	codex := checkIntegration(targetNamed(t, "codex"), home, exe)
	if !codex.Integrated || !codex.Current {
		t.Fatalf("codex: %+v", codex)
	}
	stale := checkIntegration(targetNamed(t, "codex"), home, "/opt/reminal")
	if !stale.Integrated || stale.Current {
		t.Fatalf("codex stale: %+v", stale)
	}

	// Broken JSON is an error, not "not set up": integrate refuses to touch
	// such a file, so the answer must not be "run integrate".
	if err := os.WriteFile(filepath.Join(home, ".qwen", "settings.json"), []byte("{oops"), 0o600); err != nil {
		os.MkdirAll(filepath.Join(home, ".qwen"), 0o755)
		os.WriteFile(filepath.Join(home, ".qwen", "settings.json"), []byte("{oops"), 0o600)
	}
	qwen := checkIntegration(targetNamed(t, "qwen"), home, exe)
	if qwen.Error == "" || qwen.Integrated {
		t.Fatalf("qwen broken json: %+v", qwen)
	}
}

// TestIntegrationReportJSONShape pins the wire shape the Machines view reads.
func TestIntegrationReportJSONShape(t *testing.T) {
	home := t.TempDir()
	rep := integrationReport(home, "/x/reminal", []string{"claude"})
	if len(rep) != 1 || rep[0].Bin != "claude" {
		t.Fatalf("report: %+v", rep)
	}
	raw, _ := json.Marshal(rep)
	var back []map[string]any
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"bin", "name", "installed", "integrated", "current", "hooks", "hooks_wanted", "known", "restart"} {
		if _, ok := back[0][k]; !ok {
			t.Fatalf("missing %q in %s", k, raw)
		}
	}
}
