package store

import (
	"os"
)

// CrashAfter stops the next change dead after its n-th write; fired says whether it did, and ends the crash either way.
func CrashAfter(n int) (fired func() bool) {
	crashed.Store(false)
	crashAfter.Store(int64(n))
	return func() bool {
		crashAfter.Store(0)
		return crashed.Load()
	}
}

// ExitAfter ends this process with code 3 after the next change's n-th write, as a process killed there would end.
func ExitAfter(n int) {
	onCrash = func() { os.Exit(3) }
	crashAfter.Store(int64(n))
}

// BeforeReadBack runs fn once a change's writes are made and before they are read back, until the test ends.
func BeforeReadBack(t interface{ Cleanup(func()) }, fn func()) {
	beforeReadBack = fn
	t.Cleanup(func() { beforeReadBack = nil })
}
