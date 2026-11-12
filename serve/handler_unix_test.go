//go:build !windows

package serve_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/tugbayatilla/mankit/v2/serve"
)

// A pipe exists on Linux and macOS only.

func TestServeRefusesAPipeInPlaceOfTheTokenAtOnce(t *testing.T) {
	dir := machine(t)
	writeToken(t, dir, token)
	h, err := app.Handler(site(nil), nil)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "token")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Fatal(err)
	}
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- call(t, h, "GET", "/api/greeting", "", nil) }()
	select {
	case w := <-done:
		e := refusal(t, w, http.StatusInternalServerError, serve.CodeConfig)
		if !strings.Contains(e.Message, path) {
			t.Fatalf("the refusal does not name %s: %s", path, e.Message)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a pipe in place of the token holds the request")
	}
}
