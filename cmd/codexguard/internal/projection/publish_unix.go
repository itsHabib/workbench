//go:build !windows

package projection

import (
	"errors"

	"golang.org/x/sys/unix"
)

func publishExclusive(file stagedFile) error {
	return unix.Linkat(
		int(file.parent.file.Fd()),
		file.stageBase,
		int(file.parent.file.Fd()),
		file.targetBase,
		0,
	)
}

func removePublished(file stagedFile) error {
	var staged unix.Stat_t
	if err := unix.Fstatat(int(file.parent.file.Fd()), file.stageBase, &staged, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return err
	}
	var target unix.Stat_t
	if err := unix.Fstatat(int(file.parent.file.Fd()), file.targetBase, &target, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		if errors.Is(err, unix.ENOENT) {
			return nil
		}
		return err
	}
	if staged.Dev != target.Dev || staged.Ino != target.Ino {
		return nil
	}
	return unix.Unlinkat(int(file.parent.file.Fd()), file.targetBase, 0)
}

func removeStaged(file stagedFile) error {
	return unix.Unlinkat(int(file.parent.file.Fd()), file.stageBase, 0)
}
