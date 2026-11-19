package store

import (
	"os"

	"github.com/spf13/cobra"

	"github.com/tugbayatilla/mankit/v3/ask"
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

// AskWith stands in fn for the name question store unfinished clear asks, until the test ends.
func AskWith(t interface{ Cleanup(func()) }, fn func(*cobra.Command, ask.Action) error) {
	was := askName
	askName = fn
	t.Cleanup(func() { askName = was })
}
