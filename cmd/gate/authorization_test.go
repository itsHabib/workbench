package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/itsHabib/workbench/cmd/gate/internal/authorization"
)

func TestWriteAuthorizationIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "authorization.json")
	if err := writeAuthorization(path, []byte("{}\n")); err != nil {
		t.Fatal(err)
	}
	if err := writeAuthorization(path, []byte("{}\n")); err != nil {
		t.Fatal(err)
	}
	if err := writeAuthorization(path, []byte("{\"changed\":true}\n")); err == nil {
		t.Fatal("expected changed-content refusal")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "{}\n" {
		t.Fatalf("output changed: %q", data)
	}
}

func TestStatusPostedByActions(t *testing.T) {
	description := "authorized gau_x run=run_x"
	status := authorization.Status{
		Context: "gate", State: "success", Body: description,
		Creator: authorization.Actor{Login: "github-actions[bot]", Type: "Bot"},
	}
	if !statusPostedByActions([]authorization.Status{status}, description) {
		t.Fatal("expected GitHub Actions status")
	}
	status.Creator = authorization.Actor{Login: "itsHabib", Type: "User"}
	if statusPostedByActions([]authorization.Status{status}, description) {
		t.Fatal("ambient user status must not verify")
	}
}
