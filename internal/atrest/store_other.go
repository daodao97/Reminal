// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Harshal Gajjar

//go:build !darwin && !linux && !windows

package atrest

const caseInsensitiveFS = false

func osStore(dir, account string) store { return nil }
