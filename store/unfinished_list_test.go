package store_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ElderlyGate/contracts"
	"github.com/spf13/cobra"

	"github.com/tugbayatilla/mankit/v4/ask"
	"github.com/tugbayatilla/mankit/v4/helpout"
	"github.com/tugbayatilla/mankit/v4/home"
	"github.com/tugbayatilla/mankit/v4/serve"
	"github.com/tugbayatilla/mankit/v4/store"
	storecli "github.com/tugbayatilla/mankit/v4/store/cli"
)

// The tests of <app> store unfinished list, in ADR-S19's six parts; the helpers are shared with the clear tests.

// serving is the app as its server and its CLI name it.
var serving = serve.App{ID: app.ID, Version: app.Version, Contracts: []serve.Contract{{Version: "v1"}}}

func opener(dir string) func() (*store.Scope, error) {
	root := filepath.Join(dir, "served")
	return func() (*store.Scope, error) { return store.Served(app, root, lines(root)) }
}

// unfinishedRun runs `store unfinished` words under a store command of its own, as an app mounts it, on the served scope in dir, with no terminal.
func unfinishedRun(t *testing.T, dir string, words ...string) (out string, code int, err error) {
	t.Helper()
	cli := &cobra.Command{Use: app.ID}
	group := &cobra.Command{Use: "store"}
	group.AddCommand(storecli.UnfinishedCommand(serving, opener(dir)))
	cli.AddCommand(group)
	var buf bytes.Buffer
	cli.SetOut(&buf)
	cli.SetErr(&buf)
	cli.SetIn(strings.NewReader(""))
	cli.SetArgs(append([]string{"store", "unfinished"}, words...))
	code, err = helpout.Execute(context.Background(), cli)
	return buf.String(), code, err
}

const token = "s3cret"

