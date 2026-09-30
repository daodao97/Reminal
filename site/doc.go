// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Harshal Gajjar

// Package site holds no code: it is the marketing site (public/ and the
// Worker in src/). It is a Go package only so that `go test ./...`, which CI
// runs, also runs site_test.go, which guards what a static site without a
// template step can silently break.
package site
