package store_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/ElderlyGate/contracts"

	"github.com/tugbayatilla/mankit/v3/serve"
	"github.com/tugbayatilla/mankit/v3/store"
	"github.com/tugbayatilla/mankit/v3/store/storetest"
)

var ctx = context.Background()

var app = store.App{ID: "storetest", Version: "1.0.0", Log: io.Discard}

// served opens dir/served as a served scope whose turns are kept the way e-designer keeps them: the folder's own in .queue/root, each thing's in .queue/thing/<thing>.
func served(t testing.TB, dir string) *store.Scope {
	t.Helper()
	root := filepath.Join(dir, "served")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	s, err := store.Served(app, root, lines(root))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func lines(root string) func(store.Key) string {
	return func(thing store.Key) string {
		if thing == "" {
			return filepath.Join(root, ".queue", "root")
		}
		return filepath.Join(root, ".queue", "thing", string(thing))
	}
}

// fresh is a temporary folder that is also this test's data folder, so journals stay inside it.
func fresh(t *testing.T) string {
	dir := t.TempDir()
	t.Setenv("XDG_DATA_HOME", filepath.Join(dir, "data"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, "config"))
	return dir
}

func TestFilesPassTheConformanceSuite(t *testing.T) {
	storetest.Run(t, storetest.Harness{
		New: func(t *testing.T) func() *store.Scope {
			dir := fresh(t)
			return func() *store.Scope { return served(t, dir) }
		},
		Crash: store.CrashAfter,
	})
}

// The data folder stands for both own scopes, which differ only in where they are; it also holds the journals.
func TestAnOwnScopePassesTheConformanceSuite(t *testing.T) {
	storetest.Run(t, storetest.Harness{
		New: func(t *testing.T) func() *store.Scope {
			fresh(t)
			return func() *store.Scope {
				s, err := store.Data(app)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { s.Close() })
				return s
			}
		},
		Crash: store.CrashAfter,
	})
}

