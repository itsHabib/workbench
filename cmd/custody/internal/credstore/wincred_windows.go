//go:build windows

package credstore

import (
	"errors"
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

// WinCred is the Windows Credential Manager implementation of Store (spec §4
// D7), reading and writing generic credentials via the advapi32 Cred* APIs. The
// zero value is ready to use — it holds no state. Secrets are namespaced under
// targetPrefix so custody's entries never collide with other applications'.
type WinCred struct{}

// targetPrefix namespaces custody's credential targets within the shared
// Windows Credential Manager, so `wincred:tracker-pat` maps to a target that
// cannot clobber an unrelated app's "tracker-pat" entry.
const targetPrefix = "custody:"

// CRED_TYPE_GENERIC / CRED_PERSIST_LOCAL_MACHINE — the credential class and
// persistence used for every custody secret.
const (
	credTypeGeneric         = 1
	credPersistLocalMachine = 2
)

// credentialW mirrors the Win32 CREDENTIALW struct. Field order and types match
// the C layout so the syscall reads/writes the right offsets.
type credentialW struct {
	Flags              uint32
	Type               uint32
	TargetName         *uint16
	Comment            *uint16
	LastWritten        windows.Filetime
	CredentialBlobSize uint32
	CredentialBlob     *byte
	Persist            uint32
	AttributeCount     uint32
	Attributes         uintptr // always nil; AttributeCount is always zero
	TargetAlias        *uint16
	UserName           *uint16
}

var (
	advapi32        = windows.NewLazySystemDLL("advapi32.dll")
	procCredReadW   = advapi32.NewProc("CredReadW")
	procCredWriteW  = advapi32.NewProc("CredWriteW")
	procCredFree    = advapi32.NewProc("CredFree")
	procCredDeleteW = advapi32.NewProc("CredDeleteW")
)

// Get reads the secret stored under ref. A missing entry returns
// ErrSecretUnavailable; the secret bytes never appear in any error.
func (WinCred) Get(ref string) ([]byte, error) {
	target, err := windows.UTF16PtrFromString(targetPrefix + ref)
	if err != nil {
		return nil, fmt.Errorf("credstore: bad ref %q: %w", ref, err)
	}
	var cred *credentialW
	r1, _, callErr := procCredReadW.Call(
		uintptr(unsafe.Pointer(target)),
		uintptr(credTypeGeneric),
		0,
		uintptr(unsafe.Pointer(&cred)),
	)
	if r1 == 0 {
		callErr = credCallError(callErr)
		if callErr == windows.ERROR_NOT_FOUND {
			return nil, fmt.Errorf("%w: %q", ErrSecretUnavailable, ref)
		}
		return nil, fmt.Errorf("credstore: CredRead %q: %w", ref, callErr)
	}
	defer procCredFree.Call(uintptr(unsafe.Pointer(cred)))
	return copyBlob(cred), nil
}

// copyBlob copies the credential blob into a Go-owned slice so nothing points
// into the OS buffer after CredFree.
func copyBlob(cred *credentialW) []byte {
	if cred.CredentialBlobSize == 0 || cred.CredentialBlob == nil {
		return []byte{}
	}
	src := unsafe.Slice(cred.CredentialBlob, cred.CredentialBlobSize)
	out := make([]byte, len(src))
	copy(out, src)
	return out
}

// Set writes secret under ref, overwriting any existing entry. The secret bytes
// never appear in an error message.
func (WinCred) Set(ref string, secret []byte) error {
	target, err := windows.UTF16PtrFromString(targetPrefix + ref)
	if err != nil {
		return fmt.Errorf("credstore: bad ref %q: %w", ref, err)
	}
	user, err := windows.UTF16PtrFromString("custody")
	if err != nil {
		return fmt.Errorf("credstore: username: %w", err)
	}
	cred := credentialW{
		Type:               credTypeGeneric,
		TargetName:         target,
		Persist:            credPersistLocalMachine,
		CredentialBlobSize: uint32(len(secret)),
		UserName:           user,
	}
	if len(secret) > 0 {
		cred.CredentialBlob = &secret[0]
	}
	r1, _, callErr := procCredWriteW.Call(uintptr(unsafe.Pointer(&cred)), 0)
	if r1 == 0 {
		callErr = credCallError(callErr)
		return fmt.Errorf("credstore: CredWrite %q: %w", ref, callErr)
	}
	return nil
}

// credDelete removes the entry under ref. It exists for test cleanup so the
// integration test does not leave secrets in the operator's credential store;
// it is intentionally not part of the Store interface (spec §4 D7: two methods).
func credDelete(ref string) error {
	target, err := windows.UTF16PtrFromString(targetPrefix + ref)
	if err != nil {
		return err
	}
	r1, _, callErr := procCredDeleteW.Call(uintptr(unsafe.Pointer(target)), uintptr(credTypeGeneric), 0)
	if r1 == 0 {
		return credCallError(callErr)
	}
	return nil
}

func credCallError(callErr error) error {
	if callErr != nil && callErr != windows.ERROR_SUCCESS {
		return callErr
	}
	lastErr := windows.GetLastError()
	if lastErr != nil && lastErr != windows.ERROR_SUCCESS {
		return lastErr
	}
	return errors.New("Windows credential call failed without an error code")
}
