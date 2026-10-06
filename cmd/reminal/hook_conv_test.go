// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Harshal Gajjar

package main

import "testing"

// The id is typed into a shell on restore: only an id-shaped value is taken.
func TestConvFromPayload(t *testing.T) {
	cases := map[string]string{
		`{"session_id":"3f2c9a10-7d4e-4b8a-9c11-0e5d6f7a8b9c","hook_event_name":"Stop"}`: "3f2c9a10-7d4e-4b8a-9c11-0e5d6f7a8b9c",
		`{"conversation_id":"chat_0123456789"}`:                                          "chat_0123456789",
		`{"conversationId":"agy_conversation_0042"}`:                                     "agy_conversation_0042",
		`{"session_id":"x; rm -rf ~"}`:                                                   "",
		`{"session_id":"$(curl evil)"}`:                                                  "",
		`{"session_id":"short"}`:                                                         "",
		`not json`:                                                                       "",
		`{}`:                                                                             "",
	}
	for in, want := range cases {
		if got := convFromPayload([]byte(in)); got != want {
			t.Errorf("%s: got %q, want %q", in, got, want)
		}
	}
}
