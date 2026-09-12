package fleet

import (
	"crypto/sha256"
	"fmt"
)

// MailStopKey scopes a launch stop to the configured mailbox's tenant.
func MailStopKey(address string) (string, error) {
	tenant, err := MailAddressTenant(address)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(DumpJSON([]string{tenant, address}))
	return fmt.Sprintf("address:%x", sum), nil
}
