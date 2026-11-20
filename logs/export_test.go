package logs

import (
	"errors"
	"io"
	"os"
)

// Rotation sets the size a log file rotates at and how many are kept, for one test, and gives back the func that restores them.
func Rotation(at int64, kept int) (restore func()) {
	oldAt, oldKeep := rotateAt, keep
	rotateAt, keep = at, kept
	return func() { rotateAt, keep = oldAt, oldKeep }
}

// AsTerminal makes every writer read as a terminal until the test ends.
func AsTerminal(t interface{ Cleanup(func()) }) {
	was := terminal
	terminal = func(io.Writer) bool { return true }
	t.Cleanup(func() { terminal = was })
}

// FailOpen makes the next n opens of a log file fail, until the test ends.
func FailOpen(t interface{ Cleanup(func()) }, n int) {
	was := openFile
	left := n
	openFile = func(name string, flag int, perm os.FileMode) (*os.File, error) {
		if left > 0 {
			left--
			return nil, errors.New("cannot open, as the test asked")
		}
		return was(name, flag, perm)
	}
	t.Cleanup(func() { openFile = was })
}
