//go:build !unix

package logs

import "os"

// toStd leaves stdout and stderr as they are where the system cannot point them elsewhere.
func toStd(*os.File) error { return nil }
