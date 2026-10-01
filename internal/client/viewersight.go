// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Harshal Gajjar

package client

import "sync"

// viewerSight is which attached viewers have said their terminal is out of
// sight: the tab hidden, or another view covering it. A viewer is attached
// all the while — its socket stays open, so it can be back in an instant —
// but nobody is looking. Viewers say so on their resize reports (Away), per
// tab id, and it is kept the same way as their sizes (viewersize.go): the
// relay only tells us a count, so when anyone leaves, the book is emptied and
// whoever is still here reports again. A viewer that never says (an older
// one) is taken as looking.
type viewerSight struct {
	mu   sync.Mutex
	away map[string]bool
}

// note records one viewer's report; it says whether the count of viewers
// away changed.
func (s *viewerSight) note(id string, away bool) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	before := s.countLocked()
	if away {
		if s.away == nil {
			s.away = map[string]bool{}
		}
		s.away[id] = true
	} else {
		delete(s.away, id)
	}
	return s.countLocked() != before
}

// clear forgets every report (someone left, and we cannot tell who).
func (s *viewerSight) clear() {
	s.mu.Lock()
	s.away = nil
	s.mu.Unlock()
}

// awayOf is how many of `viewers` attached are away — never more than there
// are.
func (s *viewerSight) awayOf(viewers int) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return min(s.countLocked(), max(viewers, 0))
}

func (s *viewerSight) countLocked() int { return len(s.away) }
