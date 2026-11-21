// Package testhome runs a package's tests in a home of their own, so no test reads or writes the real one.
package testhome

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// marker names the temp home of a run, so a test binary a test starts again, as a child process, keeps its parent's home.
const marker = "MANKIT_TEST_HOME"

// prefix begins the name of every temp home Run makes.
const prefix = "mankit-test-home-"

// folders are the variables Run points into the temp home, each at its own folder in it.
var folders = map[string]string{"HOME": "home", "XDG_CONFIG_HOME": "config", "XDG_DATA_HOME": "data", "XDG_STATE_HOME": "state"}

// Run is a TestMain: it points HOME, XDG_CONFIG_HOME, XDG_DATA_HOME and XDG_STATE_HOME at one temp folder, runs the tests, and removes the folder.
// A test that sets one of them itself still wins for its own run. A test that panics ends the run before the removal, which leaves a mankit-test-home-* folder in TMPDIR, never in the real home.
func Run(m *testing.M) {
	if inherited(os.Getenv) {
		os.Exit(m.Run())
	}
	dir, err := os.MkdirTemp("", prefix+"*")
	if err != nil {
		fmt.Fprintln(os.Stderr, "testhome:", err)
		os.Exit(1)
	}
	os.Setenv(marker, dir)
	for name, sub := range folders {
		os.Setenv(name, filepath.Join(dir, sub))
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

// inherited reports whether the run is a child of a run that set up its home: the marker names one of Run's temp folders and HOME is the home in it.
// The XDG variables must be set and absolute but may point elsewhere, as a test that sets its own (t.Setenv) and then starts a child hands those down. A marker alone, set by hand, is not enough.
func inherited(getenv func(string) string) bool {
	dir := getenv(marker)
	if !filepath.IsAbs(dir) || !strings.HasPrefix(filepath.Base(dir), prefix) || getenv("HOME") != filepath.Join(dir, folders["HOME"]) {
		return false
	}
	for name := range folders {
		if !filepath.IsAbs(getenv(name)) {
			return false
		}
	}
	return true
}
