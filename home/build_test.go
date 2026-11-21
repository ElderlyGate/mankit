package home_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tugbayatilla/mankit/v4/build"
	"github.com/tugbayatilla/mankit/v4/home"
)

// A test binary is stamped with nothing, so it is a dev build: its products' folders end in -dev, the org's folder does not.
func TestADevBuildKeepsItsOwnFolders(t *testing.T) {
	h := env(t, "")
	t.Setenv("XDG_DATA_HOME", "")
	if build.Released() {
		t.Fatal("an unstamped build says it is a release")
	}
	config := filepath.Join(h, ".config", "elderlygate")
	for _, c := range [][2]string{
		{home.Folder("acme"), "acme-dev"},
		{home.ID("acme-dev"), "acme"},
		{must(home.Product("acme")), filepath.Join(config, "acme-dev")},
		{must(home.ProductData("acme")), filepath.Join(h, ".local", "share", "elderlygate", "acme-dev")},
		{must(home.Config("acme", "/x")), filepath.Join("/x", "acme-dev", "config.yaml")},
		{must(home.MigratedMark("acme", "split")), filepath.Join(config, ".migrated", "acme-dev", "split")},
		{must(home.Dir()), config},
	} {
		if c[0] != c[1] {
			t.Errorf("got %q, want %q", c[0], c[1])
		}
	}
}

// The stamp set at build is all that decides: a release tag gives a release and the product's own folders, anything else a dev build and the -dev folders.
func TestTheStampDecidesReleaseAndFolders(t *testing.T) {
	for _, c := range []struct {
		stamp    string
		released bool
	}{
		{"v1.2.3", true},
		{"", false},
		{"v1.2.3-rc.1", false},
		{"dev-1218b88", false},
	} {
		t.Run("stamp "+c.stamp, func(t *testing.T) {
			bin := filepath.Join(t.TempDir(), "folders")
			args := []string{"build", "-o", bin}
			if c.stamp != "" {
				args = append(args, "-ldflags", "-X github.com/tugbayatilla/mankit/v4/build.version="+c.stamp)
			}
			if out, err := exec.Command("go", append(args, "./testdata/folders")...).CombinedOutput(); err != nil {
				t.Fatalf("go build: %v\n%s", err, out)
			}
			tmp := t.TempDir()
			run := exec.Command(bin)
			run.Env = append(os.Environ(), "HOME="+tmp, "XDG_CONFIG_HOME="+filepath.Join(tmp, "c"), "XDG_DATA_HOME="+filepath.Join(tmp, "d"))
			out, err := run.Output()
			if err != nil {
				t.Fatal(err)
			}
			folder := "acme-dev"
			if c.released {
				folder = "acme"
			}
			org := filepath.Join(tmp, "c", "elderlygate")
			want := []string{
				map[bool]string{true: "true", false: "false"}[c.released],
				filepath.Join(org, folder),
				filepath.Join(tmp, "d", "elderlygate", folder),
				org,
				filepath.Join(org, ".migrated", folder, "config.yaml"),
				"acme",
			}
			if got := strings.Split(strings.TrimSpace(string(out)), "\n"); strings.Join(got, "\n") != strings.Join(want, "\n") {
				t.Errorf("got\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
			}
		})
	}
}

func must(s string, err error) string {
	if err != nil {
		panic(err)
	}
	return s
}
