package manifest

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// FuzzManifestLoad drives the manifest loader with arbitrary bytes. Two generic
// properties hold regardless of any in-flight hardening: Load never panics, and
// it fails CLOSED — every input yields EITHER a fully-valid *Manifest (nil
// error) OR a typed error (nil manifest), never a half-built manifest paired
// with a nil error. A returned manifest re-satisfies every §5 invariant.
func FuzzManifestLoad(f *testing.F) {
	seedInline := [][]byte{
		[]byte(""), []byte("{}"), []byte("[]"), []byte("null"),
		[]byte(`{"version":1}`),
		[]byte(`{"version":1,"keys":{}}`),
		[]byte(`{"version":2,"keys":{"k":{}}}`),
		[]byte(`{"version":1,"keys":{"k":{"secret":"wincred:x","upstream":"https://h","inject":[{"kind":"header","name":"Authorization","template":"Bearer {secret}"}],"actions":{"a":{"rules":[{"methods":["GET"],"path":"/x"}]}}}}}`),
	}
	for _, s := range seedInline {
		f.Add(s)
	}
	for _, name := range manifestTestdataFiles() {
		data, err := os.ReadFile(filepath.Join("testdata", name))
		if err != nil {
			f.Fatalf("read seed %s: %v", name, err)
		}
		f.Add(data)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		m, err := Load(bytes.NewReader(data))
		if (m == nil) == (err == nil) {
			t.Fatalf("Load must return exactly one of manifest/error: m=%v err=%v", m, err)
		}
		if err != nil {
			return
		}
		assertManifestInvariants(t, m)
	})
}

// assertManifestInvariants re-checks, from the outside, that a manifest Load
// vouched for really is valid: version pinned, at least one key, and a clean
// re-run of the internal validator.
func assertManifestInvariants(t *testing.T, m *Manifest) {
	t.Helper()
	if m.Version != 1 {
		t.Fatalf("loaded manifest has version %d, want 1", m.Version)
	}
	if len(m.Keys) == 0 {
		t.Fatalf("loaded manifest has no keys")
	}
	if err := m.validate(); err != nil {
		t.Fatalf("Load returned a manifest that fails re-validation: %v", err)
	}
}

func manifestTestdataFiles() []string {
	return []string{
		"valid.json", "bad-glob.json", "missing-required.json",
		"mustmatch.json", "unknown-field.json",
	}
}
