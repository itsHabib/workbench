package fleet

import (
	"fmt"
	"os"
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

// MailPeer requires identified roles in the same tenant; mail conveys no authority.
func MailPeer(rec Rec, to string) error {
	role, tenant, _ := MailIdentity(rec)
	if role == "" {
		return fmt.Errorf("mail: caller has no role in its launch directory; ask the operator to bind it with fleet role")
	}
	actual, err := MailRoleTenant(role)
	if err != nil {
		return err
	}
	target, err := MailRoleTenant(to)
	if err != nil {
		return err
	}
	if actual != tenant || target != tenant {
		return fmt.Errorf("mail: %s is outside caller tenant %s", to, tenant)
	}
	return nil
}
