package fleet

import (
	"fmt"
	"os"
	"sort"
	"strings"
)

// MailIdentity re-resolves the launch directory; a later cd cannot acquire
// another role's identity. Old records use their recorded cwd.
func MailIdentity(rec Rec) (role, tenant, slot string) {
	launch := S(rec, "launch_dir")
	if launch == "" {
		launch = S(rec, "cwd")
	}
	return MapRowsFor(launch)
}

// Mailbox is one typed address in one tenant. A seat is independent of its role.
type Mailbox struct{ Tenant, Kind, Address string }

func rowMailAddress(r MapRow) string {
	if r.Slot != "" {
		return r.Slot
	}
	return r.Role
}

// ResolveMailbox resolves an explicit address inside the caller's tenant. A seat
// kind is never an alias for a seat, even when only one seat currently exists.
func ResolveMailbox(tenant, address string) (Mailbox, error) {
	if err := MailAddress(address, "address"); err != nil {
		return Mailbox{}, err
	}
	if tenant == "" {
		return Mailbox{}, fmt.Errorf("mail: caller tenant is missing")
	}
	if _, err := os.ReadFile(RolesMap()); err != nil {
		return Mailbox{}, err
	}
	_, rows := MapRows(RolesMap())
	kinds, seats := map[string]bool{}, map[string]bool{}
	seatPaths := map[string]bool{}
	for _, r := range rows {
		if r.Tenant != tenant {
			continue
		}
		if r.Role == address && r.Slot != "" {
			seats[r.Slot] = true
		}
		if rowMailAddress(r) != address {
			continue
		}
		kind := "role"
		if r.Slot != "" {
			kind = "seat"
			seatPaths[canonPath(r.Path)] = true
		}
		kinds[kind] = true
	}
	if len(seatPaths) > 1 {
		return Mailbox{}, fmt.Errorf("mail: seat %s is bound to multiple directories; keep one binding", address)
	}
	if len(kinds) > 1 || (len(kinds) > 0 && len(seats) > 0) {
		return Mailbox{}, fmt.Errorf("mail: address %s is ambiguous between a role and seat; give them distinct names", address)
	}
	for kind := range kinds {
		return Mailbox{tenant, kind, address}, nil
	}
	if len(seats) > 0 {
		alternatives := make([]string, 0, len(seats))
		for seat := range seats {
			alternatives = append(alternatives, seat)
		}
		sort.Strings(alternatives)
		return Mailbox{}, fmt.Errorf("mail: %s is a seat kind, not an address; use %s", address, strings.Join(alternatives, ", "))
	}
	return Mailbox{}, fmt.Errorf("mail: address %s is not bound in caller tenant %s", address, tenant)
}

// MailSender returns the launch address and the role used for provenance.
func MailSender(rec Rec) (Mailbox, string, error) {
	role, tenant, seat := MailIdentity(rec)
	if role == "" {
		return Mailbox{}, "", fmt.Errorf("mail: caller has no role in its launch directory; bind it with fleet role")
	}
	address := role
	if seat != "" {
		address = seat
	}
	box, err := ResolveMailbox(tenant, address)
	return box, role, err
}

// MailRoleTenant is the compatibility resolver for callers without a tenant.
// Tenant-aware commands use ResolveMailbox and need no globally unique names.
func MailRoleTenant(address string) (string, error) {
	if err := MailAddress(address, "address"); err != nil {
		return "", err
	}
	if _, err := os.ReadFile(RolesMap()); err != nil {
		return "", err
	}
	_, rows := MapRows(RolesMap())
	tenant := ""
	for _, r := range rows {
		if rowMailAddress(r) != address {
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
	_, err := ResolveMailbox(tenant, address)
	return tenant, err
}

// MailPeer checks identity and tenant only; mail conveys no authority.
func MailPeer(rec Rec, to string) error {
	box, _, err := MailSender(rec)
	if err != nil {
		return err
	}
	_, err = ResolveMailbox(box.Tenant, to)
	return err
}
