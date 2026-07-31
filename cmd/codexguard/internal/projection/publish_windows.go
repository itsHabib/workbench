//go:build windows

package projection

import (
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Windows has no Go-level linkat equivalent. Projection therefore holds and
// revalidates the directory handle immediately before and after the exclusive
// hard-link publish. Any identity change refuses the apply.
func publishExclusive(file stagedFile) error {
	return os.Link(file.stage, file.target)
}

func removePublished(file stagedFile) error {
	if err := file.parent.validate(); err != nil {
		return err
	}
	staged, err := os.Lstat(file.stage)
	if err != nil {
		return err
	}
	targetPath, err := windows.UTF16PtrFromString(file.target)
	if err != nil {
		return err
	}
	handle, err := windows.CreateFile(
		targetPath,
		windows.DELETE|windows.FILE_READ_ATTRIBUTES,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_FLAG_OPEN_REPARSE_POINT,
		0,
	)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	target := os.NewFile(uintptr(handle), file.target)
	if target == nil {
		_ = windows.CloseHandle(handle)
		return nil
	}
	targetInfo, err := target.Stat()
	if err != nil {
		_ = target.Close()
		return err
	}
	if !os.SameFile(staged, targetInfo) {
		return target.Close()
	}
	deleteFile := byte(1)
	err = windows.SetFileInformationByHandle(
		windows.Handle(target.Fd()),
		windows.FileDispositionInfo,
		&deleteFile,
		uint32(unsafe.Sizeof(deleteFile)),
	)
	closeErr := target.Close()
	if err != nil {
		return err
	}
	return closeErr
}

func removeStaged(file stagedFile) error {
	return os.Remove(file.stage)
}
