package fleet

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
)

const mailAddressFile = ".address.json"

// Lowercase, fixed-width digests preserve distinct names on case-insensitive
// filesystems without exceeding the per-component filename limit. The record
// still validates the original tenant, address kind, and address on every read.
func mailComponent(s string) string { return fmt.Sprintf("%x", sha256.Sum256([]byte(s))) }
func (b Mailbox) dir() string {
	return Path("mail", ".v2", mailComponent(b.Tenant), b.Kind, mailComponent(b.Address))
}
func (b Mailbox) path(id string) string { return filepath.Join(b.dir(), id+".json") }
func (b Mailbox) legacyDir() string     { return Path("mail", Safe(b.Address)) }

// MailPathFor returns the typed, case-safe filename for a resolved address.
func MailPathFor(tenant, address, id string) (string, error) {
	if err := MailAddress(address, id); err != nil {
		return "", err
	}
	box, err := ResolveMailbox(tenant, address)
	if err != nil {
		return "", err
	}
	return box.path(id), nil
}

// legacyMailDir admits only the pinned historical role identity. Legacy records
// are never reassigned to a seat or to a different tenant, and stay at their original paths.
func (b Mailbox) legacyMailDir() (string, error) {
	if b.Kind != "role" {
		return "", nil
	}
	dir := b.legacyDir()
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return "", nil
	}
	pin := ReadJSON(filepath.Join(dir, mailAddressFile))
	if pin == nil {
		return "", fmt.Errorf("mail: retained mailbox %s has unreadable address metadata; tenant unknown", b.Address)
	}
	// A different tenant or a former Safe alias is a different mailbox, not ours.
	if S(pin, "role") != b.Address || S(pin, "tenant") != b.Tenant {
		return "", nil
	}
	return dir, nil
}

// readMailbox permits explicit inspection of pinned shared-role history. It does
// not create a routing alias and is never used by send, ack, or hook identity.
func readMailbox(tenant, address string) (Mailbox, bool, error) {
	if err := MailAddress(address, "address"); err != nil {
		return Mailbox{}, false, err
	}
	box, err := ResolveMailbox(tenant, address)
	if err == nil {
		return box, false, nil
	}
	_, rows := MapRows(RolesMap())
	for _, r := range rows {
		if r.Tenant != tenant || r.Role != address || r.Slot == "" {
			continue
		}
		legacy := Mailbox{tenant, "role", address}
		dir, readErr := legacy.legacyMailDir()
		if readErr != nil {
			return Mailbox{}, false, readErr
		}
		if dir != "" {
			return legacy, true, nil
		}
	}
	return Mailbox{}, false, err
}
