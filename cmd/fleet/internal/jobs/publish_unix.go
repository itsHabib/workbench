//go:build !windows

package jobs

import "os"

func publishSnapshot(staged, target, directory string) error {
	if err := os.Rename(staged, target); err != nil {
		return err
	}
	dir, err := os.Open(directory)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
