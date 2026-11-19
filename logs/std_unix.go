//go:build unix

package logs

import (
	"os"

	"golang.org/x/sys/unix"
)

// toStd makes f the process's stdout and stderr.
func toStd(f *os.File) error {
	for _, fd := range []int{1, 2} {
		if err := unix.Dup2(int(f.Fd()), fd); err != nil {
			return err
		}
	}
	return nil
}
