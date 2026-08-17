package main

import "strings"

// sanitizeForTerminal strips terminal control sequences from text that came
// from GitHub. Thread bodies, authors and file paths are all attacker-
// influenced: an ESC sequence in any of them can reposition the cursor,
// recolour, or erase lines already written. This particular output is the
// evidence an operator reads before deciding whether to resolve a finding, so
// a thread that can repaint it can hide the very thing being judged — which is
// worth more here than in ordinary CLI output.
//
// Everything below space goes, except tab. Newlines go too: each line is
// printed under its own "  | " prefix, so an embedded newline would let thread
// text forge a line that looks like gate's own. DEL and the C1 block (0x80-
// 0x9f, where ESC-less CSI lives) go for the same reason.
func sanitizeForTerminal(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\t' {
			return r
		}
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f) {
			return -1
		}
		return r
	}, s)
}
