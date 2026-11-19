// Package storetest is the one conformance suite every adapter of mankit's store passes (ADR-S22): what a scope reads and writes, all or nothing, turns across processes, and a change stopped dead after any of its writes put back by the next one.
package storetest

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/tugbayatilla/mankit/v4/problem"
	"github.com/tugbayatilla/mankit/v4/store"
)

// Harness is how an adapter is handed to the suite.
type Harness struct {
	// New makes a new empty scope for one test and gives back how to open it; each call of open is another view of the same scope, as another process would have.
	New func(t *testing.T) (open func() *store.Scope)
	// Crash makes the adapter stop dead after its n-th write in the next change, leaving what a process killed there leaves; fired, called after that change, says whether it stopped and ends the crash either way.
	Crash func(n int) (fired func() bool)
}

// Run runs the whole suite against h.
func Run(t *testing.T, h Harness) {
	tests := []struct {
		name string
		run  func(*testing.T, Harness)
	}{
		{"a thing put is read back with its sum", putThenGet},
		{"a thing not there is not found", missing},
		{"a write is made only if the thing holds what the writer read", wants},
		{"a write naming no Want is refused", noWant},
		{"a file and a folder are removed", removes},
		{"a file and a folder are moved, never onto a taken name", moves},
		{"writes and moves in one change end where the last one leaves them", writesThenMoves},
		{"a change whose fn fails writes nothing", fnFails},
		{"a change refused half way writes nothing", refusedHalfWay},
		{"a key that leaves the scope is refused", outsideKeys},
		{"a change writes only inside its thing", insideThing},
		{"changes from many processes at once are all kept", race},
		{"the revision moves with a change and not with a read", revision},
		{"list names every thing inside a prefix", list},
		{"a list inside a change sees its own writes", listIn},
		{"a change stopped dead after any write is put back by the next", crashes},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) { tc.run(t, h) })
	}
}

var ctx = context.Background()

func key(t *testing.T, s string) store.Key {
	t.Helper()
	k, err := store.ParseKey(s)
	if err != nil {
		t.Fatalf("ParseKey(%q): %v", s, err)
	}
	return k
}

// put writes body at k in k's thing's turn, taking whatever is there.
func put(t *testing.T, s *store.Scope, k string, body string) {
	t.Helper()
	kk := key(t, k)
	if err := s.Change(ctx, "", "put "+k, func(tx store.Tx) error { return tx.Put(kk, []byte(body), store.Anything()) }); err != nil {
		t.Fatalf("put %s: %v", k, err)
	}
}

// state is every thing in the scope with its body.
func state(t *testing.T, s *store.Scope) map[string]string {
	t.Helper()
	keys, err := s.List("")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	out := map[string]string{}
	for _, k := range keys {
		item, there, err := s.Get(k)
		if err != nil || !there {
			t.Fatalf("Get(%s) = %v, %v", k, there, err)
		}
		out[string(k)] = string(item.Body)
	}
	return out
}

// reason is the store's reason in err, or empty when err is no Problem.
func reason(err error) string {
	var p *problem.Problem
	if errors.As(err, &p) {
		return p.Reason
	}
	return ""
}

func wantReason(t *testing.T, err error, want string) {
	t.Helper()
	if got := reason(err); got != want {
		t.Fatalf("err = %v (reason %q), want reason %q", err, got, want)
	}
}

func putThenGet(t *testing.T, h Harness) {
	s := h.New(t)()
	put(t, s, "shop/blocks/a.yml", "a: 1\n")
	put(t, s, "shop/empty.yml", "")
	item, there, err := s.Get(key(t, "shop/blocks/a.yml"))
	if err != nil || !there || string(item.Body) != "a: 1\n" || item.Sum != store.SumOf([]byte("a: 1\n")) {
		t.Fatalf("Get = %q %s %v %v", item.Body, item.Sum, there, err)
	}
	if item, there, err := s.Get(key(t, "shop/empty.yml")); err != nil || !there || len(item.Body) != 0 {
		t.Fatalf("an empty file reads as %q %v %v, want there and empty", item.Body, there, err)
	}
	if got := store.SumOf([]byte("x")); got[:7] != "sha256:" || len(got) != 7+64 {
		t.Fatalf("SumOf = %q, want sha256: and 64 hex", got)
	}
}

func missing(t *testing.T, h Harness) {
	s := h.New(t)()
	if _, there, err := s.Get(key(t, "shop/none.yml")); there || err != nil {
		t.Fatalf("Get of nothing = %v, %v", there, err)
	}
}

