package store_test

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ElderlyGate/contracts"
	"github.com/spf13/cobra"

	"github.com/tugbayatilla/mankit/v4/ask"
	"github.com/tugbayatilla/mankit/v4/problem"
	"github.com/tugbayatilla/mankit/v4/store"
	storecli "github.com/tugbayatilla/mankit/v4/store/cli"
	"github.com/tugbayatilla/mankit/v4/store/internal/asking"
)

// The tests of <app> store unfinished remove, in ADR-S19's six parts.

// yes stands in a typed name for the question, keeping what was asked.
func yes(t *testing.T) *ask.Action {
	asked := new(ask.Action)
	asking.With(t, func(_ *cobra.Command, a ask.Action) error {
		*asked = a
		return nil
	})
	return asked
}

// 1. Success: another user's mark is removed, so the key is changed again.
func TestUnfinishedClearRemovesTheMarkSoTheKeyIsChangedAgain(t *testing.T) {
	dir := fresh(t)
	s := served(t, dir)
	markOf(t, lines(filepath.Join(dir, "served"))("shop"), "alice", "elsewhere", false, "shop/a.yml")
	if err := put(t, s, "shop/a.yml", "mine"); reason(err) != store.ReasonUnfinished {
		t.Fatalf("before clear = %v, want unfinished_change", err)
	}
	out, code, err := unfinishedRun(t, dir, "remove", "shop", "--yes")
	if err != nil || code != 0 || !strings.Contains(out, "alice") || !strings.Contains(out, "nothing was put back") {
		t.Fatalf("clear = %d, %v\n%s", code, err, out)
	}
	if err := put(t, s, "shop/a.yml", "mine"); err != nil {
		t.Fatalf("once cleared: %v", err)
	}
}

// Only the marks the question showed are cleared: one that came while it was asked stays.
func TestUnfinishedClearRemovesOnlyTheMarksItShowed(t *testing.T) {
	dir := fresh(t)
	served(t, dir)
	line := lines(filepath.Join(dir, "served"))("shop")
	markOf(t, line, "alice", "elsewhere", false, "shop/a.yml")
	later := filepath.Join(line, ".queue", ".unfinished-17000000000000000002.json")
	asking.With(t, func(*cobra.Command, ask.Action) error {
		raw, err := os.ReadFile(filepath.Join(line, ".queue", ".unfinished-17000000000000000001.json"))
		if err != nil {
			t.Fatal(err)
		}
		write(t, later, strings.ReplaceAll(string(raw), "17000000000000000001", "17000000000000000002"))
		return nil
	})
	out, code, err := unfinishedRun(t, dir, "remove", "shop", "-o", "json")
	if err != nil || code != 0 || strings.Contains(out, "17000000000000000002") {
		t.Fatalf("clear = %d, %v\n%s", code, err, out)
	}
	if _, err := os.Stat(later); err != nil {
		t.Fatalf("a mark the question did not show was cleared: %v", err)
	}
}

// 2. Contract: -o json is a StoreUnfinishedCleared naming this user's journal, which stays and is then listed left.
func TestUnfinishedClearAnswersInItsContractShape(t *testing.T) {
	dir := fresh(t)
	s := served(t, dir)
	journal, _ := cutShort(t, s, 5)
	out, _, err := unfinishedRun(t, dir, "remove", "shop", "--yes", "-o", "json")
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
	out, code, err := unfinishedRun(t, dir, "remove", "shop", "--yes")
	var p *problem.Problem
	if code != 1 || !errors.As(err, &p) || p.Type != problem.TypeNotFound || !strings.Contains(p.Detail, "storetest store unfinished list") {
		t.Fatalf("clear with no mark = %d, %v\n%s", code, err, out)
	}
	cutShort(t, s, 5)
	if _, err := s.ClearShown(ctx, "shop", idsOf(t, s, "shop")); err != nil {
		t.Fatal(err)
	}
	if _, code, err := unfinishedRun(t, dir, "remove", "shop", "--yes"); code != 1 || !errors.As(err, &p) || p.Type != problem.TypeNotFound {
		t.Fatalf("clear with only a left journal = %d, %v", code, err)
	}
	if _, code, err := unfinishedRun(t, dir, "remove", "shop/a.yml", "--yes"); code != 1 || !errors.As(err, &p) || p.Type != problem.TypeRefused {
		t.Fatalf("clear of a key = %d, %v", code, err)
	}
	if _, code, _ := unfinishedRun(t, dir, "remove", "--yes"); code != 2 {
		t.Fatalf("clear with no thing = %d, want 2, a usage mistake", code)
	}
}

