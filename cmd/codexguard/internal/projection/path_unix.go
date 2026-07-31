//go:build !windows

package projection

import "os"

func pathEntryUnsafe(_ string, info os.FileInfo) bool {
	return info.Mode()&os.ModeSymlink != 0
}
