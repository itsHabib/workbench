package fleet

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// MaxMailSubjectBytes bounds subject text carried into hook context.
const MaxMailSubjectBytes = 1024

var mailID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
var mailRole = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)

// MailAddress validates path components before they enter the store.
func MailAddress(role, id string) error {
	if !mailRole.MatchString(role) || !mailID.MatchString(id) {
		return fmt.Errorf("mail: invalid role or id (use letters, digits, . _ -; role also permits :)")
	}
	return nil
}

// MailPath is a compatibility helper for globally unambiguous addresses.
// Tenant-aware code uses MailPathFor; invalid addresses have no path.
func MailPath(address, id string) string {
	tenant, err := MailRoleTenant(address)
	if err != nil {
		return ""
	}
	path, _ := MailPathFor(tenant, address, id)
	return path
}

func mailLock(fn func() error) error {
	if ReadOnly {
		return fmt.Errorf("mail: state is read-only")
	}
	return KeyLock("mail", fn)
}

// ReadMail reads the address in the tenant captured during authorization.
func ReadMail(address, id, tenant string) (Rec, error) {
	return ReadMailFor(tenant, address, id)
}

// ReadMailFor reads an exact tenant/address, including its pinned legacy record.
func ReadMailFor(tenant, address, id string) (Rec, error) {
	if err := MailAddress(address, id); err != nil {
		return nil, err
	}
	box, err := ResolveMailbox(tenant, address)
	if err != nil {
		return nil, err
	}
	r, _, err := box.read(id)
	return r, err
}

func (b Mailbox) readRecord(path, id string, legacy bool) (Rec, error) {
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var r Rec
	if err = json.Unmarshal(raw, &r); err != nil {
		return nil, fmt.Errorf("mail %s: unreadable: %w", id, err)
	}
	if S(r, "to") != b.Address || S(r, "id") != id || F(r, "at") <= 0 {
		return nil, fmt.Errorf("mail %s: damaged record or address collision", id)
	}
	if !legacy && (S(r, "tenant") != b.Tenant || S(r, "to_kind") != b.Kind) {
		return nil, fmt.Errorf("mail %s: damaged tenant or address kind", id)
	}
	return r, nil
}

func (b Mailbox) read(id string) (Rec, string, error) {
	r, err := b.readRecord(b.path(id), id, false)
	if err != nil {
		return nil, "", err
	}
	dir, err := b.legacyMailDir()
	if err != nil {
		return nil, "", err
	}
	if dir == "" {
		return r, b.path(id), nil
	}
	path := filepath.Join(dir, id+".json")
	old, err := b.readRecord(path, id, true)
	if err != nil {
		return nil, "", err
	}
	if old == nil {
		return r, b.path(id), nil
	}
	if r != nil {
		return nil, "", fmt.Errorf("mail %s: exists in both legacy and typed mailbox; reconcile before changing it", id)
	}
	return old, path, nil
}

func normalizeMail(payload Rec, tenant string) (Rec, Mailbox, error) {
	if tenant == "" {
		return nil, Mailbox{}, fmt.Errorf("mail: validated tenant is required")
	}
	if supplied := S(payload, "tenant"); supplied != "" && supplied != tenant {
		return nil, Mailbox{}, fmt.Errorf("mail: payload tenant differs from authorized tenant")
	}
	box, err := ResolveMailbox(tenant, S(payload, "to"))
	if err != nil {
		return nil, Mailbox{}, err
	}
	p := Rec{}
	for k, v := range payload {
		p[k] = v
	}
	p["tenant"], p["to_kind"] = tenant, box.Kind
	if S(p, "from_address") == "" {
		p["from_address"], p["from_kind"] = S(p, "from_role"), "role"
	}
	return p, box, nil
}

func sameMail(old, p Rec) bool {
	for _, k := range []string{"id", "to", "from_role", "kind", "subject", "head", "body"} {
		if S(old, k) != S(p, k) {
			return false
		}
	}
	address, kind := S(old, "from_address"), S(old, "from_kind")
	if address == "" {
		address, kind = S(old, "from_role"), "role"
	}
	return address == S(p, "from_address") && kind == S(p, "from_kind")
}