// A mark shown and gone before its turns are taken, put back or cleared meanwhile, clears nothing: not_found.
func TestUnfinishedClearOfAMarkGoneMeanwhileIsNotFound(t *testing.T) {
	dir := fresh(t)
	served(t, dir)
	line := lines(filepath.Join(dir, "served"))("shop")
	markOf(t, line, "alice", "elsewhere", false, "shop/a.yml")
	asking.With(t, func(*cobra.Command, ask.Action) error {
		return os.Remove(filepath.Join(line, ".queue", ".unfinished-17000000000000000001.json"))
	})
	_, code, err := unfinishedRun(t, dir, "remove", "shop")
	var p *problem.Problem
	if code != 1 || !errors.As(err, &p) || p.Type != problem.TypeNotFound || !strings.Contains(p.Detail, "nothing was cleared") {
		t.Fatalf("clear of a mark gone meanwhile = %d, %v", code, err)
	}
}

// The refusal and the command name the app's CLI by one name: App.Command, else its ID.
func TestTheRefusalAndTheCommandNameTheAppsCLIAlike(t *testing.T) {
	dir := fresh(t)
	served(t, dir)
	root := filepath.Join(dir, "served")
	named := app
	named.Command = "e-storetest"
	s, err := store.Served(named, root, lines(root))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	markOf(t, lines(root)("shop"), "alice", "elsewhere", false, "shop/a.yml")
	if err := put(t, s, "shop/a.yml", "x"); !strings.Contains(fmt.Sprint(err), "`e-storetest store unfinished remove shop`") {
		t.Fatalf("the refusal = %v", err)
	}
	cli := storecli.UnfinishedCommand(serving, func() (*store.Scope, error) { return store.Served(named, root, lines(root)) })
	cli.SetArgs([]string{"remove", "cart", "--yes"})
	cli.SilenceErrors, cli.SilenceUsage = true, true
	if err := cli.Execute(); !strings.Contains(fmt.Sprint(err), "`e-storetest store unfinished list`") {
		t.Fatalf("clear's not_found = %v", err)
	}
}

// 4. Asking first: it shows the change and always asks for the thing's name, a change to the system itself; without a terminal and without --yes it is refused, exit 3, and the mark stays.
func TestUnfinishedClearShowsTheChangeAndAsksForTheName(t *testing.T) {
	dir := fresh(t)
	s := served(t, dir)
	journal, _ := cutShort(t, s, 5)
	markOf(t, lines(filepath.Join(dir, "served"))("shop"), "alice", "elsewhere", false, "shop/z.yml")
	_, code, err := unfinishedRun(t, dir, "remove", "shop")
	if code != 3 || !errors.Is(err, ask.ErrRefused) || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("no terminal and no --yes = %d, %v, want 3 and refused", code, err)
	}
	if got, _ := s.Unfinished("shop"); len(got) != 2 || got[0].Left || got[1].Left {
		t.Fatalf("a refused clear removed a mark: %+v", got)
	}
	asked := yes(t)
	if _, code, err := unfinishedRun(t, dir, "remove", "shop"); code != 0 || err != nil {
		t.Fatalf("a yes = %d, %v", code, err)
	}
	if asked.Type != ask.Delete || !asked.System || asked.Name != "shop" {
		t.Fatalf("asked %+v, want a delete of the system itself naming shop", asked)
	}
	for _, says := range []string{"alice", "elsewhere", "shop/z.yml", journal, "was to add shop/a.yml", "nothing is put back"} {
		if !strings.Contains(asked.What, says) {
			t.Errorf("the question does not show %q:\n%s", says, asked.What)
		}
	}
}

