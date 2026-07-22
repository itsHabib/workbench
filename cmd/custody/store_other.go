//go:build !windows

package main

import (
	"fmt"

	"github.com/itsHabib/workbench/cmd/custody/internal/credstore"
)

// unavailableStore is the non-Windows credential backend: there is none in v0
// (spec §4 D7 — Windows Credential Manager only). It fails closed and loud so a
// serve/keys invocation on an unsupported OS never silently forwards without a
// credential; secret bytes never appear because there are none to leak.
type unavailableStore struct{}

func (unavailableStore) Get(_ string) ([]byte, error) {
	return nil, fmt.Errorf("%w: credential store unsupported on this platform", credstore.ErrSecretUnavailable)
}

func (unavailableStore) Set(ref string, _ []byte) error {
	return fmt.Errorf("credstore: %q: credential store unsupported on this platform", ref)
}

// newCredStore returns the fail-closed stub so main.go builds on non-Windows CI.
func newCredStore() credstore.Store { return unavailableStore{} }
