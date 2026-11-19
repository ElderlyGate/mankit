package serve

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/tugbayatilla/mankit/v3/internal/disk"
)

// The servers folder is the app's own: one that is a link is refused, and nothing is written where it leads.
func TestAServersFolderThatIsALinkIsRefused(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no owner or link checks")
	}
	base := t.TempDir()
	elsewhere, dir := filepath.Join(base, "elsewhere"), filepath.Join(base, "servers")
	if err := os.Mkdir(elsewhere, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, dir); err != nil {
		t.Fatal(err)
	}
	r := record{APIVersion: recordAPIVersion(), Kind: recordKind, Metadata: recordMetadata{App: "designer", Addr: "127.0.0.1:7799", PID: 1}}
	if err := r.save(dir); !errors.Is(err, disk.ErrNotOwned) {
		t.Errorf("save: err = %v, want ErrNotOwned", err)
	}
	if _, err := (App{ID: "designer"}).renewToken(dir); !errors.Is(err, disk.ErrNotOwned) {
		t.Errorf("renew: err = %v, want ErrNotOwned", err)
	}
	if entries, _ := os.ReadDir(elsewhere); len(entries) != 0 {
		t.Errorf("written through the link: %v", entries)
	}
}

// A token file that is a link is never renewed through: what it leads to stays as it was.
func TestRenewingALinkedTokenIsRefused(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no link checks")
	}
	dir := filepath.Join(t.TempDir(), "servers")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(t.TempDir(), "other")
	if err := os.WriteFile(other, []byte("keep\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(other, filepath.Join(dir, tokenName)); err != nil {
		t.Fatal(err)
	}
	if _, err := (App{ID: "designer"}).renewToken(dir); !errors.Is(err, disk.ErrLink) {
		t.Errorf("err = %v, want ErrLink", err)
	}
	if b, _ := os.ReadFile(other); string(b) != "keep\n" {
		t.Errorf("the link's target was written: %q", b)
	}
}
