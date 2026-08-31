package config

import "testing"

func withCloudDefaults(t *testing.T, relay, web string) {
	t.Helper()
	oldRelay, oldWeb := DefaultCloudRelay, DefaultCloudWeb
	DefaultCloudRelay, DefaultCloudWeb = relay, web
	t.Cleanup(func() {
		DefaultCloudRelay, DefaultCloudWeb = oldRelay, oldWeb
	})
	t.Setenv("REMINAL_RELAY", "")
	t.Setenv("REMINAL_WEB", "")
	t.Setenv("REMINAL_LOCAL", "")
}

func TestUpstreamDefaultsRemainUsable(t *testing.T) {
	withCloudDefaults(t,
		"wss://reminal-relay.futuristic.workers.dev/ws",
		"https://reminal-relay.futuristic.workers.dev")
	if got := RelayWS(); got != "wss://reminal-relay.futuristic.workers.dev/ws" {
		t.Fatalf("RelayWS() = %q", got)
	}
	if got := WebURL(); got != "https://reminal-relay.futuristic.workers.dev" {
		t.Fatalf("WebURL() = %q", got)
	}
}

func TestRuntimeRelayDerivesWebURL(t *testing.T) {
	withCloudDefaults(t, "wss://compiled.example/ws", "https://compiled.example")
	t.Setenv("REMINAL_RELAY", "wss://mine.example/prefix/ws/")
	if got := RelayWS(); got != "wss://mine.example/prefix/ws" {
		t.Fatalf("RelayWS() = %q", got)
	}
	if got := WebURL(); got != "https://mine.example/prefix" {
		t.Fatalf("WebURL() = %q, want runtime relay counterpart", got)
	}
}

func TestRuntimeWebDerivesRelayURL(t *testing.T) {
	withCloudDefaults(t, "wss://compiled.example/ws", "https://compiled.example")
	t.Setenv("REMINAL_WEB", "http://127.0.0.1:8787/base/")
	if got := WebURL(); got != "http://127.0.0.1:8787/base" {
		t.Fatalf("WebURL() = %q", got)
	}
	if got := RelayWS(); got != "ws://127.0.0.1:8787/base/ws" {
		t.Fatalf("RelayWS() = %q, want runtime web counterpart", got)
	}
}

func TestSingleBuildDefaultDerivesCounterpart(t *testing.T) {
	t.Run("relay", func(t *testing.T) {
		withCloudDefaults(t, "wss://relay.example/ws", "")
		if got := WebURL(); got != "https://relay.example" {
			t.Fatalf("WebURL() = %q", got)
		}
	})
	t.Run("web", func(t *testing.T) {
		withCloudDefaults(t, "", "https://relay.example")
		if got := RelayWS(); got != "wss://relay.example/ws" {
			t.Fatalf("RelayWS() = %q", got)
		}
	})
}

func TestLocalRelay(t *testing.T) {
	withCloudDefaults(t, "wss://compiled.example/ws", "https://compiled.example")
	t.Setenv("REMINAL_LOCAL", "1")
	if got := RelayWS(); got != DefaultLocalRelay {
		t.Fatalf("RelayWS() = %q, want %q", got, DefaultLocalRelay)
	}
	if got := WebURL(); got != DefaultLocalWeb {
		t.Fatalf("WebURL() = %q, want %q", got, DefaultLocalWeb)
	}
}
