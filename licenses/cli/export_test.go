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
