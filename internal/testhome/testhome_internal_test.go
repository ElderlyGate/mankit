package testhome

import (
	"path/filepath"
	"testing"
)

// Only a home a parent run set up is kept: a marker alone, a HOME outside it or an XDG variable unset gets a fresh temp home; an XDG folder of a test's own is handed down.
func TestOnlyAParentRunsHomeIsInherited(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "mankit-test-home-1")
	parent := map[string]string{marker: dir}
	for name, sub := range folders {
		parent[name] = filepath.Join(dir, sub)
	}
	with := func(changes map[string]string) func(string) string {
		env := map[string]string{}
		for k, v := range parent {
			env[k] = v
		}
		for k, v := range changes {
			env[k] = v
		}
		return func(k string) string { return env[k] }
	}
	for _, c := range []struct {
		name    string
		changes map[string]string
		want    bool
	}{
		{"the parent's whole home", nil, true},
		{"no marker", map[string]string{marker: ""}, false},
		{"a marker set by hand", map[string]string{marker: "1"}, false},
		{"a marker naming another folder", map[string]string{marker: t.TempDir()}, false},
		{"HOME elsewhere", map[string]string{"HOME": t.TempDir()}, false},
		{"XDG_STATE_HOME unset", map[string]string{"XDG_STATE_HOME": ""}, false},
		{"XDG_CONFIG_HOME relative", map[string]string{"XDG_CONFIG_HOME": "config"}, false},
		{"XDG_DATA_HOME of a test's own", map[string]string{"XDG_DATA_HOME": t.TempDir()}, true},
	} {
		if got := inherited(with(c.changes)); got != c.want {
			t.Errorf("%s: inherited = %v, want %v", c.name, got, c.want)
		}
	}
}
