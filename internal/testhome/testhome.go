// Package testhome runs a package's tests in a home of their own, so no test reads or writes the real one.
package testhome

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// marker names the temp home of a run, so a test binary a test starts again, as a child process, keeps its parent's home.
const marker = "MANKIT_TEST_HOME"

// Run is a TestMain: it points HOME, XDG_CONFIG_HOME, XDG_DATA_HOME and XDG_STATE_HOME at one temp folder, runs the tests, and removes the folder.
// A test that sets one of them itself still wins for its own run.
func Run(m *testing.M) {
	if os.Getenv(marker) != "" {
		os.Exit(m.Run())
	}
	dir, err := os.MkdirTemp("", "mankit-test-home-")
	if err != nil {
		fmt.Fprintln(os.Stderr, "testhome:", err)
		os.Exit(1)
	}
	for name, sub := range map[string]string{marker: "", "HOME": "home", "XDG_CONFIG_HOME": "config", "XDG_DATA_HOME": "data", "XDG_STATE_HOME": "state"} {
		os.Setenv(name, filepath.Join(dir, sub))
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}
