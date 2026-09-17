package poc

import (
	"os"
	"strings"
	"testing"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func replaceVersion(src string) string { return strings.Replace(src, "0.1.0", "0.2.0", 1) }
