package store_test

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ElderlyGate/contracts"
	"github.com/spf13/cobra"

	"github.com/tugbayatilla/mankit/v3/ask"
	"github.com/tugbayatilla/mankit/v3/serve"
	"github.com/tugbayatilla/mankit/v3/store"
)

// The tests of <app> store unfinished clear, in ADR-S19's six parts.

// 1. Success: another user's mark is removed, so the key is changed again.
func TestUnfinishedClearRemovesTheMarkSoTheKeyIsChangedAgain(t *testing.T) {
	dir := fresh(t)
	s := served(t, dir)
	markOf(t, lines(filepath.Join(dir, "served"))("shop"), "alice", "elsewhere", false, "shop/a.yml")
	if err := put(t, s, "shop/a.yml", "mine"); reason(err) != store.ReasonUnfinished {
		t.Fatalf("before clear = %v, want unfinished_change", err)
	}
	out, code, err := unfinishedRun(t, dir, nil, "clear", "shop", "--yes")
	if err != nil || code != 0 || !strings.Contains(out, "alice") || !strings.Contains(out, "nothing was put back") {
		t.Fatalf("clear = %d, %v\n%s", code, err, out)
	}
	if err := put(t, s, "shop/a.yml", "mine"); err != nil {
		t.Fatalf("once cleared: %v", err)
	}
}

// 2. Contract: -o json is a StoreUnfinishedCleared naming this user's journal, which stays and is then listed left.
func TestUnfinishedClearAnswersInItsContractShape(t *testing.T) {
	dir := fresh(t)
	s := served(t, dir)
	journal, _ := cutShort(t, s, 5)
	out, _, err := unfinishedRun(t, dir, nil, "clear", "shop", "--yes", "-o", "json")
	if err != nil {
		t.Fatal(err)
	}
	if err := contracts.Validate("mankit/v1/store-unfinished-cleared.schema.json", []byte(out)); err != nil {
		t.Fatalf("not a StoreUnfinishedCleared: %v\n%s", err, out)
	}
	if !strings.Contains(out, `"thing": "shop"`) || !strings.Contains(out, journal) {
		t.Fatalf("the answer does not name the thing and the journal:\n%s", out)
	}
	left, err := s.Unfinished("shop")
	if err != nil || len(left) != 1 || !left[0].Left || left[0].File != journal {
		t.Fatalf("after clear = %+v, %v, want the journal left", left, err)
	}
}

// 3. Errors and exit codes: clear needs a mark that holds the thing; a left journal alone is no mark; a key is not a thing.
func TestUnfinishedClearNeedsTheMarkToExist(t *testing.T) {
	dir := fresh(t)
	s := served(t, dir)
	out, code, err := unfinishedRun(t, dir, nil, "clear", "shop", "--yes")
	var p *serve.Problem
	if code != 1 || !errors.As(err, &p) || p.Type != serve.TypeNotFound || !strings.Contains(p.Detail, "storetest store unfinished list") {
		t.Fatalf("clear with no mark = %d, %v\n%s", code, err, out)
	}
	cutShort(t, s, 5)
	if _, err := s.Clear(ctx, "shop"); err != nil {
		t.Fatal(err)
	}
	if _, code, err := unfinishedRun(t, dir, nil, "clear", "shop", "--yes"); code != 1 || !errors.As(err, &p) || p.Type != serve.TypeNotFound {
		t.Fatalf("clear with only a left journal = %d, %v", code, err)
	}
	if _, code, err := unfinishedRun(t, dir, nil, "clear", "shop/a.yml", "--yes"); code != 1 || !errors.As(err, &p) || p.Type != serve.TypeRefused {
		t.Fatalf("clear of a key = %d, %v", code, err)
	}
	if _, code, _ := unfinishedRun(t, dir, nil, "clear", "--yes"); code != 2 {
		t.Fatalf("clear with no thing = %d, want 2, a usage mistake", code)
	}
}

// 4. Asking first: it shows the change and asks as a delete; without a terminal and without --yes it is refused, exit 3, and the mark stays.
func TestUnfinishedClearShowsTheChangeAndAsksFirst(t *testing.T) {
	dir := fresh(t)
	s := served(t, dir)
	journal, _ := cutShort(t, s, 5)
	markOf(t, lines(filepath.Join(dir, "served"))("shop"), "alice", "elsewhere", false, "shop/z.yml")
	var asked ask.Action
	no := func(_ *cobra.Command, a ask.Action) error {
		asked = a
		return ask.ErrRefused
	}
	if _, code, _ := unfinishedRun(t, dir, no, "clear", "shop"); code != 3 {
		t.Fatalf("a no = %d, want 3", code)
	}
	if asked.Type != ask.Delete || asked.Name != "shop" {
		t.Fatalf("asked %+v, want a delete naming shop", asked)
	}
	for _, says := range []string{"alice", "elsewhere", "shop/z.yml", journal, "was to add shop/a.yml", "nothing is put back"} {
		if !strings.Contains(asked.What, says) {
			t.Errorf("the question does not show %q:\n%s", says, asked.What)
		}
	}
	_, code, err := unfinishedRun(t, dir, nil, "clear", "shop")
	if code != 3 || !errors.Is(err, ask.ErrRefused) || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("no terminal and no --yes = %d, %v, want 3 and refused", code, err)
	}
	if got, _ := s.Unfinished("shop"); len(got) != 2 || got[0].Left || got[1].Left {
		t.Fatalf("a refused clear removed a mark: %+v", got)
	}
}

// 5. CLI and HTTP parity: the marks and journals are this machine's, so the command has no HTTP door; an app's server clears nothing.

// 6. Help.
func TestUnfinishedClearHelpSaysItPutsNothingBack(t *testing.T) {
	out, code, err := unfinishedRun(t, fresh(t), nil, "clear", "--help")
	if err != nil || code != 0 || !strings.Contains(out, "clear <thing>") || !strings.Contains(out, "puts nothing back") || !strings.Contains(out, "--yes") {
		t.Fatalf("help = %d, %v\n%s", code, err, out)
	}
}