func wants(t *testing.T, h Harness) {
	s := h.New(t)()
	k := key(t, "shop/a.yml")
	change := func(body string, w store.Want) error {
		return s.Change(ctx, "shop", "write", func(tx store.Tx) error { return tx.Put(k, []byte(body), w) })
	}
	if err := change("one", store.Absent()); err != nil {
		t.Fatalf("Absent on nothing: %v", err)
	}
	wantReason(t, change("two", store.Absent()), store.ReasonChanged)
	old := store.SumOf([]byte("one"))
	if err := change("two", store.Holds(old)); err != nil {
		t.Fatalf("Holds the sum read: %v", err)
	}
	wantReason(t, change("three", store.Holds(old)), store.ReasonChanged)
	if err := change("three", store.Anything()); err != nil {
		t.Fatalf("Anything: %v", err)
	}
	if got := state(t, s)["shop/a.yml"]; got != "three" {
		t.Fatalf("holds %q, want three", got)
	}
}

func noWant(t *testing.T, h Harness) {
	s := h.New(t)()
	err := s.Change(ctx, "shop", "write", func(tx store.Tx) error { return tx.Put(key(t, "shop/a.yml"), []byte("x"), store.Want{}) })
	if err == nil {
		t.Fatal("a write with no Want went through")
	}
}

