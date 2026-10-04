// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Harshal Gajjar

package session

import (
	"testing"

	"reminal/internal/atrest"
)

func resetAtrestCache(t *testing.T) { t.Helper(); atrest.ResetCacheForTest() }
