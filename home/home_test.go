package home_test

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/tugbayatilla/mankit/v4/home"
)

// env points HOME and XDG_CONFIG_HOME at temp dirs and returns HOME.
func env(t *testing.T, xdg string) string {
	t.Helper()
	h := t.TempDir()
	t.Setenv("HOME", h)
	t.Setenv("XDG_CONFIG_HOME", xdg)
	return h
}

func TestPaths(t *testing.T) {
	xdg := t.TempDir()
	tests := []struct {
		name, configEnv, dataEnv string
		config, data             func(home string) string
	}{
		{"XDG folders", filepath.Join(xdg, "config"), filepath.Join(xdg, "data"),
			func(string) string { return filepath.Join(xdg, "config", "elderlygate") },
			func(string) string { return filepath.Join(xdg, "data", "elderlygate") }},
		{"no XDG folders", "", "",
			func(h string) string { return filepath.Join(h, ".config", "elderlygate") },
			func(h string) string { return filepath.Join(h, ".local", "share", "elderlygate") }},
		{"relative XDG folders are ignored", "rel", "rel",
			func(h string) string { return filepath.Join(h, ".config", "elderlygate") },
			func(h string) string { return filepath.Join(h, ".local", "share", "elderlygate") }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := env(t, tt.configEnv)
			t.Setenv("XDG_DATA_HOME", tt.dataEnv)
			config, data := tt.config(h), tt.data(h)
			dir, _ := home.Dir()
			product, _ := home.Product("gitman")
			file, _ := home.File("gitman", "split")
			dataDir, _ := home.Data()
			productData, _ := home.ProductData("control-room")
			got := []string{dir, product, file, dataDir, productData}
			want := []string{config, filepath.Join(config, "gitman-dev"),
				filepath.Join(config, "gitman-dev", "split"), data, filepath.Join(data, "control-room-dev")}
			for i := range got {
				if got[i] != want[i] {
					t.Errorf("got %q, want %q", got[i], want[i])
				}
			}
		})
	}
}

// The logs folder is the state folder on Linux and ~/Library/Logs on macOS (G-STACK-8), under the org's name.
func TestTheLogsFolder(t *testing.T) {
	h := env(t, "")
	xdg := t.TempDir()
	t.Setenv("XDG_STATE_HOME", xdg)
	want := filepath.Join(xdg, "elderlygate", "e-designer")
	if runtime.GOOS == "darwin" {
		want = filepath.Join(h, "Library", "Logs", "elderlygate", "e-designer")
	}
	if got, err := home.ProductLogs("e-designer"); err != nil || got != want {
		t.Errorf("ProductLogs = %q, %v; want %q", got, err, want)
	}
	if runtime.GOOS != "darwin" {
		t.Setenv("XDG_STATE_HOME", "")
		if got, _ := home.Logs(); got != filepath.Join(h, ".local", "state", "elderlygate") {
			t.Errorf("Logs with no XDG_STATE_HOME = %q", got)
		}
	}
}

// oldFile writes body to <system config dir>/<id>/<name>, where the apps kept their files before.
func oldFile(t *testing.T, id, name, body string) string {
	t.Helper()
	dir, err := os.UserConfigDir()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, id, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestFileOnlyNamesThePath(t *testing.T) {
	env(t, t.TempDir())
	oldFile(t, "gitman", "config.yaml", "theme: nord\n")
	path, err := home.File("gitman", "config.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("File copied or made a file: %v", err)
	}
}
