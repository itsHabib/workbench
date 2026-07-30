package reviewfindings

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

type addressManifest struct {
	Version      string `json:"version"`
	DigestRule   string `json:"digest_rule"`
	CorpusSHA256 string `json:"corpus_sha256"`
	Cases        []struct {
		File   string `json:"file"`
		SHA256 string `json:"sha256"`
	} `json:"cases"`
}

func TestAddressConformanceManifestPinsCorpus(t *testing.T) {
	root := filepath.Join("testdata", "address-v1")
	data, err := os.ReadFile(filepath.Join(root, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest addressManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Version != "reviewfindings-address-conformance-v1" {
		t.Fatalf("version = %q", manifest.Version)
	}
	sort.Slice(manifest.Cases, func(i, j int) bool { return manifest.Cases[i].File < manifest.Cases[j].File })
	var corpus []byte
	for _, entry := range manifest.Cases {
		caseData, err := os.ReadFile(filepath.Join(root, entry.File))
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(caseData)
		got := hex.EncodeToString(sum[:])
		if got != entry.SHA256 {
			t.Fatalf("%s digest = %s, want %s", entry.File, got, entry.SHA256)
		}
		corpus = append(corpus, []byte(entry.File+":"+got+"\n")...)
	}
	sum := sha256.Sum256(corpus)
	if got := hex.EncodeToString(sum[:]); got != manifest.CorpusSHA256 {
		t.Fatalf("corpus digest = %s, want %s", got, manifest.CorpusSHA256)
	}
}