// PutMail publishes once per tenant/address/id. Retry identity includes the
// sender's address, so another seat of the same role cannot claim its send.
func PutMail(payload Rec, tenant string) (Rec, error) {
	if len(S(payload, "subject")) > MaxMailSubjectBytes {
		return nil, fmt.Errorf("mail: subject exceeds %d bytes", MaxMailSubjectBytes)
	}
	if err := MailAddress(S(payload, "to"), S(payload, "id")); err != nil {
		return nil, err
	}
	p, box, err := normalizeMail(payload, tenant)
	if err != nil {
		return nil, err
	}
	id := S(p, "id")
	var result Rec
	err = mailLock(func() error {
		old, _, err := box.read(id)
		if err != nil {
			return err
		}
		if old != nil {
			if !sameMail(old, p) {
				return fmt.Errorf("mail %s to %s: id already has a different payload or sender address", id, box.Address)
			}
			result = old
			return nil
		}
		result = Rec{}
		for _, k := range []string{"id", "to", "tenant", "to_kind", "from_role", "from_address", "from_kind", "from_session", "kind", "subject", "head", "body"} {
			result[k] = S(p, k)
		}
		result["at"] = Now()
		return WriteJSON(box.path(id), result)
	})
	return result, err
}

// Mail lists the address in the tenant captured during authorization.
func Mail(address string, unacked bool, tenant string) ([]Rec, error) {
	return MailFor(tenant, address, unacked)
}

// MailFor lists the exact mailbox, or explicitly requested historical role mail.
// A shared seat-kind mailbox is read-only history, never a seat's own inbox.
func MailFor(tenant, address string, unacked bool) ([]Rec, error) {
	box, legacyOnly, err := readMailbox(tenant, address)
	if err != nil {
		return nil, err
	}
	ids := map[string]bool{}
	if !legacyOnly {
		if err := mailIDs(box.dir(), ids); err != nil {
			return nil, err
		}
	}
	dir, err := box.legacyMailDir()
	if err != nil {
		return nil, err
	}
	if dir != "" {
		if err := mailIDs(dir, ids); err != nil {
			return nil, err
		}
	}
	out := []Rec{}
	for id := range ids {
		r, _, err := box.read(id)
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

func mailIDs(dir string, ids map[string]bool) error {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.Name() != mailAddressFile && strings.HasSuffix(e.Name(), ".json") {
			ids[strings.TrimSuffix(e.Name(), ".json")] = true
		}
	}
	return nil
}

// AckMail acknowledges the address in the tenant captured during authorization.
func AckMail(address, id, sid, tenant string) (Rec, error) {
	return AckMailFor(tenant, address, id, sid)
}

// AckMailFor updates the exact mailbox. The command verifies caller ownership.
// Pinned legacy role mail is acknowledged in place, preserving its provenance.
func AckMailFor(tenant, address, id, sid string) (Rec, error) {
	if err := MailAddress(address, id); err != nil {
		return nil, err
	}
	box, err := ResolveMailbox(tenant, address)
	if err != nil {
		return nil, err
	}
	var r Rec
	err = mailLock(func() error {
		var path string
		var err error
		r, path, err = box.read(id)
		if err != nil {
			return err
		}
		if r == nil {
			return fmt.Errorf("mail %s: no message for %s", id, address)
		}
		if Has(r, "acked_at") {
			return nil
		}
		r["acked_at"], r["acked_by"] = Now(), sid
		return WriteJSON(path, r)
	})
	return r, err
}

// MailLine remains one line even when a subject contains control characters.
func MailLine(r Rec) string {
	clean := func(s string) string {
		if len(s) > MaxMailSubjectBytes {
			s = strings.ToValidUTF8(s[:MaxMailSubjectBytes], "") + "…"
		}
		return strings.Join(strings.Fields(s), " ")
	}
	return fmt.Sprintf("[fleet] mail %s from %s (%s): %s", clean(S(r, "id")), clean(mailFrom(r)), clean(S(r, "kind")), clean(S(r, "subject")))
}

// MailLines caps context without acknowledging messages.
func MailLines(role string) []string {
	if role == "" {
		return nil
	}
	tenant, err := MailRoleTenant(role)
	if err != nil {
		return []string{"[fleet] mail unavailable; fleet mail"}
	}
	rows, err := Mail(role, true, tenant)
	if err != nil {
		return []string{"[fleet] mail unavailable; fleet mail"}
	}
	return MailSummary(rows)
}

// MailSummary bounds prompt injection.
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

func mailFrom(r Rec) string {
	if address := S(r, "from_address"); address != "" {
		return address
	}
	return S(r, "from_role")
}

func sessionMailLines(rec Rec) []string {
	box, _, err := MailSender(rec)
	if err != nil {
		role, _, _ := MailIdentity(rec)
		if role == "" {
			return nil
		}
		return []string{"[fleet] mail unavailable; fleet mail"}
	}
	rows, err := MailFor(box.Tenant, box.Address, true)
	if err != nil {
		return []string{"[fleet] mail unavailable; fleet mail"}
	}
	return MailSummary(rows)
}
