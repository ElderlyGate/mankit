package logs

import "io"

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
