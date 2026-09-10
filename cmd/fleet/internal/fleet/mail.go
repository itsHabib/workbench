package fleet

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/itsHabib/workbench/filelock"
)

var mailID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
var mailRole = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)

// MailAddress validates path components before they enter the store.
func MailAddress(role, id string) error {
	if !mailRole.MatchString(role) || !mailID.MatchString(id) {
		return fmt.Errorf("mail: invalid role or id (use letters, digits, . _ -; role also permits :)")
	}
	return nil
}

// MailPath is the record's filename. Callers validate its components first.
func MailPath(role, id string) string { return Path("mail", Safe(role), id+".json") }

// MailLock serializes send, ack and delivery under watch/.
func MailLock(fn func() error) error {
	if ReadOnly {
		return fmt.Errorf("mail: state is read-only")
	}
	p := Path("watch", "mail.lock")
	if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
		return err
	}
	f, err := os.OpenFile(p, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return err
	}
	defer f.Close()
	for i := 0; i < 60; i++ {
		if err := filelock.TryLock(f); err == nil {
			defer func() { _ = filelock.Unlock(f) }()
			return fn()
		}
		time.Sleep(20 * time.Millisecond)
	}
	return ErrKeyBusy
}

// ReadMail distinguishes absent from damaged records and refuses filename aliases.
func ReadMail(role, id string) (Rec, error) {
	if err := MailAddress(role, id); err != nil {
		return nil, err
	}
	b, err := os.ReadFile(MailPath(role, id))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if err := checkMailAddress(role, false); err != nil {
		return nil, err
	}
	var r Rec
	if err = json.Unmarshal(b, &r); err != nil {
		return nil, fmt.Errorf("mail %s: unreadable: %w", id, err)
	}
	if S(r, "to") != role || S(r, "id") != id || F(r, "at") <= 0 {
		return nil, fmt.Errorf("mail %s: damaged record or address collision", id)
	}
	return r, nil
}

// PutMail publishes once, retaining original timestamps and stamps on retries.
func PutMail(payload Rec) (Rec, error) {
	role, id := S(payload, "to"), S(payload, "id")
	if err := MailAddress(role, id); err != nil {
		return nil, err
	}
	var result Rec
	err := MailLock(func() error {
		if err := checkMailAddress(role, true); err != nil {
			return err
		}
		old, err := ReadMail(role, id)
		if err != nil {
			return err
		}
		if old != nil {
			for _, k := range []string{"id", "to", "from_role", "from_session", "kind", "subject", "head", "body"} {
				if S(old, k) != S(payload, k) {
					return fmt.Errorf("mail %s to %s: id already has a different payload", id, role)
				}
			}
			result = old
			return nil
		}
		result = Rec{}
		for _, k := range []string{"id", "to", "from_role", "from_session", "kind", "subject", "head", "body"} {
			result[k] = S(payload, k)
		}
		result["at"] = Now()
		return WriteJSON(MailPath(role, id), result)
	})
	return result, err
}

// Mail lists records oldest first; an unreadable mailbox is not empty.
func Mail(role string, unacked bool) ([]Rec, error) {
	if err := MailAddress(role, "list"); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(Path("mail", Safe(role)))
	if os.IsNotExist(err) {
		return []Rec{}, nil
	}
	if err != nil {
		return nil, err
	}
	if err := checkMailAddress(role, false); err != nil {
		return nil, err
	}
	out := []Rec{}
	for _, e := range entries {
		if e.Name() == mailAddressFile {
			continue
		}
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		r, err := ReadMail(role, strings.TrimSuffix(e.Name(), ".json"))
		if err != nil {
			return nil, err
		}
		if r != nil && (!unacked || !Has(r, "acked_at")) {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if F(out[i], "at") == F(out[j], "at") {
			return S(out[i], "id") < S(out[j], "id")
		}
		return F(out[i], "at") < F(out[j], "at")
	})
	return out, nil
}

// AckMail marks read, preserving delivery and payload. Caller verifies role ownership.
func AckMail(role, id, sid string) (Rec, error) {
	var r Rec
	err := MailLock(func() error {
		var err error
		r, err = ReadMail(role, id)
		if err != nil {
			return err
		}
		if r == nil {
			return fmt.Errorf("mail %s: no message for %s", id, role)
		}
		if Has(r, "acked_at") {
			return nil
		}
		r["acked_at"], r["acked_by"] = Now(), sid
		return WriteJSON(MailPath(role, id), r)
	})
	return r, err
}

// MailLine remains one line even when a subject contains control characters.
func MailLine(r Rec) string {
	clean := func(s string) string { return strings.Join(strings.Fields(s), " ") }
	return fmt.Sprintf("[fleet] mail %s from %s (%s): %s", clean(S(r, "id")), clean(S(r, "from_role")), clean(S(r, "kind")), clean(S(r, "subject")))
}

// MailLines caps context without acknowledging messages.
func MailLines(role string) []string {
	if role == "" {
		return nil
	}
	if _, err := MailRoleTenant(role); err != nil {
		return []string{"[fleet] mail unavailable; fleet mail"}
	}
	rows, err := Mail(role, true)
	if err != nil {
		return []string{"[fleet] mail unavailable; fleet mail"}
	}
	return MailSummary(rows)
}

// MailSummary is shared by prompt injection and detached delivery.
func MailSummary(rows []Rec) []string {
	var lines []string
	for i, r := range rows {
		if i == 5 {
			break
		}
		lines = append(lines, MailLine(r))
	}
	if len(rows) > 5 {
		lines = append(lines, fmt.Sprintf("[fleet] and %d more; fleet mail", len(rows)-5))
	}
	return lines
}

func sessionMailLines(rec Rec) []string { role, _, _ := MailIdentity(rec); return MailLines(role) }
