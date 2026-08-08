// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Harshal Gajjar

package client

import (
	"os"
	"os/signal"
	"syscall"
	"time"
)

// RunDaemon runs this machine's directory host in the foreground until it gets
// SIGINT/SIGTERM. It's the always-on presence layer: a machine's directory host
// otherwise lives only inside a running session, so a machine with no live
// session drops off `reminal machines` entirely and can't be spawned onto (the
// per-machine "+" sends its request to a host that isn't there). The login
// service (see InstallDaemonService) runs this so an OWNED machine stays
// reachable — listable and "+"-spawnable — even while idle.
//
// It shares the machine-local single-host flock with any session-embedded hosts,
// so exactly one process ever serves the channel; whichever holds the lock
// answers from the same on-disk session registry, so it doesn't matter which. A
// no-op while the machine is unowned (runDirectoryHost re-checks periodically),
// so it's harmless to leave running after every owner is revoked.
func RunDaemon() error {
	stop := make(chan struct{})
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sig
		close(stop)
	}()
	go watchBinaryAndExit(stop)
	runDirectoryHost(stop)
	return nil
}

// binaryWatchInterval is how often the daemon checks whether its own executable
// changed on disk. Upgrades are rare, so a coarse poll is plenty.
const binaryWatchInterval = 30 * time.Second

// watchBinaryAndExit exits the daemon when its own on-disk binary is replaced, so
// the service manager (launchd KeepAlive / systemd Restart=always) restarts it
// onto the new image. `reminal upgrade` and `restart --all` bounce the service
// explicitly, but this backstops every OTHER path that swaps the binary without
// telling us — most importantly a background *critical* (security) auto-update,
// after which a long-lived daemon would otherwise keep serving from the old,
// still-vulnerable binary. The upgrade is an atomic rename, so a stat always sees
// either the whole old or the whole new file — never a partial write.
func watchBinaryAndExit(stop <-chan struct{}) {
	exe, err := os.Executable()
	if err != nil {
		return
	}
	base, err := os.Stat(exe)
	if err != nil {
		return
	}
	t := time.NewTicker(binaryWatchInterval)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			fi, err := os.Stat(exe)
			if err != nil {
				continue // transient (mid-rename or briefly absent) — re-check next tick
			}
			if fi.Size() != base.Size() || !fi.ModTime().Equal(base.ModTime()) {
				// Replaced (e.g. by an upgrade). Exit cleanly; the service manager
				// starts a fresh instance from the new binary. Flock and sockets are
				// released by the OS on exit, so no cleanup is needed here.
				os.Exit(0)
			}
		}
	}
}
