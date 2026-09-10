package fleet

// The substrate's own reader of the mail store.
//
// The mail verb resolves a mailbox through the caller's identity: a live session in a
// roled directory. That rule is right for an agent — it is what keeps one role from
// reading another's inbox — and wrong for the substrate itself. The watcher has no
// session and no directory; it is the thing that starts sessions. So it reads the
// store: the typed mailbox for a tenant and address, plus the retained flat mailbox
// that predates it, with every record re-validating the address it claims.
//
// One function, used by every duty that needs a mailbox without an identity. The
// verb's rule is unchanged; nothing here grants a caller anything.

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// mailDigest is the fixed-width, lowercase path component of a name: distinct names
// stay distinct on a case-insensitive filesystem and no component exceeds the limit.
func mailDigest(s string) string { return fmt.Sprintf("%x", sha256.Sum256([]byte(s))) }

// MailStoreDirs is every directory that may hold records for one address in one
// tenant: the typed mailbox under each address kind, then the retained flat one —
// and the flat one only when its own pin still names this tenant and this address.
//
// The pin is the only tenant evidence a flat mailbox has. Its records predate the
// tenant field, so the directory is their whole claim; when roles.map later rebinds
// the name to another tenant, an unpinned directory would hand that tenant the
// previous one's mail. An unreadable pin is an error, not an empty result.
func MailStoreDirs(tenant, address string) ([]string, error) {
	var dirs []string
	for _, kind := range []string{"role", "seat"} {
		dirs = append(dirs, Path("mail", ".v2", mailDigest(tenant), kind, mailDigest(address)))
	}
	flat, err := Mailbox{Tenant: tenant, Kind: "role", Address: address}.legacyMailDir()
	if err != nil {
		return nil, err
	}
	if flat == "" {
		return dirs, nil
	}
	return append(dirs, flat), nil
}

// MailAddressTenant is the tenant owning an address named as a role or as a seat.
// An address that names neither, or names both in different tenants, has no tenant.
func MailAddressTenant(address string) (string, error) {
	if err := MailAddress(address, "address"); err != nil {
		return "", err
	}
	_, rows := MapRows(RolesMap())
	tenant := ""
	for _, r := range rows {
		if r.Role != address && r.Slot != address {
			continue
		}
		if tenant != "" && tenant != r.Tenant {
			return "", fmt.Errorf("mail: address %s is ambiguous across tenants", address)
		}
		tenant = r.Tenant
	}
	if tenant == "" {
		return "", fmt.Errorf("mail: address %s is not bound in roles.map", address)
	}
	return tenant, nil
}

// MailboxRecords is every record addressed to one address in one tenant, oldest
// first. A record whose own fields disagree with the mailbox it sits in is not that
// address's mail and is skipped rather than reported: the reader observes, and a
// damaged record is the verb's problem to refuse.
func MailboxRecords(tenant, address string) ([]Rec, error) {
	if err := MailAddress(address, "address"); err != nil {
		return nil, err
	}
	if tenant == "" {
		return nil, fmt.Errorf("mail: tenant is required")
	}
	dirs, err := MailStoreDirs(tenant, address)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var out []Rec
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		for _, e := range entries {
			id := strings.TrimSuffix(e.Name(), ".json")
			if e.IsDir() || e.Name() == mailAddressFile || !strings.HasSuffix(e.Name(), ".json") || seen[id] {
				continue
			}
			r := mailRecordAt(filepath.Join(dir, e.Name()), tenant, address, id)
			if r == nil {
				continue
			}
			seen[id] = true
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

// mailRecordAt is the record at a path when it claims exactly this address, id and
// tenant, else nil. A record written before tenants were stored carries none, and
// its directory is the only claim it has — which is why MailStoreDirs admits a flat
// directory only when its pin still names this tenant and address.
func mailRecordAt(path, tenant, address, id string) Rec {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var r Rec
	if err := json.Unmarshal(b, &r); err != nil {
		return nil
	}
	if S(r, "to") != address || S(r, "id") != id || F(r, "at") <= 0 {
		return nil
	}
	if t := S(r, "tenant"); t != "" && t != tenant {
		return nil
	}
	return r
}

// StampMail records a substrate-side fact on one record — that it was handed to a
// process, and by whom — without acknowledging it. Acknowledgement stays the
// recipient's act; a stamp only stops the substrate doing the same thing twice.
func StampMail(tenant, address, id string, fields Rec) (Rec, error) {
	if err := MailAddress(address, id); err != nil {
		return nil, err
	}
	var out Rec
	err := mailLock(func() error {
		dirs, err := MailStoreDirs(tenant, address)
		if err != nil {
			return err
		}
		for _, dir := range dirs {
			path := filepath.Join(dir, id+".json")
			r := mailRecordAt(path, tenant, address, id)
			if r == nil {
				continue
			}
			for k, v := range fields {
				r[k] = v
			}
			out = r
			return WriteJSON(path, r)
		}
		return fmt.Errorf("mail %s: no record for %s", id, address)
	})
	return out, err
}

// UnstampMail removes substrate-side fields a stamp wrote. It is the other half of
// StampMail: a reservation taken before an act that did not happen has to be given
// back, or the message is stamped with nothing running and no fold ever carries it
// again. It never touches the recipient's own fields.
func UnstampMail(tenant, address, id string, keys ...string) error {
	if err := MailAddress(address, id); err != nil {
		return err
	}
	return mailLock(func() error {
		dirs, err := MailStoreDirs(tenant, address)
		if err != nil {
			return err
		}
		for _, dir := range dirs {
			path := filepath.Join(dir, id+".json")
			r := mailRecordAt(path, tenant, address, id)
			if r == nil {
				continue
			}
			for _, k := range keys {
				delete(r, k)
			}
			return WriteJSON(path, r)
		}
		return fmt.Errorf("mail %s: no record for %s", id, address)
	})
}
