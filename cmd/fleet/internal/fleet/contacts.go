package fleet

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// MailIdentity re-resolves the launch directory; a later cd cannot acquire
// another node's address book. Old records use their recorded cwd.
func MailIdentity(rec Rec) (role, tenant, slot string) {
	launch := S(rec, "launch_dir")
	if launch == "" {
		launch = S(rec, "cwd")
	}
	return MapRowsFor(launch)
}

// MailRoleTenant refuses ambiguous addresses: the disk address has no tenant
// component, so one role name must identify exactly one tenant.
func MailRoleTenant(role string) (string, error) {
	if err := MailAddress(role, "address"); err != nil {
		return "", err
	}
	if _, err := os.ReadFile(RolesMap()); err != nil {
		return "", err
	}
	_, rows := MapRows(RolesMap())
	tenant := ""
	for _, r := range rows {
		if Safe(r.Role) == Safe(role) && r.Role != role {
			return "", fmt.Errorf("mail: role %s has a filename collision with %s", role, r.Role)
		}
		if r.Role != role {
			continue
		}
		if tenant != "" && tenant != r.Tenant {
			return "", fmt.Errorf("mail: role %s is ambiguous across tenants", role)
		}
		tenant = r.Tenant
	}
	if tenant == "" {
		return "", fmt.Errorf("mail: role %s is not bound in roles.map", role)
	}
	return tenant, nil
}

// MailContacts reads explicit adjacency: parent, children, siblings. No transitive
// closure or name inference. Seats follow current dispatch, never static contacts.
func MailContacts(rec Rec) ([]string, error) {
	role, tenant, slot := MailIdentity(rec)
	actual, err := MailRoleTenant(role)
	if err != nil {
		return nil, err
	}
	if actual != tenant {
		return nil, fmt.Errorf("mail: tenant changed for %s", role)
	}
	var contacts []string
	if slot != "" {
		contacts, err = seatMailContacts(rec, slot)
	}
	if slot == "" {
		contacts, err = staticMailContacts(role)
	}
	if err != nil {
		return nil, err
	}
	set := map[string]bool{}
	for _, to := range contacts {
		target, err := MailRoleTenant(to)
		if err != nil {
			return nil, err
		}
		if target != tenant {
			return nil, fmt.Errorf("mail: contact %s is outside tenant %s", to, tenant)
		}
		if to != role {
			set[to] = true
		}
	}
	out := make([]string, 0, len(set))
	for to := range set {
		out = append(out, to)
	}
	sort.Strings(out)
	return out, nil
}

func staticMailContacts(role string) ([]string, error) {
	b, err := os.ReadFile(Path("contacts.json"))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var contacts map[string][]string
	if err = json.Unmarshal(b, &contacts); err != nil {
		return nil, fmt.Errorf("mail: contacts.json: %w", err)
	}
	return contacts[role], nil
}

func seatMailContacts(rec Rec, slot string) ([]string, error) {
	// Retained dispatch rows for an old branch in a reused seat are not contacts.
	cwd := S(rec, "launch_dir")
	if cwd == "" {
		cwd = S(rec, "cwd")
	}
	repo, branch := RepoID(cwd), BranchOf(cwd)
	if repo == "" || branch == "" {
		return nil, fmt.Errorf("mail: seat %s has no current branch", slot)
	}
	entries, err := os.ReadDir(Path("dispatch"))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	contact := ""
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		row := ReadJSON(filepath.Join(Path("dispatch"), e.Name()))
		if row == nil {
			return nil, fmt.Errorf("mail: unreadable dispatch %s", e.Name())
		}
		if S(row, "slot") != slot || S(row, "repo") != repo || S(row, "change") != branch {
			continue
		}
		next := S(row, "for")
		if next == "" || (contact != "" && next != contact) {
			return nil, fmt.Errorf("mail: seat %s has ambiguous accountability", slot)
		}
		contact = next
	}
	if contact == "" {
		return nil, nil
	}
	return []string{contact}, nil
}
