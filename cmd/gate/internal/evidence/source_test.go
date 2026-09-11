package evidence

import (
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"testing"
)

func TestExactSourceValidatesBlob(t *testing.T) {
	content := "source\n"
	sum := sha1.Sum(append([]byte(fmt.Sprintf("blob %d%c", len(content), 0)), []byte(content)...))
	blob := map[string]any{"type": "file", "path": "docs/x.md", "sha": hex.EncodeToString(sum[:]), "encoding": "base64", "content": base64.StdEncoding.EncodeToString([]byte(content)), "size": len(content)}
	raw, _ := json.Marshal(blob)
	got, _, err := decodeExactSource(raw, "docs/x.md")
	if err != nil || got != content {
		t.Fatalf("%q %v", got, err)
	}
	for _, key := range []string{"sha", "path", "type", "encoding", "content"} {
		t.Run(key, func(t *testing.T) {
			altered := map[string]any{}
			for k, v := range blob {
				altered[k] = v
			}
			altered[key] = "bad"
			raw, _ := json.Marshal(altered)
			if _, _, err := decodeExactSource(raw, "docs/x.md"); err == nil {
				t.Fatal("invalid source accepted")
			}
		})
	}
}
