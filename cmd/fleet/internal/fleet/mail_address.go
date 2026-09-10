package fleet

import (
	"fmt"
	"os"
	"strings"
)

const mailAddressFile = ".address.json"

// checkMailAddress pins the original tenant without changing message payloads.
// Only send may initialize an empty mailbox, while holding MailLock. Retained
// messages without a readable pin are unknown, never assigned to today's tenant.
func checkMailAddress(role string, create bool) error {
	tenant, err := MailRoleTenant(role)
	if err != nil {
		return err
	}
	p := Path("mail", Safe(role), mailAddressFile)
	pin := ReadJSON(p)
	if pin != nil {
		if S(pin, "role") != role || S(pin, "tenant") != tenant {
			return fmt.Errorf("mail: mailbox %s belongs to a different role or tenant; ask the operator to reconcile it", role)
		}
		return nil
	}
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		return fmt.Errorf("mail: mailbox %s has unreadable address metadata", role)
	}
	if !create {
		return fmt.Errorf("mail: mailbox %s has no address metadata; tenant unknown", role)
	}
	entries, err := os.ReadDir(Path("mail", Safe(role)))
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".json") {
			return fmt.Errorf("mail: retained mailbox %s has no address metadata; ask the operator to reconcile it", role)
		}
	}
	return WriteJSON(p, Rec{"role": role, "tenant": tenant})
}
