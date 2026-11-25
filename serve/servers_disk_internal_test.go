package serve

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"go.naywee.com/mankit/v5/problem"
	"go.naywee.com/mankit/v5/store"
)

// The servers folder is in the app's own data folder, written through the store: one that is a link is refused, and nothing is written where it leads.
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
	if err := (App{ID: "designer"}).save(dir, r); !outside(err) {
		t.Errorf("save: err = %v, want the store's outside", err)
	}
	if _, err := (App{ID: "designer"}).renewToken(dir); !outside(err) {
		t.Errorf("renew: err = %v, want the store's outside", err)
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
	if _, err := (App{ID: "designer"}).renewToken(dir); !outside(err) {
		t.Errorf("err = %v, want the store's outside", err)
	}
	if b, _ := os.ReadFile(other); string(b) != "keep\n" {
		t.Errorf("the link's target was written: %q", b)
	}
}

// outside says whether err is the store's refusal of a link or a key that leaves the scope.
func outside(err error) bool {
	var p *problem.Problem
	return errors.As(err, &p) && p.Type == problem.TypeRefused && p.Reason == store.ReasonOutside
}