// unfinishedServer is the app's server with UnfinishedRoutes on the served scope in dir, behind serve's guard with a token of the test's own.
func unfinishedServer(t *testing.T, dir string) http.Handler {
	t.Helper()
	tokens := filepath.Join(os.Getenv("XDG_DATA_HOME"), home.Org, app.ID, "servers")
	if err := os.MkdirAll(tokens, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tokens, "token"), []byte(token+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	h, err := serving.Handler(serve.Site{Routes: storecli.UnfinishedRoutes(opener(dir))}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

// call is one request to h with the token, as a page on this machine sends it.
func call(t *testing.T, h http.Handler, method, target string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, "http://127.0.0.1:7480"+target, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

// dataOf is the data of an answer, to compare the CLI's with the server's.
func dataOf(t *testing.T, answer []byte) string {
	t.Helper()
	var e struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(answer, &e); err != nil {
		t.Fatalf("%v\n%s", err, answer)
	}
	var v any
	_ = json.Unmarshal(e.Data, &v)
	b, _ := json.Marshal(v)
	return string(b)
}

// 1. Success: every user's marks, this user's own change with its journal file, and its journal once left.
func TestUnfinishedListShowsEveryUsersMarksAndThisUsersJournal(t *testing.T) {
	dir := fresh(t)
	s := served(t, dir)
	if err := put(t, s, "cart/b.yml", "b"); err != nil {
		t.Fatal(err)
	}
	journal, _ := cutShort(t, s, 5)
	markOf(t, lines(filepath.Join(dir, "served"))("cart"), "alice", "elsewhere", false, "cart/a.yml")
	out, code, err := unfinishedRun(t, dir, "list")
	if err != nil || code != 0 {
		t.Fatalf("list = %d, %v\n%s", code, err, out)
	}
	for _, says := range []string{"MARK", "alice", "elsewhere", "cart/a.yml", journal, "shop/a.yml", "holds"} {
		if !strings.Contains(out, says) {
			t.Errorf("list does not show %q:\n%s", says, out)
		}
	}
	one, _, err := unfinishedRun(t, dir, "list", "cart")
	if err != nil || !strings.Contains(one, "alice") || strings.Contains(one, journal) {
		t.Fatalf("list cart shows other things or not cart's: %v\n%s", err, one)
	}
	if _, err := s.ClearShown(ctx, "shop", idsOf(t, s, "shop")); err != nil {
		t.Fatal(err)
	}
	left, _, err := unfinishedRun(t, dir, "list", "shop")
	if err != nil || !strings.Contains(left, "left") || !strings.Contains(left, journal) {
		t.Fatalf("a journal whose marks are gone is not shown left: %v\n%s", err, left)
	}
	empty := t.TempDir()
	served(t, empty)
	none, _, err := unfinishedRun(t, empty, "list")
	if err != nil || !strings.Contains(none, "no change is cut short") {
		t.Fatalf("an empty scope: %v\n%s", err, none)
	}
}

// A mark only in the turn of a thing that is gone is found by naming the thing, as the refusal does.
func TestUnfinishedListOfAThingGoneFindsItsMark(t *testing.T) {
	dir := fresh(t)
	served(t, dir)
	markOf(t, lines(filepath.Join(dir, "served"))("gone"), "alice", "elsewhere", false, "gone/a.yml")
	out, _, err := unfinishedRun(t, dir, "list", "gone", "-o", "json")
	if err != nil || strings.Count(out, `"id"`) != 1 || !strings.Contains(out, "gone/a.yml") {
		t.Fatalf("list gone does not find its mark: %v\n%s", err, out)
	}
}

func idsOf(t *testing.T, s *store.Scope, thing store.Key) []string {
	t.Helper()
	got, err := s.Unfinished(thing)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, u := range got {
		if !u.Left {
			out = append(out, u.ID)
		}
	}
	return out
}

// 2. Contract: -o json is a StoreUnfinishedList, with a left journal and with none.
func TestUnfinishedListAnswersInItsContractShape(t *testing.T) {
	dir := fresh(t)
	s := served(t, dir)
	empty, _, err := unfinishedRun(t, dir, "list", "-o", "json")
	if err != nil {
		t.Fatal(err)
	}
	if err := contracts.Validate("mankit/v1/store-unfinished-list.schema.json", []byte(empty)); err != nil || !strings.Contains(empty, `"changes": []`) {
		t.Fatalf("an empty list: %v\n%s", err, empty)
	}
	cutShort(t, s, 5)
	markOf(t, lines(filepath.Join(dir, "served"))(""), "alice", "elsewhere", true, "cart")
	if _, err := s.ClearShown(ctx, "shop", idsOf(t, s, "shop")); err != nil {
		t.Fatal(err)
	}
	out, _, err := unfinishedRun(t, dir, "list", "-o", "json")
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
	if y, _, err := unfinishedRun(t, dir, "list", "-o", "yaml"); err != nil || !strings.Contains(y, "kind: StoreUnfinishedList") {
		t.Fatalf("-o yaml: %v\n%s", err, y)
	}
}

// 3. Errors and exit codes: a key that is not a thing, an unknown -o, a scope that cannot be opened.
func TestUnfinishedListRefusesWhatItCannotList(t *testing.T) {
	dir := fresh(t)
	served(t, dir)
	for _, words := range [][]string{{"list", "shop/a.yml"}, {"list", "../x"}, {"list", "-o", "xml"}} {
		if _, code, err := unfinishedRun(t, dir, words...); code != 1 || err == nil {
			t.Errorf("%v = %d, %v, want 1 and an error", words, code, err)
		}
	}
	if _, code, _ := unfinishedRun(t, dir, "list", "a", "b"); code != 2 {
		t.Errorf("two things = %d, want 2, a usage mistake", code)
	}
	if _, code, err := unfinishedRun(t, filepath.Join(dir, "none"), "list"); code != 1 || err == nil {
		t.Errorf("a scope that is not there = %d, %v", code, err)
	}
}

// 4. Asking first: list changes nothing, so it never asks, terminal or not.
func TestUnfinishedListNeverAsks(t *testing.T) {
	dir := fresh(t)
	served(t, dir)
	store.AskWith(t, func(*cobra.Command, ask.Action) error { return errors.New("list asked") })
	if _, code, err := unfinishedRun(t, dir, "list"); code != 0 || err != nil {
		t.Fatalf("list = %d, %v", code, err)
	}
}

// 5. CLI and HTTP parity: GET answers the same data as list -o json, a thing too, and refuses what list refuses.
func TestUnfinishedListOverHTTPAnswersAsTheCLI(t *testing.T) {
	dir := fresh(t)
	s := served(t, dir)
	cutShort(t, s, 5)
	markOf(t, lines(filepath.Join(dir, "served"))("shop"), "alice", "elsewhere", false, "shop/z.yml")
	h := unfinishedServer(t, dir)
	for _, q := range []string{"", "?thing=shop"} {
		w := call(t, h, http.MethodGet, storecli.UnfinishedPath+q)
		if w.Code != http.StatusOK {
			t.Fatalf("GET%s = %d %s", q, w.Code, w.Body)
		}
		if err := contracts.Validate("mankit/v1/store-unfinished-list.schema.json", w.Body.Bytes()); err != nil {
			t.Fatalf("GET%s is not a StoreUnfinishedList: %v\n%s", q, err, w.Body)
		}
		words := []string{"list", "-o", "json"}
		if q != "" {
			words = append(words, "shop")
		}
		cli, _, err := unfinishedRun(t, dir, words...)
		if err != nil || dataOf(t, w.Body.Bytes()) != dataOf(t, []byte(cli)) {
			t.Fatalf("GET%s and list differ: %v\n%s\n%s", q, err, w.Body, cli)
		}
	}
	if w := call(t, h, http.MethodGet, storecli.UnfinishedPath+"?thing=shop/a.yml"); w.Code != http.StatusBadRequest {
		t.Fatalf("GET of a key = %d %s, want 400", w.Code, w.Body)
	}
}

// 6. Help.
func TestUnfinishedListHelpSaysWhatItShows(t *testing.T) {
	out, code, err := unfinishedRun(t, fresh(t), "list", "--help")
	if err != nil || code != 0 || !strings.Contains(out, "list [<thing>]") || !strings.Contains(out, "left") || !strings.Contains(out, "list <thing>") {
		t.Fatalf("help = %d, %v\n%s", code, err, out)
	}
}
