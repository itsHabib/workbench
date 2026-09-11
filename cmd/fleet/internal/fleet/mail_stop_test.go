package fleet

import (
	"fmt"
	"path/filepath"
	"testing"
)

func TestAddressStopKeysCannotAliasTenantsOrSafeNames(t *testing.T) {
	root := mailFixture(t)
	addMailBindings(t, fmt.Sprintf("%s a:b hub:c\n%s a b:hub:c\n%s one hub:a\n%s one hub__a\n", filepath.Join(root, "one"), filepath.Join(root, "two"), filepath.Join(root, "three"), filepath.Join(root, "four")))
	seen := map[string]bool{}
	for _, address := range []string{"hub:c", "b:hub:c", "hub:a", "hub__a"} {
		key, err := MailStopKey(address)
		if err != nil {
			t.Fatal(err)
		}
		file := KeyFile("stop", key)
		if seen[file] {
			t.Fatal("address stop alias", address, file)
		}
		seen[file] = true
	}
}
