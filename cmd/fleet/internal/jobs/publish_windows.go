//go:build windows

package jobs

import "golang.org/x/sys/windows"

func publishSnapshot(staged, target, _ string) error {
	from, err := windows.UTF16PtrFromString(staged)
	if err != nil {
		return err
	}
	to, err := windows.UTF16PtrFromString(target)
	if err != nil {
		return err
	}
	// The staged file is already flushed. Windows cannot fsync a directory;
	// MoveFileEx supplies replacement and write-through publication semantics.
	return windows.MoveFileEx(from, to, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH)
}
