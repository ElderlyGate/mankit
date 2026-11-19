//go:build unix

package disk

import (
	"io/fs"
	"os"
	"syscall"
)

// nonBlocking opens a file so that a pipe or a device answers at once instead of waiting for a writer; noFollow refuses a link as the last step.
const (
	nonBlocking = syscall.O_NONBLOCK
	noFollow    = syscall.O_NOFOLLOW
)

// Owned says whether this user owns the file.
func Owned(info fs.FileInfo) bool {
	st, ok := info.Sys().(*syscall.Stat_t)
	return ok && int(st.Uid) == os.Getuid()
}
