//go:build !unix

package disk

import "io/fs"

// Where the system has no such flags, the look before opening still refuses what is not a file, and NoLinks every link.
const (
	nonBlocking = 0
	noFollow    = 0
)

// Owned is true where the system keeps no owner the core can ask.
func Owned(fs.FileInfo) bool { return true }
