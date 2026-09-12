package fleet

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// MailSnapshot bounds observational reads. A partial scan cannot establish total
// or unacknowledged counts, nor claim the returned rows are the newest in the store.
func MailSnapshot(tenant, address string) ([]Rec, bool, error) {
	box, dirs, err := mailStore(tenant, address)
	if err != nil {
		return nil, false, err
	}
	rows := []Rec{}
	partial := false
	entriesLeft, bytesLeft := 512, 2<<20
	seen := map[string]bool{}
	for _, dir := range dirs {
		records, cut, err := snapshotDir(box, dir, &entriesLeft, &bytesLeft, seen)
		if err != nil {
			return nil, false, err
		}
		rows = append(rows, records...)
		partial = partial || cut
	}
	sort.SliceStable(rows, func(i, j int) bool { return F(rows[i], "at") > F(rows[j], "at") })
	return rows, partial, nil
}

func snapshotDir(box Mailbox, dir string, entriesLeft, bytesLeft *int, seen map[string]bool) ([]Rec, bool, error) {
	f, err := os.Open(dir)
	if os.IsNotExist(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	defer f.Close()
	entries, err := f.ReadDir(*entriesLeft + 1)
	if err != nil && err != io.EOF {
		return nil, false, err
	}
	partial := len(entries) > *entriesLeft
	entries = entries[:min(len(entries), *entriesLeft)]
	*entriesLeft -= len(entries)
	rows := []Rec{}
	for _, entry := range entries {
		if entry.IsDir() || entry.Name() == mailAddressFile || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), ".json")
		if !mailID.MatchString(id) {
			return nil, false, fmt.Errorf("mail: invalid retained id")
		}
		if seen[id] {
			return nil, false, fmt.Errorf("mail %s: duplicate retained id", id)
		}
		seen[id] = true
		rec, cut, err := snapshotRecord(box, filepath.Join(dir, entry.Name()), id, dir == box.legacyDir(), bytesLeft)
		if err != nil {
			return nil, false, err
		}
		partial = partial || cut
		if rec != nil {
			rows = append(rows, rec)
		}
	}
	return rows, partial, nil
}

func snapshotRecord(box Mailbox, path, id string, legacy bool, bytesLeft *int) (Rec, bool, error) {
	if *bytesLeft <= 0 {
		return nil, true, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, false, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return nil, false, fmt.Errorf("mail %s: not a readable regular record", id)
	}
	limit := min(64<<10, *bytesLeft)
	raw, err := io.ReadAll(io.LimitReader(f, int64(limit+1)))
	*bytesLeft -= len(raw)
	if err != nil {
		return nil, false, err
	}
	if len(raw) > limit {
		return nil, true, nil
	}
	var rec Rec
	if err := json.Unmarshal(raw, &rec); err != nil {
		return nil, false, fmt.Errorf("mail %s: unreadable record", id)
	}
	if S(rec, "id") != id || S(rec, "to") != box.Address || F(rec, "at") <= 0 {
		return nil, false, fmt.Errorf("mail %s: damaged record or address collision", id)
	}
	if !legacy && (S(rec, "tenant") != box.Tenant || S(rec, "to_kind") != box.Kind) {
		return nil, false, fmt.Errorf("mail %s: damaged tenant or address kind", id)
	}
	return rec, false, nil
}
