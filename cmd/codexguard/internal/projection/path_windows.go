//go:build windows

package projection

import (
	"os"

	"golang.org/x/sys/windows"
)

func pathEntryUnsafe(path string, info os.FileInfo) bool {
	if info.Mode()&os.ModeSymlink != 0 {
		return true
	}
	pathPointer, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return true
	}
	attributes, err := windows.GetFileAttributes(pathPointer)
	if err != nil {
		return true
	}
	return attributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0
}
