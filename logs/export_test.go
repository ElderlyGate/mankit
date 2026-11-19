package logs

// Rotation sets the size a log file rotates at and how many are kept, for one test, and gives back the func that restores them.
func Rotation(at int64, kept int) (restore func()) {
	oldAt, oldKeep := rotateAt, keep
	rotateAt, keep = at, kept
	return func() { rotateAt, keep = oldAt, oldKeep }
}
