//go:build !unix

package store

// alive takes every process for running where the system cannot be asked, so a journal is never put back while its writer may run.
func alive(int) bool { return true }
