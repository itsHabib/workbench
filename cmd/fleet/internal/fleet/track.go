package fleet

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

type waitAnchor struct {
	label string
	start time.Time
}

// waitLines reads the optional named waits anchored by track.py. Their clock starts
// at the anchor, independent of Stop or any tick. Labels are quoted data; the hook
// neither interprets them nor runs the anchor's optional check command.
func waitLines(sid string, now time.Time) []string {
	if sid == "" || sid == "unknown" {
		return nil
	}
	dir := expand(envOr("FLEET_TRACK_DIR", "~/.claude/track"))
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var waits []waitAnchor
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		if w, ok := readWait(filepath.Join(dir, e.Name()), sid, now); ok {
			waits = append(waits, w)
		}
	}
	sort.SliceStable(waits, func(i, j int) bool { return waits[i].start.Before(waits[j].start) })
	var lines []string
	for i, w := range waits {
		if i == 3 {
			lines = append(lines, fmt.Sprintf("[fleet] +%d more active waits (track list shows their elapsed time)", len(waits)-i))
			break
		}
		lines = append(lines, fmt.Sprintf("[fleet] waiting %s on: %s", FmtAge(now.Sub(w.start).Seconds()), quoteWaitLabel(w.label)))
	}
	return lines
}

// quoteWaitLabel bounds the rendered label, including escape expansion. Reserve
// space for an ellipsis and only append whole escapes, keeping the quote valid.
func quoteWaitLabel(label string) string {
	quotedLabel := strconv.Quote(label)
	if utf8.RuneCountInString(quotedLabel) <= 162 { // 160 label runes plus quotes
		return quotedLabel
	}
	var b strings.Builder
	used := 0
	for _, c := range label {
		quoted := strconv.Quote(string(c))
		part := quoted[1 : len(quoted)-1]
		n := utf8.RuneCountInString(part)
		if used+n > 159 {
			b.WriteRune('…')
			break
		}
		b.WriteString(part)
		used += n
	}
	return `"` + b.String() + `"`
}

func readWait(path, sid string, now time.Time) (waitAnchor, bool) {
	r := ReadJSON(path)
	if S(r, "session") != sid {
		return waitAnchor{}, false
	}
	start, err := time.Parse(time.RFC3339Nano, S(r, "started_at"))
	if err != nil || start.After(now) {
		return waitAnchor{}, false
	}
	text := strings.Map(func(c rune) rune {
		if unicode.IsControl(c) {
			return ' '
		}
		return c
	}, S(r, "label"))
	label := []rune(strings.Join(strings.Fields(text), " "))
	if len(label) == 0 {
		return waitAnchor{}, false
	}
	if len(label) > 160 {
		label = append(label[:160], '…')
	}
	return waitAnchor{label: string(label), start: start}, true
}
