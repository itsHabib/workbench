package main

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// TestLookupProtectionUnprotectedIsAnAnswer pins the one gh failure that is a
// FACT, not an error: an unprotected default branch answers the protection
// endpoint with 404, and "no protection — gate and the guard are the only
// boundary" is exactly what the sweep needs to know.
func TestLookupProtectionUnprotectedIsAnAnswer(t *testing.T) {
	run := func(context.Context, string) ([]byte, []byte, error) {
		return nil, []byte("gh: Not Found (HTTP 404)"), errors.New("exit status 1")
	}
	p, err := lookupProtectionContext(context.Background(), "o/r", run)
	if err != nil {
		t.Fatalf("404 must read as unprotected, not an error: %v", err)
	}
	if p.Protected {
		t.Errorf("404 must report Protected=false, got %+v", p)
	}
}

// TestLookupProtectionErrorsAreNotUnprotected is the safety half: a repo gate
// could not READ must never be reported as unprotected, or a sweep would plan a
// refresh-free merge against a strict repo on the strength of an auth failure.
func TestLookupProtectionErrorsAreNotUnprotected(t *testing.T) {
	run := func(context.Context, string) ([]byte, []byte, error) {
		return nil, []byte("gh: Bad credentials (HTTP 401)"), errors.New("exit status 1")
	}
	if _, err := lookupProtectionContext(context.Background(), "o/r", run); err == nil {
		t.Fatal("an auth failure must stay an error")
	}
}

// TestLookupProtectionCancelledReportsContext pins that a timeout surfaces as
// the context error rather than gh's exit status.
func TestLookupProtectionCancelledReportsContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	run := func(context.Context, string) ([]byte, []byte, error) {
		return nil, nil, errors.New("signal: killed")
	}
	_, err := lookupProtectionContext(ctx, "o/r", run)
	if err == nil || !strings.Contains(err.Error(), "context canceled") {
		t.Fatalf("want the context error, got %v", err)
	}
}

// TestLookupProtectionDecodesStrictShape pins the read a sweep plans around:
// strict, the required contexts, and conversation resolution.
func TestLookupProtectionDecodesStrictShape(t *testing.T) {
	body := `{"required_status_checks":{"strict":true,"contexts":["ubuntu-latest","windows-latest"]},
	          "required_conversation_resolution":{"enabled":true}}`
	run := func(context.Context, string) ([]byte, []byte, error) {
		return []byte(body), nil, nil
	}
	p, err := lookupProtectionContext(context.Background(), "o/r", run)
	if err != nil {
		t.Fatal(err)
	}
	if !p.Protected || !p.Strict || !p.ConversationResolution {
		t.Fatalf("protection shape wrong: %+v", p)
	}
	if strings.Join(p.Contexts, ",") != "ubuntu-latest,windows-latest" {
		t.Errorf("required contexts wrong: %+v", p.Contexts)
	}
}

// TestRepeatedFlagSplits pins that a sweep scope reads the same typed flag by
// flag or pasted as one comma-separated string.
func TestRepeatedFlagSplits(t *testing.T) {
	var r repeatedFlag
	if err := r.Set("a/b, c/d"); err != nil {
		t.Fatal(err)
	}
	if err := r.Set("e/f"); err != nil {
		t.Fatal(err)
	}
	if strings.Join(r, "|") != "a/b|c/d|e/f" {
		t.Fatalf("repeated flag = %v", r)
	}
}
