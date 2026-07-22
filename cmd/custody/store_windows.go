//go:build windows

package main

import "github.com/itsHabib/workbench/cmd/custody/internal/credstore"

// newCredStore returns the Windows Credential Manager backend (spec §4 D7). The
// platform seam keeps main.go building on every OS the CI matrix runs, while the
// real secret backend stays Windows-only.
func newCredStore() credstore.Store { return credstore.WinCred{} }
