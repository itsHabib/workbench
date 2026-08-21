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
// 0x9f, where ESC-less CSI lives) go for the same reason. So do Unicode
// bidirectional and other invisible formatting controls: a U+202E right-to-
// left override in a commit subject visually reorders the rest of the line —
// including the trusted markers gate appends after it — without any byte below
// 0xa0. See isFormatControl.
func sanitizeForTerminal(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\t' {
			return r
		}
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f) {
			return -1
		}
		if isFormatControl(r) {
			return -1
		}
		return r
	}, s)
}

// isFormatControl reports the invisible Unicode formatting controls that can
// reorder or splice rendered text: the bidi embeddings/overrides and isolates
// (U+202A-202E, U+2066-2069), the directional marks and the Arabic letter mark
// (U+200E, U+200F, U+061C), and the zero-width joiners/space and BOM
// (U+200B-200D, U+2060, U+FEFF), which can hide splices inside what reads as
// one word. Visible text survives untouched — these carry no glyph, so
// dropping them never removes anything an honest subject displays.
func isFormatControl(r rune) bool {
	switch r {
	case 0x061c, 0x200b, 0x200c, 0x200d, 0x200e, 0x200f, 0x2060, 0xfeff:
		return true
	}
	return (r >= 0x202a && r <= 0x202e) || (r >= 0x2066 && r <= 0x2069)
}
