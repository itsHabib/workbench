package fleet

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"
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
		lines = append(lines, fmt.Sprintf("[fleet] waiting %s on: %q", FmtAge(now.Sub(w.start).Seconds()), w.label))
	}
	return lines
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
