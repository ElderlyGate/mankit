package store_test

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ElderlyGate/contracts"
	"github.com/spf13/cobra"

	"github.com/tugbayatilla/mankit/v3/ask"
	"github.com/tugbayatilla/mankit/v3/helpout"
	"github.com/tugbayatilla/mankit/v3/prefs"
	"github.com/tugbayatilla/mankit/v3/serve"
	"github.com/tugbayatilla/mankit/v3/store"
)

// The tests of <app> store unfinished list, in ADR-S19's six parts; unfinishedRun is shared with the clear tests.

// unfinishedRun runs `store unfinished` words under a store command of its own, as an app mounts it, on the served scope in dir, with no terminal; confirm nil asks as prefs does.
func unfinishedRun(t *testing.T, dir string, confirm func(*cobra.Command, ask.Action) error, words ...string) (out string, code int, err error) {
	t.Helper()
	a := serve.App{ID: app.ID, Version: app.Version, Contracts: []serve.Contract{{Version: "v1"}}}
	if confirm == nil {
		config := filepath.Join(dir, "config.yaml")
		confirm = prefs.Asker{Config: func() string { return config }}.Ask
	}
	root := filepath.Join(dir, "served")
	open := func() (*store.Scope, error) { return store.Served(app, root, lines(root)) }
	cli := &cobra.Command{Use: app.ID}
	group := &cobra.Command{Use: "store"}
	group.AddCommand(store.UnfinishedCommand(a, open, confirm))
	cli.AddCommand(group)
	var buf bytes.Buffer
	cli.SetOut(&buf)
	cli.SetErr(&buf)
	cli.SetIn(strings.NewReader(""))
	cli.SetArgs(append([]string{"store", "unfinished"}, words...))
	code, err = helpout.Execute(context.Background(), cli)
	return buf.String(), code, err
}

// 1. Success: every user's marks, and this user's own change with its journal file.
func TestUnfinishedListShowsEveryUsersMarksAndThisUsersJournal(t *testing.T) {
	dir := fresh(t)
	s := served(t, dir)
	if err := put(t, s, "cart/b.yml", "b"); err != nil {
		t.Fatal(err)
	}
	journal, _ := cutShort(t, s, 5)
	// A thing that is not there is added in the folder's turn too, so its mark is found there; this one's thing is there.
	markOf(t, lines(filepath.Join(dir, "served"))("cart"), "alice", "elsewhere", false, "cart/a.yml")
	out, code, err := unfinishedRun(t, dir, nil, "list")
	if err != nil || code != 0 {
		t.Fatalf("list = %d, %v\n%s", code, err, out)
	}
	for _, says := range []string{"MARK", "alice", "elsewhere", "cart/a.yml", journal, "shop/a.yml", "holds"} {
		if !strings.Contains(out, says) {
			t.Errorf("list does not show %q:\n%s", says, out)
		}
	}
	one, _, err := unfinishedRun(t, dir, nil, "list", "cart")
	if err != nil || !strings.Contains(one, "alice") || strings.Contains(one, journal) {
		t.Fatalf("list cart shows other things or not cart's: %v\n%s", err, one)
	}
	if _, err := s.Clear(ctx, "shop"); err != nil {
		t.Fatal(err)
	}
	left, _, err := unfinishedRun(t, dir, nil, "list", "shop")
	if err != nil || !strings.Contains(left, "left") || !strings.Contains(left, journal) {
		t.Fatalf("a journal whose marks are gone is not shown left: %v\n%s", err, left)
	}
	empty := t.TempDir()
	served(t, empty)
	none, _, err := unfinishedRun(t, empty, nil, "list")
	if err != nil || !strings.Contains(none, "no change is cut short") {
		t.Fatalf("an empty scope: %v\n%s", err, none)
	}
}

// 2. Contract: -o json is a StoreUnfinishedList, with a left journal and with none.
func TestUnfinishedListAnswersInItsContractShape(t *testing.T) {
	dir := fresh(t)
	s := served(t, dir)
	empty, _, err := unfinishedRun(t, dir, nil, "list", "-o", "json")
	if err != nil {
		t.Fatal(err)
	}
	if err := contracts.Validate("mankit/v1/store-unfinished-list.schema.json", []byte(empty)); err != nil || !strings.Contains(empty, `"changes": []`) {
		t.Fatalf("an empty list: %v\n%s", err, empty)
	}
	cutShort(t, s, 5)
	markOf(t, lines(filepath.Join(dir, "served"))(""), "alice", "elsewhere", true, "cart")
	if _, err := s.Clear(ctx, "shop"); err != nil {
		t.Fatal(err)
	}
	out, _, err := unfinishedRun(t, dir, nil, "list", "-o", "json")
	if err != nil {
		t.Fatal(err)
	}
	if err := contracts.Validate("mankit/v1/store-unfinished-list.schema.json", []byte(out)); err != nil {
		t.Fatalf("not a StoreUnfinishedList: %v\n%s", err, out)
	}
	for _, says := range []string{`"left": true`, `"folder": true`, `"user": "alice"`} {
		if !strings.Contains(out, says) {
			t.Errorf("the answer has no %s:\n%s", says, out)
		}
	}
	if y, _, err := unfinishedRun(t, dir, nil, "list", "-o", "yaml"); err != nil || !strings.Contains(y, "kind: StoreUnfinishedList") {
		t.Fatalf("-o yaml: %v\n%s", err, y)
	}
}

// 3. Errors and exit codes: a key that is not a thing, an unknown -o, a scope that cannot be opened.
func TestUnfinishedListRefusesWhatItCannotList(t *testing.T) {
	dir := fresh(t)
	served(t, dir)
	for _, words := range [][]string{{"list", "shop/a.yml"}, {"list", "../x"}, {"list", "-o", "xml"}} {
		if _, code, err := unfinishedRun(t, dir, nil, words...); code != 1 || err == nil {
			t.Errorf("%v = %d, %v, want 1 and an error", words, code, err)
		}
	}
	if _, code, _ := unfinishedRun(t, dir, nil, "list", "a", "b"); code != 2 {
		t.Errorf("two things = %d, want 2, a usage mistake", code)
	}
	if _, code, err := unfinishedRun(t, filepath.Join(dir, "none"), nil, "list"); code != 1 || err == nil {
		t.Errorf("a scope that is not there = %d, %v", code, err)
	}
}

// 4. Asking first: list changes nothing, so it never asks, terminal or not.
func TestUnfinishedListNeverAsks(t *testing.T) {
	dir := fresh(t)
	served(t, dir)
	never := func(*cobra.Command, ask.Action) error { return errors.New("list asked") }
	if _, code, err := unfinishedRun(t, dir, never, "list"); code != 0 || err != nil {
		t.Fatalf("list = %d, %v", code, err)
	}
}

// 5. CLI and HTTP parity: the marks and journals are this machine's, so the command has no HTTP door; an app's server shows nothing of them.

// 6. Help.
func TestUnfinishedListHelpSaysWhatItShows(t *testing.T) {
	out, code, err := unfinishedRun(t, fresh(t), nil, "list", "--help")
	if err != nil || code != 0 || !strings.Contains(out, "list [<thing>]") || !strings.Contains(out, "left") {
		t.Fatalf("help = %d, %v\n%s", code, err, out)
	}
}
