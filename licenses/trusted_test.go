package licenses_test

import (
	"crypto/ed25519"
	"encoding/base64"
	"maps"
	"os"
	"path/filepath"
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

func TestAWrongTrustedFileIsRefusedNamingIt(t *testing.T) {
	files, err := filepath.Glob("testdata/refused/*.json")
	if err != nil || len(files) == 0 {
		t.Fatalf("no refused files: %v", err)
	}
	for _, f := range files {
		t.Run(filepath.Base(f), func(t *testing.T) {
			raw, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			_, err = licenses.ReadTrust(filepath.Base(f), raw, false)
			if err == nil {
				t.Fatal("taken, want refused")
			}
			if !strings.Contains(err.Error(), filepath.Base(f)) {
				t.Errorf("the error %q does not name the file", err)
			}
		})
	}
}
