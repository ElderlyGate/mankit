//go:build twouser

package store_test

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/tugbayatilla/mankit/v4/problem"
	"github.com/tugbayatilla/mankit/v4/store"
)

// TestTwoUsers is one step of testdata/twouser.sh, which runs each step as one of two users on one of two machines sharing a served folder.
func TestTwoUsers(t *testing.T) {
	s := served(t, os.Getenv("STORE_TWOUSER_DIR"))
	shop := []string{"shop/a.yml", "shop/b.yml", "shop/c/d.yml"}
	switch role := os.Getenv("STORE_TWOUSER_ROLE"); role {
	case "seed":
		if err := put(t, s, "shop/a.yml", "a"); err != nil {
			t.Fatal(err)
		}
	case "crash":
		store.ExitAfter(4)
		_ = s.Change(ctx, "shop", "three writes", func(tx store.Tx) error {
			for _, k := range shop {
				if err := tx.Put(key(t, k), []byte("cut short"), store.Anything()); err != nil {
					return err
				}
			}
			return nil
		})
		t.Fatal("the change was not stopped")
	case "refused":
		err := put(t, s, "shop/a.yml", "the other user's")
		var p *problem.Problem
		if !errors.As(err, &p) || p.Type != problem.TypeRefused || p.Reason != store.ReasonUnfinished {
			t.Fatalf("the other user's change = %v, want refused unfinished_change", err)
		}
		for _, says := range strings.Fields(os.Getenv("STORE_TWOUSER_NAMES")) {
			if !strings.Contains(p.Detail, says) {
				t.Fatalf("the refusal %q does not name %q", p.Detail, says)
			}
		}
		fmt.Printf("refused: %s: %s\n", p.Reason, p.Detail)
	case "recover":
		if err := s.Change(ctx, "shop", "the next run", func(store.Tx) error { return nil }); err != nil {
			t.Fatal(err)
		}
		keys, _ := s.List("")
		item, _, _ := s.Get(key(t, "shop/a.yml"))
		if fmt.Sprint(keys) != "[shop/a.yml]" || string(item.Body) != "a" {
			t.Fatalf("after putting back: %v, shop/a.yml holds %q", keys, item.Body)
		}
		if left, err := s.Unfinished("shop"); err != nil || len(left) != 0 {
			t.Fatalf("unfinished after putting back: %v, %v", left, err)
		}
		fmt.Println("put back: the scope holds", keys)
	case "after":
		if err := put(t, s, "shop/a.yml", "the other user's"); err != nil {
			t.Fatal(err)
		}
		fmt.Println("the other user's change went through")
	default:
		t.Fatalf("STORE_TWOUSER_ROLE %q is no step; testdata/twouser.sh runs this test", role)
	}
}
