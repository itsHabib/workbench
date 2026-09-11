package fleet

// MailStopKey scopes a launch stop to the configured mailbox's tenant.
func MailStopKey(address string) (string, error) {
	tenant, err := MailAddressTenant(address)
	if err != nil {
		return "", err
	}
	return "address:" + tenant + ":" + address, nil
}