func key(t testing.TB, s string) store.Key {
	t.Helper()
	k, err := store.ParseKey(s)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func reason(err error) string {
	var p *serve.Problem
	if errors.As(err, &p) {
		return p.Reason
	}
	return ""
}

func put(t testing.TB, s *store.Scope, k, body string) error {
	t.Helper()
	kk := key(t, k)
	return s.Change(ctx, "", "put", func(tx store.Tx) error { return tx.Put(kk, []byte(body), store.Anything()) })
}

func write(t testing.TB, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestALinkIsNeverReadOrWrittenThrough(t *testing.T) {
	dir := fresh(t)
	s := served(t, dir)
	root := filepath.Join(dir, "served")
	outside := filepath.Join(dir, "secret")
	write(t, outside, "secret\n")
	write(t, filepath.Join(root, "shop", "real.yml"), "real\n")
	for name, target := range map[string]string{"out.yml": outside, "in.yml": filepath.Join(root, "shop", "real.yml")} {
		if err := os.Symlink(target, filepath.Join(root, "shop", name)); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(filepath.Join(root, "shop"), filepath.Join(root, "via")); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"shop/out.yml", "shop/in.yml", "via/real.yml"} {
		if _, _, err := s.Get(key(t, k)); reason(err) != store.ReasonOutside {
			t.Errorf("Get(%s) = %v, want outside", k, err)
		}
		if err := put(t, s, k, "new\n"); reason(err) != store.ReasonOutside {
			t.Errorf("Put(%s) = %v, want outside", k, err)
		}
	}
	if got, _ := os.ReadFile(outside); string(got) != "secret\n" {
		t.Fatalf("the file outside holds %q", got)
	}
	if info, _ := os.Lstat(filepath.Join(root, "shop", "in.yml")); info.Mode()&fs.ModeSymlink == 0 {
		t.Fatal("a link was replaced")
	}
	if err := s.Change(ctx, "", "remove", func(tx store.Tx) error { return tx.Remove(key(t, "shop"), store.Anything()) }); reason(err) != store.ReasonOutside {
		t.Fatalf("removing a folder holding a link = %v, want outside", err)
	}
}

func TestAPipeIsRefusedWithoutWaiting(t *testing.T) {
	dir := fresh(t)
	s := served(t, dir)
	pipe := filepath.Join(dir, "served", "shop", "pipe.yml")
	if err := os.MkdirAll(filepath.Dir(pipe), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(pipe, 0o644); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, _, err := s.Get(key(t, "shop/pipe.yml")); done <- err }()
	select {
	case err := <-done:
		var p *serve.Problem
		if !errors.As(err, &p) || p.Type != serve.TypeRefused {
			t.Fatalf("Get of a pipe = %v, want refused", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Get of a pipe waited for a writer")
	}
}

func TestTheOwnScopesArePrivateAndServedFilesKeepTheirMode(t *testing.T) {
	dir := fresh(t)
	c, err := store.Config(app)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := put(t, c, "tokens/web.token", "t"); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(dir, "config", "elderlygate", app.ID)
	for path, want := range map[string]fs.FileMode{cfg: 0o700, filepath.Join(cfg, "tokens"): 0o700, filepath.Join(cfg, "tokens", "web.token"): 0o600} {
		if info, err := os.Stat(path); err != nil || info.Mode().Perm() != want {
			t.Errorf("%s: %v %v, want %v", path, info.Mode().Perm(), err, want)
		}
	}
	s := served(t, dir)
	kept := filepath.Join(dir, "served", "shop", "kept.yml")
	write(t, kept, "old\n")
	if err := os.Chmod(kept, 0o640); err != nil {
		t.Fatal(err)
	}
	if err := put(t, s, "shop/kept.yml", "new\n"); err != nil {
		t.Fatal(err)
	}
	if err := put(t, s, "shop/new.yml", "new\n"); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]fs.FileMode{kept: 0o640, filepath.Join(dir, "served", "shop", "new.yml"): 0o644} {
		if info, err := os.Stat(path); err != nil || info.Mode().Perm() != want {
			t.Errorf("%s: %v %v, want %v", path, info.Mode().Perm(), err, want)
		}
	}
}

func TestThisUsersOwnFolderOpenToOthersIsMadePrivateAndSaidSo(t *testing.T) {
	dir := fresh(t)
	data := filepath.Join(dir, "data", "elderlygate", app.ID)
	write(t, filepath.Join(data, "config.yaml"), "a: 1\n")
	write(t, filepath.Join(data, "runs", "one.json"), "{}")
	for p, mode := range map[string]fs.FileMode{data: 0o755, filepath.Join(data, "runs"): 0o755, filepath.Join(data, "config.yaml"): 0o644, filepath.Join(data, "runs", "one.json"): 0o664} {
		if err := os.Chmod(p, mode); err != nil {
			t.Fatal(err)
		}
	}
	var said bytes.Buffer
	a := app
	a.Log = &said
	s, err := store.Data(a)
	if err != nil {
		t.Fatalf("Data over this user's own folder open to others = %v, want it made private", err)
	}
	s.Close()
	for p, want := range map[string]fs.FileMode{data: 0o700, filepath.Join(data, "runs"): 0o700, filepath.Join(data, "config.yaml"): 0o600, filepath.Join(data, "runs", "one.json"): 0o600} {
		if info, err := os.Stat(p); err != nil || info.Mode().Perm() != want {
			t.Errorf("%s: %v %v, want %v", p, info.Mode().Perm(), err, want)
		}
		if !strings.Contains(said.String(), p+" was open to other users") {
			t.Errorf("not said for %s: %q", p, said.String())
		}
	}
}

func TestAnOwnFolderThatIsALinkIsNotUsed(t *testing.T) {
	dir := fresh(t)
	elsewhere := filepath.Join(dir, "elsewhere")
	if err := os.MkdirAll(elsewhere, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "data", "elderlygate"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, filepath.Join(dir, "data", "elderlygate", app.ID)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Data(app); reason(err) != store.ReasonNotOwned {
		t.Fatalf("Data over a link = %v, want not_owned", err)
	}
}

// markOf writes a mark another user left on host into the queue folder of line, as their change cut short would.
func markOf(t *testing.T, line, user, host string, folder bool, keys ...string) string {
	t.Helper()
	id := "17000000000000000001"
	m := map[string]any{
		"apiVersion": store.APIVersion(), "kind": store.UnfinishedKind,
		"metadata": map[string]any{"id": id},
		"spec":     map[string]any{"user": user, "host": host, "journal": id, "keys": keys},
	}
	if folder {
		m["spec"].(map[string]any)["folder"] = true
	}
	data, _ := json.Marshal(m)
	if err := contracts.Validate("mankit/v1/store-unfinished.schema.json", data); err != nil {
		t.Fatalf("the test's own mark: %v", err)
	}
	dir := filepath.Join(line, ".queue")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(dir, ".unfinished-"+id+".json"), string(data))
	return id
}

func TestAnotherUsersUnfinishedChangeHoldsItsKeysUntilCleared(t *testing.T) {
	dir := fresh(t)
	s := served(t, dir)
	if err := put(t, s, "shop/a.yml", "a"); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(dir, "served")
	markOf(t, lines(root)("shop"), "alice", "elsewhere", false, "shop/a.yml", "shop/b.yml")
	err := put(t, s, "shop/a.yml", "mine")
	var p *serve.Problem
	if !errors.As(err, &p) || p.Reason != store.ReasonUnfinished || p.Type != serve.TypeRefused {
		t.Fatalf("a change to a held key = %v, want refused unfinished_change", err)
	}
	for _, says := range []string{"alice", "elsewhere", "storetest store unfinished clear shop"} {
		if !strings.Contains(p.Detail, says) {
			t.Errorf("the refusal %q does not name %q", p.Detail, says)
		}
	}
	if len(p.At) != 1 || p.At[0].File != "shop/a.yml" {
		t.Errorf("the refusal points at %v", p.At)
	}
	if err := put(t, s, "shop/c.yml", "free"); err != nil {
		t.Fatalf("a key the mark does not name: %v", err)
	}
	if err := s.Change(ctx, "", "remove", func(tx store.Tx) error { return tx.Remove(key(t, "shop"), store.Anything()) }); reason(err) != store.ReasonUnfinished {
		t.Fatalf("removing the folder holding a held key = %v, want unfinished_change", err)
	}
	got, err := s.Unfinished("shop")
	if err != nil || len(got) != 1 || got[0].User != "alice" || got[0].Host != "elsewhere" || len(got[0].Keys) != 2 {
		t.Fatalf("Unfinished = %+v, %v", got, err)
	}
	if cleared, err := s.Clear(ctx, "shop"); err != nil || len(cleared) != 1 {
		t.Fatalf("Clear = %+v, %v", cleared, err)
	}
	if err := put(t, s, "shop/a.yml", "mine"); err != nil {
		t.Fatalf("once cleared: %v", err)
	}
}

func TestAnUnfinishedChangeToTheFolderHoldsTheFolder(t *testing.T) {
	dir := fresh(t)
	s := served(t, dir)
	root := filepath.Join(dir, "served")
	markOf(t, lines(root)(""), "alice", "elsewhere", true, "shop")
	if err := put(t, s, "other/a.yml", "a"); reason(err) != store.ReasonUnfinished {
		t.Fatalf("a change in the folder's turn = %v, want unfinished_change", err)
	}
}

func TestAMarkThatCannotBeReadIsNeverTakenForNone(t *testing.T) {
	dir := fresh(t)
	s := served(t, dir)
	line := filepath.Join(lines(filepath.Join(dir, "served"))("shop"), ".queue")
	write(t, filepath.Join(line, ".unfinished-17000000000000000001.json"), "{not json")
	if err := put(t, s, "shop/a.yml", "a"); reason(err) != store.ReasonUnfinished {
		t.Fatalf("a change beside a broken mark = %v, want unfinished_change", err)
	}
}

// cutShort runs a change of three writes stopped dead after its n-th, and gives back the journal and the marks it left.
func cutShort(t *testing.T, s *store.Scope, n int) (journal string, marks []string) {
	t.Helper()
	fired := store.CrashAfter(n)
	_ = s.Change(ctx, "shop", "three writes", func(tx store.Tx) error {
		for _, k := range []string{"shop/a.yml", "shop/b.yml", "shop/c/d.yml"} {
			if err := tx.Put(key(t, k), []byte("new "+k), store.Anything()); err != nil {
				return err
			}
		}
		return nil
	})
	if !fired() {
		t.Fatalf("the change was not stopped after write %d", n)
	}
	data := os.Getenv("XDG_DATA_HOME")
	journals, _ := filepath.Glob(filepath.Join(data, "elderlygate", app.ID, ".store", "*", "*.jsonl"))
	marks, _ = filepath.Glob(filepath.Join(filepath.Dir(data), "served", ".queue", "*", "*", ".queue", ".unfinished-*.json"))
	more, _ := filepath.Glob(filepath.Join(filepath.Dir(data), "served", ".queue", "root", ".queue", ".unfinished-*.json"))
	marks = append(marks, more...)
	if len(journals) != 1 {
		t.Fatalf("journals left: %v", journals)
	}
	return journals[0], marks
}

func TestTheJournalAndTheMarksAreInTheirContractShape(t *testing.T) {
	dir := fresh(t)
	s := served(t, dir)
	if err := put(t, s, "shop/a.yml", "a"); err != nil {
		t.Fatal(err)
	}
	journal, marks := cutShort(t, s, 4)
	raw, _ := os.ReadFile(journal)
	scan := bufio.NewScanner(bytes.NewReader(raw))
	for n := 0; scan.Scan(); n++ {
		schema := "mankit/v1/store-journal-change.schema.json"
		if n == 0 {
			schema = "mankit/v1/store-journal.schema.json"
		}
		if err := contracts.Validate(schema, scan.Bytes()); err != nil {
			t.Fatalf("journal line %d %s: %v", n+1, scan.Bytes(), err)
		}
	}
	if len(marks) == 0 {
		t.Fatal("no mark left")
	}
	for _, m := range marks {
		data, _ := os.ReadFile(m)
		if err := contracts.Validate("mankit/v1/store-unfinished.schema.json", data); err != nil {
			t.Fatalf("%s: %v", m, err)
		}
	}
	if info, _ := os.Stat(journal); info.Mode().Perm() != 0o600 {
		t.Fatalf("the journal's mode is %v", info.Mode().Perm())
	}
	if info, _ := os.Stat(filepath.Dir(journal)); info.Mode().Perm() != 0o700 {
		t.Fatalf("the journal folder's mode is %v", info.Mode().Perm())
	}
}

func TestAJournalWhoseWriterStillRunsIsNotPutBack(t *testing.T) {
	dir := fresh(t)
	s := served(t, dir)
	journal, _ := cutShort(t, s, 4)
	raw, _ := os.ReadFile(journal)
	head, rest, _ := bytes.Cut(raw, []byte("\n"))
	var h map[string]any
	_ = json.Unmarshal(head, &h)
	// Process 1 always runs.
	h["metadata"].(map[string]any)["pid"] = 1
	head, _ = json.Marshal(h)
	if err := os.WriteFile(journal, append(append(head, '\n'), rest...), 0o600); err != nil {
		t.Fatal(err)
	}
	err := put(t, s, "shop/a.yml", "x")
	if reason(err) != store.ReasonUnfinished || !strings.Contains(err.Error(), "still runs") {
		t.Fatalf("a change beside a journal whose writer runs = %v", err)
	}
}

func TestAJournalOthersCanWriteIsNotPutBack(t *testing.T) {
	dir := fresh(t)
	s := served(t, dir)
	journal, _ := cutShort(t, s, 4)
	if err := os.Chmod(journal, 0o666); err != nil {
		t.Fatal(err)
	}
	if err := put(t, s, "shop/a.yml", "x"); reason(err) != store.ReasonNotOwned {
		t.Fatalf("a change beside a journal others can write = %v, want not_owned", err)
	}
}

func TestAJournalThatCannotBeReadPutsNothingBack(t *testing.T) {
	dir := fresh(t)
	s := served(t, dir)
	journal, _ := cutShort(t, s, 5)
	raw, _ := os.ReadFile(journal)
	lines := bytes.Split(raw, []byte("\n"))
	lines[1] = []byte("{broken")
	if err := os.WriteFile(journal, bytes.Join(lines, []byte("\n")), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := put(t, s, "shop/a.yml", "x"); reason(err) != store.ReasonUnfinished {
		t.Fatalf("a change beside a broken journal = %v, want unfinished_change", err)
	}
	if _, err := os.Stat(journal); err != nil {
		t.Fatalf("the broken journal went: %v", err)
	}
}

// A process killed for real after a write, its tickets and locks left behind, is put back by the next process.
func TestAProcessKilledHalfWayIsPutBack(t *testing.T) {
	if dir := os.Getenv("STORE_KILLED_IN"); dir != "" {
		s := served(t, dir)
		store.ExitAfter(4)
		_ = s.Change(ctx, "shop", "three writes", func(tx store.Tx) error {
			for _, k := range []string{"shop/a.yml", "shop/b.yml", "shop/c/d.yml"} {
				if err := tx.Put(key(t, k), []byte("new"), store.Anything()); err != nil {
					return err
				}
			}
			return nil
		})
		t.Fatal("the change was not stopped")
	}
	dir := fresh(t)
	s := served(t, dir)
	if err := put(t, s, "shop/a.yml", "a"); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestAProcessKilledHalfWayIsPutBack$")
	cmd.Env = append(os.Environ(), "STORE_KILLED_IN="+dir)
	out, err := cmd.CombinedOutput()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 3 {
		t.Fatalf("the child ended with %v: %s", err, out)
	}
	if err := put(t, s, "shop/e.yml", "e"); err != nil {
		t.Fatal(err)
	}
	keys, _ := s.List("")
	if fmt.Sprint(keys) != "[shop/a.yml shop/e.yml]" {
		t.Fatalf("after the kill and the next change: %v", keys)
	}
	if item, _, _ := s.Get(key(t, "shop/a.yml")); string(item.Body) != "a" {
		t.Fatalf("shop/a.yml holds %q", item.Body)
	}
}

func TestAFolderHoldingANameNoKeyCanHoldIsNotRemoved(t *testing.T) {
	dir := fresh(t)
	s := served(t, dir)
	if err := put(t, s, "shop/a.yml", "a"); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(dir, "served", "shop", "f", "x\\y.yml"), "odd")
	err := s.Change(ctx, "shop", "two", func(tx store.Tx) error {
		if err := tx.Put(key(t, "shop/a.yml"), []byte("changed"), store.Anything()); err != nil {
			return err
		}
		return tx.Remove(key(t, "shop/f"), store.Anything())
	})
	if reason(err) != store.ReasonOutside {
		t.Fatalf("removing a folder holding x\\y.yml = %v, want outside", err)
	}
	if item, _, _ := s.Get(key(t, "shop/a.yml")); string(item.Body) != "a" {
		t.Fatalf("shop/a.yml holds %q after the refused change", item.Body)
	}
	if _, err := os.Stat(filepath.Join(dir, "served", "shop", "f", "x\\y.yml")); err != nil {
		t.Fatalf("the odd file went: %v", err)
	}
}

func TestTemporaryFilesACrashLeftDoNotStopPuttingBack(t *testing.T) {
	dir := fresh(t)
	s := served(t, dir)
	if err := put(t, s, "shop/a.yml", "a"); err != nil {
		t.Fatal(err)
	}
	// Stopped after the marks, the journal and shop/a.yml, before shop/b.yml and shop/c/d.yml.
	cutShort(t, s, 3)
	shop := filepath.Join(dir, "served", "shop")
	write(t, filepath.Join(shop, ".a.yml.~disk-0123456789ab"), "half")
	if err := os.MkdirAll(filepath.Join(shop, "c"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(shop, "c", ".d.yml.~disk-0123456789ab"), "half")
	if err := put(t, s, "shop/e.yml", "e"); err != nil {
		t.Fatalf("putting back beside temporary files: %v", err)
	}
	keys, _ := s.List("")
	if fmt.Sprint(keys) != "[shop/a.yml shop/e.yml]" {
		t.Fatalf("after putting back: %v", keys)
	}
	if left, _ := filepath.Glob(filepath.Join(shop, ".*~disk-*")); len(left) != 0 {
		t.Fatalf("temporary files left: %v", left)
	}
}

func TestAThingMovedAwayWhileAChangeWaitedIsAddedInTheFolderTurn(t *testing.T) {
	dir := fresh(t)
	s := served(t, dir)
	if err := put(t, s, "shop/a.yml", "a"); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(dir, "served")
	holding, release := make(chan struct{}), make(chan struct{})
	first := make(chan error, 1)
	go func() {
		first <- s.Change(ctx, "", "remove shop", func(tx store.Tx) error {
			// Staged first, so this change holds shop's turn before the other waits for it.
			err := tx.Remove(key(t, "shop"), store.Anything())
			close(holding)
			<-release
			// Another user's change to the folder, cut short while this one ran: only a change in the folder's turn meets it.
			markOf(t, lines(root)(""), "alice", "elsewhere", true, "other")
			return err
		})
	}()
	<-holding
	second := make(chan error, 1)
	go func() { second <- put2(s, "shop", "shop/b.yml") }()
	time.Sleep(300 * time.Millisecond)
	close(release)
	if err := <-first; err != nil {
		t.Fatal(err)
	}
	if err := <-second; reason(err) != store.ReasonUnfinished {
		t.Fatalf("the waiting change = %v, want it to take the folder's turn and meet the mark there", err)
	}
}

// put2 writes key in thing's own turn.
func put2(s *store.Scope, thing, k string) error {
	return s.Change(ctx, store.Key(thing), "put", func(tx store.Tx) error { return tx.Put(store.Key(k), []byte("x"), store.Anything()) })
}

func TestAJournalNamingTheScopeThroughALinkIsPutBack(t *testing.T) {
	dir := fresh(t)
	real := served(t, dir)
	if err := put(t, real, "shop/a.yml", "a"); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(filepath.Join(dir, "served"), link); err != nil {
		t.Fatal(err)
	}
	viaLink, err := store.Served(app, link, lines(filepath.Join(dir, "served")))
	if err != nil {
		t.Fatal(err)
	}
	defer viaLink.Close()
	cutShort(t, viaLink, 4)
	if err := put(t, real, "shop/e.yml", "e"); err != nil {
		t.Fatal(err)
	}
	if keys, _ := real.List(""); fmt.Sprint(keys) != "[shop/a.yml shop/e.yml]" {
		t.Fatalf("after putting back: %v", keys)
	}
}

func TestAMovedFileKeepsItsModeAndPuttingBackGivesItBack(t *testing.T) {
	dir := fresh(t)
	s := served(t, dir)
	shop := filepath.Join(dir, "served", "shop")
	write(t, filepath.Join(shop, "kept.yml"), "k")
	if err := os.Chmod(filepath.Join(shop, "kept.yml"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := s.Change(ctx, "shop", "move", func(tx store.Tx) error {
		return tx.Move(key(t, "shop/kept.yml"), key(t, "shop/moved/kept.yml"), store.Anything())
	}); err != nil {
		t.Fatal(err)
	}
	moved := filepath.Join(shop, "moved", "kept.yml")
	if info, err := os.Stat(moved); err != nil || info.Mode().Perm() != 0o640 {
		t.Fatalf("the moved file: %v %v, want 0640", info.Mode().Perm(), err)
	}
	err := s.Change(ctx, "shop", "remove, then a write another writer blocks", func(tx store.Tx) error {
		if err := tx.Remove(key(t, "shop/moved/kept.yml"), store.Anything()); err != nil {
			return err
		}
		if err := tx.Put(key(t, "shop/b.yml"), []byte("b"), store.Absent()); err != nil {
			return err
		}
		// Written outside the store after the Want was met, so the write fails at commit, after the remove.
		return os.MkdirAll(filepath.Join(shop, "b.yml"), 0o755)
	})
	if reason(err) != store.ReasonChanged {
		t.Fatalf("the blocked change = %v, want changed", err)
	}
	if info, err := os.Stat(moved); err != nil || info.Mode().Perm() != 0o640 {
		t.Fatalf("the file put back: %v %v, want 0640", info, err)
	}
}

func TestTxGetSeesTheChangesOwnWritesAndWantsAreCheckedAsTheyAreStaged(t *testing.T) {
	dir := fresh(t)
	s := served(t, dir)
	if err := put(t, s, "shop/a.yml", "a"); err != nil {
		t.Fatal(err)
	}
	if err := put(t, s, "shop/f/b.yml", "b"); err != nil {
		t.Fatal(err)
	}
	err := s.Change(ctx, "shop", "staged", func(tx store.Tx) error {
		if err := tx.Put(key(t, "shop/a.yml"), []byte("a2"), store.Holds(store.SumOf([]byte("a")))); err != nil {
			return err
		}
		item, there, err := tx.Get(key(t, "shop/a.yml"))
		if err != nil || !there || string(item.Body) != "a2" {
			t.Fatalf("Get after Put = %q %v %v, want a2", item.Body, there, err)
		}
		if err := tx.Put(key(t, "shop/a.yml"), []byte("a3"), store.Holds(store.SumOf([]byte("a")))); reason(err) != store.ReasonChanged {
			t.Fatalf("a Want the staged write no longer meets = %v, want changed at once", err)
		}
		if err := tx.Put(key(t, "shop/a.yml"), []byte("a3"), store.Holds(item.Sum)); err != nil {
			return err
		}
		if err := tx.Move(key(t, "shop/f"), key(t, "shop/g"), store.Anything()); err != nil {
			return err
		}
		if item, there, _ := tx.Get(key(t, "shop/g/b.yml")); !there || string(item.Body) != "b" {
			t.Fatalf("Get under a folder's new name = %q %v", item.Body, there)
		}
		if _, there, _ := tx.Get(key(t, "shop/f/b.yml")); there {
			t.Fatal("Get under a folder's old name still finds it")
		}
		if err := tx.Move(key(t, "shop/a.yml"), key(t, "shop/g/b.yml"), store.Anything()); reason(err) != store.ReasonChanged {
			t.Fatalf("a move onto a name the change took = %v, want changed", err)
		}
		if err := tx.Remove(key(t, "shop/g"), store.Anything()); err != nil {
			return err
		}
		if _, there, _ := tx.Get(key(t, "shop/g/b.yml")); there {
			t.Fatal("Get inside a removed folder still finds it")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if keys, _ := s.List(""); fmt.Sprint(keys) != "[shop/a.yml]" {
		t.Fatalf("after the change: %v", keys)
	}
	if item, _, _ := s.Get(key(t, "shop/a.yml")); string(item.Body) != "a3" {
		t.Fatalf("shop/a.yml holds %q", item.Body)
	}
}

func TestAStuckChangeOfAnotherThingDoesNotStopThisOne(t *testing.T) {
	dir := fresh(t)
	s := served(t, dir)
	if err := put(t, s, "shop/a.yml", "a"); err != nil {
		t.Fatal(err)
	}
	if err := put(t, s, "other/a.yml", "a"); err != nil {
		t.Fatal(err)
	}
	journal, _ := cutShort(t, s, 5)
	raw, _ := os.ReadFile(journal)
	lines := bytes.Split(raw, []byte("\n"))
	lines[1] = []byte("{broken")
	if err := os.WriteFile(journal, bytes.Join(lines, []byte("\n")), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := put2(s, "other", "other/b.yml"); err != nil {
		t.Fatalf("a change of another thing = %v, want it to go on", err)
	}
	if err := put2(s, "shop", "shop/x.yml"); reason(err) != store.ReasonUnfinished {
		t.Fatalf("a change of the stuck thing = %v, want unfinished_change", err)
	}
}

func TestAJournalOfAnotherMarkIsNotPutBack(t *testing.T) {
	dir := fresh(t)
	s := served(t, dir)
	journal, _ := cutShort(t, s, 4)
	raw, _ := os.ReadFile(journal)
	head, rest, _ := bytes.Cut(raw, []byte("\n"))
	var h map[string]any
	_ = json.Unmarshal(head, &h)
	h["spec"].(map[string]any)["mark"] = "17000000000000000009"
	head, _ = json.Marshal(h)
	if err := os.WriteFile(journal, append(append(head, '\n'), rest...), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := put(t, s, "shop/a.yml", "x"); reason(err) != store.ReasonUnfinished || !strings.Contains(err.Error(), "not the journal the mark") {
		t.Fatalf("a journal whose mark is another = %v", err)
	}
}

func TestAQueueFolderThatCannotBeReadIsNeverTakenForNoMarks(t *testing.T) {
	dir := fresh(t)
	s := served(t, dir)
	if err := put(t, s, "shop/a.yml", "a"); err != nil {
		t.Fatal(err)
	}
	q := filepath.Join(lines(filepath.Join(dir, "served"))("shop"), ".queue")
	if err := os.Chmod(q, 0); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(q, 0o700)
	if err := put2(s, "shop", "shop/b.yml"); err == nil {
		t.Fatal("a change went through beside a queue folder that cannot be read")
	}
}

func TestAJournalLeftAfterItsMarksAreClearedIsShown(t *testing.T) {
	dir := fresh(t)
	s := served(t, dir)
	journal, _ := cutShort(t, s, 5)
	raw, _ := os.ReadFile(journal)
	lines := bytes.Split(raw, []byte("\n"))
	lines[1] = []byte("{broken")
	_ = os.WriteFile(journal, bytes.Join(lines, []byte("\n")), 0o600)
	cleared, err := s.Clear(ctx, "shop")
	if err != nil || len(cleared) != 1 || cleared[0].File != journal {
		t.Fatalf("Clear = %+v, %v, want the journal named", cleared, err)
	}
	// A journal that cannot be read is still said, never taken for none.
	if _, err := s.Unfinished("shop"); reason(err) != store.ReasonUnfinished {
		t.Fatalf("Unfinished beside a broken journal = %v", err)
	}
	_ = os.WriteFile(journal, raw, 0o600)
	left, err := s.Unfinished("shop")
	if err != nil || len(left) != 1 || !left[0].Left || left[0].File != journal {
		t.Fatalf("Unfinished after Clear = %+v, %v, want the journal left", left, err)
	}
}

func BenchmarkPut(b *testing.B) {
	dir := b.TempDir()
	b.Setenv("XDG_DATA_HOME", filepath.Join(dir, "data"))
	s := served(b, dir)
	k := key(b, "shop/a.yml")
	body := bytes.Repeat([]byte("x"), 4096)
	b.ResetTimer()
	for b.Loop() {
		if err := s.Change(ctx, "shop", "put", func(tx store.Tx) error { return tx.Put(k, body, store.Anything()) }); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkChangeOfThreeWrites(b *testing.B) {
	dir := b.TempDir()
	b.Setenv("XDG_DATA_HOME", filepath.Join(dir, "data"))
	s := served(b, dir)
	keys := []store.Key{key(b, "shop/a.yml"), key(b, "shop/b.yml"), key(b, "shop/c.yml")}
	body := bytes.Repeat([]byte("x"), 4096)
	b.ResetTimer()
	for b.Loop() {
		err := s.Change(ctx, "shop", "three", func(tx store.Tx) error {
			for _, k := range keys {
				if err := tx.Put(k, body, store.Anything()); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			b.Fatal(err)
		}
	}
}