func removes(t *testing.T, h Harness) {
	s := h.New(t)()
	put(t, s, "shop/a.yml", "a")
	put(t, s, "shop/blocks/b.yml", "b")
	put(t, s, "shop/blocks/deep/c.yml", "c")
	put(t, s, "keep.yml", "k")
	wantReason(t, s.Change(ctx, "shop", "remove", func(tx store.Tx) error {
		return tx.Remove(key(t, "shop/a.yml"), store.Holds(store.SumOf([]byte("other"))))
	}), store.ReasonChanged)
	err := s.Change(ctx, "shop", "remove", func(tx store.Tx) error {
		if err := tx.Remove(key(t, "shop/a.yml"), store.Holds(store.SumOf([]byte("a")))); err != nil {
			return err
		}
		return tx.Remove(key(t, "shop/blocks"), store.Anything())
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := state(t, s); !maps.Equal(got, map[string]string{"keep.yml": "k"}) {
		t.Fatalf("left %v", got)
	}
}

func moves(t *testing.T, h Harness) {
	s := h.New(t)()
	put(t, s, "shop/a.yml", "a")
	put(t, s, "shop/blocks/b.yml", "b")
	put(t, s, "shop/taken.yml", "t")
	err := s.Change(ctx, "shop", "move", func(tx store.Tx) error {
		if err := tx.Move(key(t, "shop/a.yml"), key(t, "shop/moved/a.yml"), store.Holds(store.SumOf([]byte("a")))); err != nil {
			return err
		}
		return tx.Move(key(t, "shop/blocks"), key(t, "shop/parts"), store.Anything())
	})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"shop/moved/a.yml": "a", "shop/parts/b.yml": "b", "shop/taken.yml": "t"}
	if got := state(t, s); !maps.Equal(got, want) {
		t.Fatalf("after the moves %v, want %v", got, want)
	}
	wantReason(t, s.Change(ctx, "shop", "move", func(tx store.Tx) error {
		return tx.Move(key(t, "shop/moved/a.yml"), key(t, "shop/taken.yml"), store.Anything())
	}), store.ReasonChanged)
	if got := state(t, s); !maps.Equal(got, want) {
		t.Fatalf("a refused move changed %v", got)
	}
}

func writesThenMoves(t *testing.T, h Harness) {
	steps := func(ops ...func(store.Tx) error) func(store.Tx) error {
		return func(tx store.Tx) error {
			for _, op := range ops {
				if err := op(tx); err != nil {
					return err
				}
			}
			return nil
		}
	}
	put := func(k, body string) func(store.Tx) error {
		return func(tx store.Tx) error { return tx.Put(key(t, k), []byte(body), store.Anything()) }
	}
	move := func(from, to string) func(store.Tx) error {
		return func(tx store.Tx) error { return tx.Move(key(t, from), key(t, to), store.Anything()) }
	}
	remove := func(k string) func(store.Tx) error {
		return func(tx store.Tx) error { return tx.Remove(key(t, k), store.Anything()) }
	}
	cases := []struct {
		name   string
		before map[string]string
		change func(store.Tx) error
		want   map[string]string
	}{
		{"a file put, then its folder moved", map[string]string{"shop/a/old.yml": "o"},
			steps(put("shop/a/x.yml", "x"), move("shop/a", "shop/b")),
			map[string]string{"shop/b/old.yml": "o", "shop/b/x.yml": "x"}},
		{"a file put, then moved", map[string]string{"shop/keep.yml": "k"},
			steps(put("shop/a/x.yml", "x"), move("shop/a/x.yml", "shop/b/x.yml")),
			map[string]string{"shop/keep.yml": "k", "shop/b/x.yml": "x"}},
		{"a folder moved, then a file put into it", map[string]string{"shop/a/old.yml": "o"},
			steps(move("shop/a", "shop/b"), put("shop/b/x.yml", "x")),
			map[string]string{"shop/b/old.yml": "o", "shop/b/x.yml": "x"}},
		{"a file put, its folder moved and moved back", map[string]string{"shop/a/old.yml": "o"},
			steps(put("shop/a/x.yml", "x"), move("shop/a", "shop/b"), move("shop/b", "shop/a")),
			map[string]string{"shop/a/old.yml": "o", "shop/a/x.yml": "x"}},
		{"a folder removed, then another moved to its name", map[string]string{"shop/a/x.yml": "new", "shop/b/x.yml": "old"},
			steps(remove("shop/b"), move("shop/a", "shop/b")),
			map[string]string{"shop/b/x.yml": "new"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := h.New(t)()
			seed(t, s, c.before)
			if err := s.Change(ctx, "shop", c.name, c.change); err != nil {
				t.Fatal(err)
			}
			if got := state(t, s); !maps.Equal(got, c.want) {
				t.Fatalf("after the change %v, want %v", got, c.want)
			}
			if left, err := s.Unfinished(key(t, "shop")); err != nil || len(left) != 0 {
				t.Fatalf("unfinished after the change: %v, %v", left, err)
			}
		})
	}
}

func fnFails(t *testing.T, h Harness) {
	s := h.New(t)()
	put(t, s, "shop/a.yml", "a")
	boom := errors.New("boom")
	err := s.Change(ctx, "shop", "fails", func(tx store.Tx) error {
		_ = tx.Put(key(t, "shop/a.yml"), []byte("changed"), store.Anything())
		_ = tx.Put(key(t, "shop/b.yml"), []byte("new"), store.Anything())
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want fn's own", err)
	}
	if got := state(t, s); !maps.Equal(got, map[string]string{"shop/a.yml": "a"}) {
		t.Fatalf("left %v", got)
	}
	put(t, s, "shop/a.yml", "after")
}

func refusedHalfWay(t *testing.T, h Harness) {
	s := h.New(t)()
	put(t, s, "shop/a.yml", "a")
	put(t, s, "shop/b.yml", "b")
	before := state(t, s)
	err := s.Change(ctx, "shop", "half", func(tx store.Tx) error {
		if err := tx.Put(key(t, "shop/a.yml"), []byte("a2"), store.Anything()); err != nil {
			return err
		}
		if err := tx.Put(key(t, "shop/new/c.yml"), []byte("c"), store.Absent()); err != nil {
			return err
		}
		// Read before the change began, so this Want no longer holds once the first write is made.
		return tx.Put(key(t, "shop/a.yml"), []byte("a3"), store.Holds(store.SumOf([]byte("a"))))
	})
	wantReason(t, err, store.ReasonChanged)
	if got := state(t, s); !maps.Equal(got, before) {
		t.Fatalf("a change refused half way left %v, want %v", got, before)
	}
}

func outsideKeys(t *testing.T, h Harness) {
	for _, s := range []string{"", "/etc/passwd", "../up", "a/../../up", "a\\b", "C:/x", "file:x", "a:b", "~/x", " x", "a/.queue/x", ".store/j", "a\x01b"} {
		if _, err := store.ParseKey(s); reason(err) != store.ReasonOutside {
			t.Errorf("ParseKey(%q) = %v, want outside", s, err)
		}
	}
	for in, want := range map[string]string{"a//b/": "a/b", "./a/./b": "a/b", "a/b:c": "a/b:c", "1:x": "1:x"} {
		if got, err := store.ParseKey(in); err != nil || string(got) != want {
			t.Errorf("ParseKey(%q) = %q, %v, want %q", in, got, err, want)
		}
	}
	s := h.New(t)()
	if _, _, err := s.Get(store.Key("../up")); reason(err) != store.ReasonOutside {
		t.Fatalf("Get of a key made by a conversion = %v, want outside", err)
	}
	wantReason(t, s.Change(ctx, "", "up", func(tx store.Tx) error {
		return tx.Put(store.Key("a/../../up"), []byte("x"), store.Anything())
	}), store.ReasonOutside)
}

func insideThing(t *testing.T, h Harness) {
	s := h.New(t)()
	wantReason(t, s.Change(ctx, "shop", "write", func(tx store.Tx) error {
		return tx.Put(key(t, "other/a.yml"), []byte("x"), store.Anything())
	}), store.ReasonOutside)
	wantReason(t, s.Change(ctx, "shop/blocks", "write", func(tx store.Tx) error { return nil }), store.ReasonOutside)
}

func race(t *testing.T, h Harness) {
	open := h.New(t)
	views := []*store.Scope{open(), open()}
	k := key(t, "count/n")
	put(t, views[0], "count/n", "0")
	const each = 10
	var wg sync.WaitGroup
	errs := make(chan error, each*len(views))
	for _, s := range views {
		for range each {
			wg.Add(1)
			go func() {
				defer wg.Done()
				errs <- s.Change(ctx, "count", "add one", func(tx store.Tx) error {
					item, _, err := tx.Get(k)
					if err != nil {
						return err
					}
					n, err := strconv.Atoi(string(item.Body))
					if err != nil {
						return err
					}
					return tx.Put(k, []byte(strconv.Itoa(n+1)), store.Holds(item.Sum))
				})
			}()
		}
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if got := state(t, views[0])["count/n"]; got != strconv.Itoa(each*len(views)) {
		t.Fatalf("count is %s, want %d", got, each*len(views))
	}
}

func revision(t *testing.T, h Harness) {
	s := h.New(t)()
	put(t, s, "shop/a.yml", "a")
	r1, err := s.Revision()
	if err != nil {
		t.Fatal(err)
	}
	_, _, _ = s.Get(key(t, "shop/a.yml"))
	if r2, _ := s.Revision(); r2 != r1 {
		t.Fatalf("a read moved the revision: %s to %s", r1, r2)
	}
	put(t, s, "shop/b.yml", "b")
	if r3, _ := s.Revision(); r3 == r1 {
		t.Fatal("a change left the revision where it was")
	}
}

func list(t *testing.T, h Harness) {
	s := h.New(t)()
	put(t, s, "shop/b.yml", "b")
	put(t, s, "shop/a/x.yml", "x")
	put(t, s, "other.yml", "o")
	got, err := s.List(key(t, "shop"))
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(got) != "[shop/a/x.yml shop/b.yml]" {
		t.Fatalf("List(shop) = %v", got)
	}
	if got, err := s.List(key(t, "none")); err != nil || len(got) != 0 {
		t.Fatalf("List(none) = %v, %v", got, err)
	}
	if got, err := s.List(key(t, "other.yml")); err != nil || fmt.Sprint(got) != "[other.yml]" {
		t.Fatalf("List(other.yml) = %v, %v", got, err)
	}
}

func listIn(t *testing.T, h Harness) {
	s := h.New(t)()
	seed(t, s, map[string]string{"shop/a.yml": "a", "shop/f/b.yml": "b", "shop/gone.yml": "g", "shop/again.yml": "1", "away/c.yml": "c", "other.yml": "o", "else/d.yml": "d"})
	var inside, whole, file, none []store.Key
	err := s.Change(ctx, "", "list in a change", func(tx store.Tx) error {
		steps := []error{
			tx.Put(key(t, "shop/a-new.yml"), []byte("n"), store.Absent()),
			tx.Remove(key(t, "shop/gone.yml"), store.Anything()),
			tx.Remove(key(t, "shop/again.yml"), store.Anything()),
			tx.Put(key(t, "shop/again.yml"), []byte("2"), store.Absent()),
			tx.Move(key(t, "shop/f"), key(t, "shop/g"), store.Anything()),
			tx.Move(key(t, "away"), key(t, "shop/away"), store.Anything()),
			tx.Move(key(t, "shop/away/c.yml"), key(t, "shop/c.yml"), store.Anything()),
		}
		if err := errors.Join(steps...); err != nil {
			return err
		}
		var err error
		if inside, err = store.ListIn(tx, key(t, "shop")); err != nil {
			return err
		}
		if whole, err = store.ListIn(tx, ""); err != nil {
			return err
		}
		if file, err = store.ListIn(tx, key(t, "shop/a-new.yml")); err != nil {
			return err
		}
		if none, err = store.ListIn(tx, key(t, "away")); err != nil {
			return err
		}
		_, err = store.ListIn(tx, store.Key("../x"))
		wantReason(t, err, store.ReasonOutside)
		probes := []string{"shop/a.yml", "shop/a-new.yml", "shop/gone.yml", "shop/again.yml", "shop/f/b.yml", "shop/g/b.yml", "away/c.yml", "shop/away/c.yml", "shop/c.yml", "other.yml", "else/d.yml"}
		agree(t, tx, "", probes...)
		agree(t, tx, "shop", probes...)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(inside) != "[shop/a-new.yml shop/a.yml shop/again.yml shop/c.yml shop/g/b.yml]" {
		t.Fatalf("ListIn(shop) = %v", inside)
	}
	if fmt.Sprint(whole) != "[else/d.yml other.yml shop/a-new.yml shop/a.yml shop/again.yml shop/c.yml shop/g/b.yml]" {
		t.Fatalf("ListIn() = %v", whole)
	}
	if fmt.Sprint(file) != "[shop/a-new.yml]" || none == nil || len(none) != 0 {
		t.Fatalf("ListIn(shop/a-new.yml) = %v, ListIn(away) = %v", file, none)
	}
	// Once made, the scope lists what the change listed.
	if got, err := s.List(""); err != nil || fmt.Sprint(got) != fmt.Sprint(whole) {
		t.Fatalf("List() after = %v, %v; the change listed %v", got, err, whole)
	}
	// A change of one thing lists another as it is, with its own writes kept to its thing.
	err = s.Change(ctx, "shop", "list another thing", func(tx store.Tx) error {
		if err := tx.Put(key(t, "shop/z.yml"), []byte("z"), store.Absent()); err != nil {
			return err
		}
		got, err := store.ListIn(tx, key(t, "else"))
		if err != nil {
			return err
		}
		if fmt.Sprint(got) != "[else/d.yml]" {
			t.Errorf("ListIn(else) in a change of shop = %v", got)
		}
		agree(t, tx, "", "else/d.yml", "shop/z.yml", "shop/a.yml", "other.yml")
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ListIn(tx{}, ""); err == nil {
		t.Fatal("ListIn of a Tx no change handed out gave no error")
	}
}

// agree checks that ListIn and Get tell one story inside a change: ListIn(prefix) lists exactly the probes inside prefix that Get finds, and every key it lists Gets as there.
func agree(t *testing.T, tx store.Tx, prefix string, probes ...string) {
	t.Helper()
	var at store.Key
	if prefix != "" {
		at = key(t, prefix)
	}
	listed, err := store.ListIn(tx, at)
	if err != nil {
		t.Errorf("ListIn(%s): %v", prefix, err)
		return
	}
	in := map[store.Key]bool{}
	for _, k := range listed {
		in[k] = true
		if _, there, err := tx.Get(k); err != nil || !there {
			t.Errorf("%s is listed but Get gives there=%v, %v", k, there, err)
		}
	}
	for _, p := range probes {
		if prefix != "" && p != prefix && !strings.HasPrefix(p, prefix+"/") {
			continue
		}
		k := key(t, p)
		_, there, err := tx.Get(k)
		switch {
		case err != nil:
			t.Errorf("Get(%s): %v", k, err)
		case there != in[k]:
			t.Errorf("Get(%s) gives there=%v, but ListIn(%s) lists it: %v", k, there, prefix, in[k])
		}
	}
}

// tx is a Tx no change handed out.
type tx struct{ store.Tx }

// crashCase is one change the crash test stops after each of its writes in turn.
type crashCase struct {
	name   string
	thing  string
	before map[string]string
	change func(t *testing.T, tx store.Tx) error
}

var crashCases = []crashCase{
	{"a new file", "shop", map[string]string{"keep.yml": "k"}, func(t *testing.T, tx store.Tx) error {
		return tx.Put(key(t, "shop/a.yml"), []byte("a"), store.Absent())
	}},
	{"a file replaced", "shop", map[string]string{"shop/a.yml": "a"}, func(t *testing.T, tx store.Tx) error {
		return tx.Put(key(t, "shop/a.yml"), []byte("a2"), store.Anything())
	}},
	{"files written into new folders", "shop", map[string]string{"shop/a.yml": "a"}, func(t *testing.T, tx store.Tx) error {
		if err := tx.Put(key(t, "shop/a.yml"), []byte("a2"), store.Anything()); err != nil {
			return err
		}
		return tx.Put(key(t, "shop/x/y/b.yml"), []byte("b"), store.Absent())
	}},
	{"a file removed beside a write", "shop", map[string]string{"shop/a.yml": "a", "shop/b.yml": "b"}, func(t *testing.T, tx store.Tx) error {
		if err := tx.Remove(key(t, "shop/a.yml"), store.Anything()); err != nil {
			return err
		}
		return tx.Put(key(t, "shop/b.yml"), []byte("b2"), store.Anything())
	}},
	{"a folder removed", "shop", map[string]string{"shop/a.yml": "a", "shop/f/b.yml": "b", "shop/f/g/c.yml": "c"}, func(t *testing.T, tx store.Tx) error {
		return tx.Remove(key(t, "shop/f"), store.Anything())
	}},
	{"a file moved", "shop", map[string]string{"shop/a.yml": "a"}, func(t *testing.T, tx store.Tx) error {
		return tx.Move(key(t, "shop/a.yml"), key(t, "shop/n/a.yml"), store.Anything())
	}},
	{"a folder moved and written into", "shop", map[string]string{"shop/f/b.yml": "b"}, func(t *testing.T, tx store.Tx) error {
		if err := tx.Move(key(t, "shop/f"), key(t, "shop/g"), store.Anything()); err != nil {
			return err
		}
		return tx.Put(key(t, "shop/g/c.yml"), []byte("c"), store.Absent())
	}},
	{"a file written, then its folder moved", "shop", map[string]string{"shop/f/b.yml": "b"}, func(t *testing.T, tx store.Tx) error {
		if err := tx.Put(key(t, "shop/f/c.yml"), []byte("c"), store.Absent()); err != nil {
			return err
		}
		return tx.Move(key(t, "shop/f"), key(t, "shop/g"), store.Anything())
	}},
	{"a thing moved in the folder's turn", "", map[string]string{"shop/a.yml": "a", "other.yml": "o"}, func(t *testing.T, tx store.Tx) error {
		if err := tx.Move(key(t, "shop"), key(t, "store"), store.Anything()); err != nil {
			return err
		}
		return tx.Put(key(t, "other.yml"), []byte("o2"), store.Anything())
	}},
}

// crashes stops each case after its first write, then its second, and so on until the change finishes, and checks every time that the next change finds the scope all before or all after, with nothing left unfinished.
func crashes(t *testing.T, h Harness) {
	for _, c := range crashCases {
		t.Run(c.name, func(t *testing.T) {
			var after map[string]string
			{
				s := h.New(t)()
				seed(t, s, c.before)
				if err := s.Change(ctx, store.Key(c.thing), c.name, func(tx store.Tx) error { return c.change(t, tx) }); err != nil {
					t.Fatalf("the change itself: %v", err)
				}
				after = state(t, s)
			}
			for n := 1; ; n++ {
				open := h.New(t)
				s := open()
				seed(t, s, c.before)
				fired := h.Crash(n)
				err := s.Change(ctx, store.Key(c.thing), c.name, func(tx store.Tx) error { return c.change(t, tx) })
				if !fired() {
					if err != nil {
						t.Fatalf("not stopped after write %d, yet: %v", n, err)
					}
					if n == 1 {
						t.Fatal("the change made no write the crash could stop after")
					}
					t.Logf("stopped after each of %d writes", n-1)
					return
				}
				next := open()
				// The next change of the thing puts back what the one stopped dead left, before it runs.
				if err := next.Change(ctx, store.Key(c.thing), "after the crash", func(store.Tx) error { return nil }); err != nil {
					t.Fatalf("after write %d: the next change: %v", n, err)
				}
				got := state(t, next)
				if !maps.Equal(got, c.before) && !maps.Equal(got, after) {
					t.Fatalf("after write %d the scope holds %v, neither before %v nor after %v", n, got, c.before, after)
				}
				for _, thing := range []string{"", "shop", "store"} {
					left, err := next.Unfinished(store.Key(thing))
					if err != nil || len(left) != 0 {
						t.Fatalf("after write %d: unfinished in %q: %v, %v", n, thing, left, err)
					}
				}
				put(t, next, "shop/later.yml", "later")
			}
		})
	}
}

// seed writes things, each in its own change.
func seed(t *testing.T, s *store.Scope, things map[string]string) {
	t.Helper()
	for k, v := range things {
		put(t, s, k, v)
	}
}
