// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Harshal Gajjar

// Package cloudflare holds no code: it is the relay Worker (src/) and the web
// viewer (public/). It is a Go package only so that `go test ./...`, which CI
// runs, also runs worker_test.go — the same arrangement as site/.
package cloudflare