// A journal that cannot be read is said in the question, never taken for one doing nothing.
func TestUnfinishedClearSaysAJournalItCannotRead(t *testing.T) {
	dir := fresh(t)
	s := served(t, dir)
	journal, _ := cutShort(t, s, 5)
	if err := os.WriteFile(journal, []byte("{broken\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	asked := yes(t)
	if _, _, err := unfinishedRun(t, dir, "remove", "shop"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(asked.What, "what it was doing is not known") {
		t.Fatalf("the question does not say the journal cannot be read:\n%s", asked.What)
	}
}

// 5. CLI and HTTP parity: DELETE asks through serve's change door (428 without confirm=yes, remember silences nothing), clears only the marks it names, and answers as clear -o json.
func TestUnfinishedClearOverHTTPAsksAndAnswersAsTheCLI(t *testing.T) {
	dir := fresh(t)
	s := served(t, dir)
	journal, _ := cutShort(t, s, 5)
	markOf(t, lines(filepath.Join(dir, "served"))("shop"), "alice", "elsewhere", false, "shop/z.yml")
	h := unfinishedServer(t, dir)
	mine := ""
	for _, id := range idsOf(t, s, "shop") {
		if id != "17000000000000000001" {
			mine = id
		}
	}
	target := storecli.UnfinishedPath + "/shop?id=" + mine
	if w := call(t, h, http.MethodDelete, target); w.Code != http.StatusPreconditionRequired {
		t.Fatalf("DELETE without a yes = %d %s, want 428", w.Code, w.Body)
	}
	if w := call(t, h, http.MethodDelete, storecli.UnfinishedPath+"/shop"); w.Code != http.StatusBadRequest {
		t.Fatalf("DELETE naming no mark, unasked = %d %s, want 400 before 428", w.Code, w.Body)
	}
	if w := call(t, h, http.MethodDelete, storecli.UnfinishedPath+"/shop?confirm=yes"); w.Code != http.StatusBadRequest {
		t.Fatalf("DELETE naming no mark = %d %s, want 400", w.Code, w.Body)
	}
	if got := idsOf(t, s, "shop"); len(got) != 2 {
		t.Fatalf("a refused DELETE cleared a mark: %v", got)
	}
	w := call(t, h, http.MethodDelete, target+"&confirm=remember")
	if w.Code != http.StatusOK {
		t.Fatalf("DELETE = %d %s", w.Code, w.Body)
	}
	if err := contracts.Validate("mankit/v1/store-unfinished-cleared.schema.json", w.Body.Bytes()); err != nil || !strings.Contains(w.Body.String(), journal) {
		t.Fatalf("not a StoreUnfinishedCleared naming the journal: %v\n%s", err, w.Body)
	}
	if got := idsOf(t, s, "shop"); len(got) != 1 || got[0] != "17000000000000000001" {
		t.Fatalf("DELETE cleared more than it named: %v", got)
	}
	again := call(t, h, http.MethodDelete, target+"&confirm=yes")
	if again.Code != http.StatusNotFound {
		t.Fatalf("DELETE of a mark gone = %d %s, want 404", again.Code, again.Body)
	}
	// The same clear on the CLI answers the same data.
	cli, _, err := unfinishedRun(t, dir, "remove", "shop", "--yes", "-o", "json")
	if err != nil {
		t.Fatal(err)
	}
	markOf(t, lines(filepath.Join(dir, "served"))("shop"), "alice", "elsewhere", false, "shop/z.yml")
	over := call(t, h, http.MethodDelete, storecli.UnfinishedPath+"/shop?id=17000000000000000001&confirm=yes")
	if dataOf(t, over.Body.Bytes()) != dataOf(t, []byte(cli)) {
		t.Fatalf("DELETE and clear differ:\n%s\n%s", over.Body, cli)
	}
}

// 6. Help.
func TestUnfinishedRemoveHelpSaysItPutsNothingBack(t *testing.T) {
	out, code, err := unfinishedRun(t, fresh(t), "remove", "--help")
	if err != nil || code != 0 || !strings.Contains(out, "remove <thing>") || !strings.Contains(out, "puts nothing back") || !strings.Contains(out, "name") || !strings.Contains(out, "--yes") {
		t.Fatalf("help = %d, %v\n%s", code, err, out)
	}
}
