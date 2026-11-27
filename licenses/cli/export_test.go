package cli

import (
	"testing"
	"time"
)

// SetNow lets a test fix the day the Checker reads by, until the test ends.
func SetNow(t *testing.T, f func() time.Time) {
	now = f
	t.Cleanup(func() { now = nil })
}

// SetLine lets a test fix the product line the Checker reads by, in place of the build stamp, until the test ends.
func SetLine(t *testing.T, n int) {
	line = n
	t.Cleanup(func() { line = 0 })
}
