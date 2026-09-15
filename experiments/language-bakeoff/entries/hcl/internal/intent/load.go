package intent

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// Pattern selects source files in a workspace directory.
const Pattern = "*.wb.hcl"

// LoadDir reads the workspace's source files in name order.
func LoadDir(dir string) ([]Source, error) {
	names, err := filepath.Glob(filepath.Join(dir, Pattern))
	if err != nil {
		return nil, err
	}
	if len(names) == 0 {
		return nil, fmt.Errorf("no %s files in the workspace", Pattern)
	}
	sort.Strings(names)
	srcs := make([]Source, 0, len(names))
	for _, n := range names {
		b, err := os.ReadFile(n)
		if err != nil {
			return nil, err
		}
		srcs = append(srcs, Source{Name: filepath.Base(n), Bytes: b})
	}
	return srcs, nil
}

// DigestSources fingerprints the exact source a plan was made from.
func DigestSources(srcs []Source) string {
	h := sha256.New()
	for _, s := range srcs {
		fmt.Fprintf(h, "%s\x00%d\x00", s.Name, len(s.Bytes))
		h.Write(s.Bytes)
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil))
}
