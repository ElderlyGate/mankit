package licenses_test

import (
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/tugbayatilla/mankit/v4/build"
	"github.com/tugbayatilla/mankit/v4/licenses"
)

func TestTheShippedTrustedKeysLoad(t *testing.T) {
	trust, err := licenses.Trusted()
	if err != nil {
		t.Fatalf("Trusted: %v", err)
	}
	if trust.Release != build.Released() {
		t.Errorf("Release = %v, want build.Released() = %v", trust.Release, build.Released())
	}
	if _, err := licenses.New(licenses.Config{ID: "designer", Trust: trust}); err != nil {
		t.Errorf("New refuses the shipped keys: %v", err)
	}
}

func TestTheShippedProdKeyIsTakenByAReleaseOnly(t *testing.T) {
	for _, release := range []bool{true, false} {
		trust, err := licenses.ReadTrust("trusted.json", licenses.Shipped, release)
		if err != nil {
			t.Fatalf("release %v: %v", release, err)
		}
		_, err = licenses.TrustKey(trust, "prod-2026")
		if release && err != nil {
			t.Errorf("a release refuses the prod key prod-2026: %v", err)
		}
		if !release && reason(err) != licenses.ReasonOtherBuild {
			t.Errorf("a dev build takes the prod key prod-2026: %v, want %s", err, licenses.ReasonOtherBuild)
		}
	}
}

func TestTrustedKeysAreSplitByBuild(t *testing.T) {
	raw, err := os.ReadFile("testdata/trusted.json")
	if err != nil {
		t.Fatal(err)
	}
	key := func(b64 string) ed25519.PublicKey {
		k, err := base64.StdEncoding.DecodeString(b64)
		if err != nil {
			t.Fatal(err)
		}
		return k
	}
	wantProd := map[string]ed25519.PublicKey{
		"acme-prod-1": key("qPmZ6Wh9dh0qeTN5cq4tANX0pJYTJcphn+N+97zqwdw="),
		"acme-prod-2": key("5S+jfqyEMtv/nYeUwiOtLVQmr3rDSiNEeMFn8QgZBOQ="),
	}
	wantDev := map[string]ed25519.PublicKey{"acme-dev-1": key("/Du/AnhqWh0dZZWlJKWtFEnJBWlaWM1wIzaigTUidnc=")}
	equal := func(a, b ed25519.PublicKey) bool { return a.Equal(b) }
	for _, release := range []bool{true, false} {
		trust, err := licenses.ReadTrust("trusted.json", raw, release)
		if err != nil {
			t.Fatalf("release %v: %v", release, err)
		}
		if trust.Release != release {
			t.Errorf("Release = %v, want %v", trust.Release, release)
		}
		if !maps.EqualFunc(trust.Prod, wantProd, equal) || !maps.EqualFunc(trust.Dev, wantDev, equal) {
			t.Errorf("release %v: Prod %v, Dev %v; want Prod %v, Dev %v", release, trust.Prod, trust.Dev, wantProd, wantDev)
		}
	}
}

func TestAnEmptyListTrustsNoKey(t *testing.T) {
	trust, err := licenses.ReadTrust("trusted.json", []byte("[]\n"), true)
	if err != nil || len(trust.Prod) != 0 || len(trust.Dev) != 0 {
		t.Errorf("ReadTrust(empty) = %v, %v; want no keys and no error", trust, err)
	}
}

func TestAWrongTrustedFileIsRefusedForItsOwnReason(t *testing.T) {
	why := map[string]string{
		"cut-short.json":     "is not a list of keys: unexpected EOF",
		"not-a-list.json":    "is not a list of keys: json: cannot unmarshal object",
		"not-base64.json":    "illegal base64 data",
		"unknown-field.json": `unknown field "until"`,
		"null.json":          "it is not a list of keys",
		"two-lists.json":     "it holds more than one list of keys",
		"no-id.json":         "it lists a key without an id",
		"no-build.json":      `has the build "", not prod or dev`,
		"other-build.json":   `has the build "release", not prod or dev`,
		"twice.json":         "it lists the key acme-prod-1 twice",
		"prod-and-dev.json":  "the key id acme-1 is both a prod and a dev key",
		"short-key.json":     "the prod key acme-prod-1 is not an Ed25519 public key",
		"no-key.json":        "the key acme-prod-1 has no public key",
	}
	files, err := filepath.Glob("testdata/refused/*.json")
	if err != nil || len(files) != len(why) {
		t.Fatalf("refused files %v (%v), want one per reason: %v", files, err, slices.Sorted(maps.Keys(why)))
	}
	for _, f := range files {
		name := filepath.Base(f)
		t.Run(name, func(t *testing.T) {
			reason, ok := why[name]
			if !ok {
				t.Fatalf("%s has no reason in the table", name)
			}
			raw, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			_, err = licenses.ReadTrust(name, raw, false)
			if !errors.Is(err, licenses.ErrTrustedKeys) {
				t.Fatalf("ReadTrust = %v, want ErrTrustedKeys", err)
			}
			if prefix := "licenses: " + name + ": "; !strings.HasPrefix(err.Error(), prefix) || !strings.Contains(err.Error(), reason) {
				t.Errorf("the error %q does not start %q and say %q", err, prefix, reason)
			}
		})
	}
}
