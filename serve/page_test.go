package serve_test

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ElderlyGate/contracts"

	"github.com/tugbayatilla/mankit/v4/home"
	"github.com/tugbayatilla/mankit/v4/serve"
)

// shop is an app of the tests' own, whose CLI is shop.
var shop = serve.App{ID: "shop", Command: "shop", Version: "1.0.0", Contracts: []serve.Contract{{Version: "v1"}}, Addr: "127.0.0.1:0"}

// shopHandler is shop's server, with its log in log.
func shopHandler(t *testing.T, log *bytes.Buffer) http.Handler {
	t.Helper()
	s := site(nil)
	s.Log = log
	servers := machine(t) // the tests' app's servers folder; shop's is beside it
	writeToken(t, filepath.Join(filepath.Dir(filepath.Dir(servers)), home.Folder("shop"), "servers"), token)
	h, err := shop.Handler(s, nil)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

// browse is one request as a person's browser sends it: a navigation that prefers a page.
func browse(t *testing.T, h http.Handler, url string, header map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("GET", url, nil)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Sec-Fetch-Mode", "navigate")
	for k, v := range header {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

// isPage checks w is the readable page with its status, the words a person needs, and no trace of the token.
func isPage(t *testing.T, w *httptest.ResponseRecorder, status int, words ...string) {
	t.Helper()
	body := w.Body.String()
	if w.Code != status || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/html") {
		t.Fatalf("status %d, %s, want %d and a page:\n%s", w.Code, w.Header().Get("Content-Type"), status, body)
	}
	if w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Errorf("the page may be kept or leak its address: %v", w.Header())
	}
	for _, want := range append([]string{"What went wrong", "<!doctype html>"}, words...) {
		if !strings.Contains(body, want) {
			t.Errorf("the page does not say %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, token) {
		t.Fatalf("the page holds the token:\n%s", body)
	}
}

// A browser that opens the link on a name the server does not answer to reads why, and what to do, never the token the link carried.
func TestABrowserOnAnotherHostReadsAPage(t *testing.T) {
	var log bytes.Buffer
	h := shopHandler(t, &log)
	w := browse(t, h, "http://elsewhere.example/?token="+token, nil)
	isPage(t, w, http.StatusForbidden, "This address is not one the server answers on", "shop serve --host &lt;name&gt;")
	if strings.Contains(log.String(), token) {
		t.Fatalf("the log holds the token:\n%s", log.String())
	}
}

// A program asking the same keeps the envelope, as before.
func TestAProgramOnAnotherHostKeepsTheEnvelope(t *testing.T) {
	var log bytes.Buffer
	h := shopHandler(t, &log)
	for _, target := range []string{"/api/greeting", "/board"} {
		req := httptest.NewRequest("GET", "http://elsewhere.example"+target, nil)
		req.Header.Set("Accept", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != http.StatusForbidden || !strings.HasPrefix(w.Header().Get("Content-Type"), "application/json") {
			t.Fatalf("%s: status %d, %s", target, w.Code, w.Header().Get("Content-Type"))
		}
		if err := contracts.Validate("envelope/v1/envelope.schema.json", w.Body.Bytes()); err != nil {
			t.Errorf("%s: not the envelope: %v\n%s", target, err, w.Body)
		}
	}
}

// A browser that opens a page or an API path without the token reads where its link is; the app's own page fetching the API gets the envelope.
func TestWithoutTheTokenABrowserReadsAPageAndAFetchTheEnvelope(t *testing.T) {
	var log bytes.Buffer
	h := shopHandler(t, &log)
	isPage(t, browse(t, h, "http://127.0.0.1:7480/board", nil), http.StatusUnauthorized, "Open this page from its link", "shop server link")
	isPage(t, browse(t, h, "http://127.0.0.1:7480/?token=wrong", nil), http.StatusUnauthorized, "Open this page from its link")
	isPage(t, browse(t, h, "http://127.0.0.1:7480/api/greeting", nil), http.StatusUnauthorized, "Open this page from its link")
	req := httptest.NewRequest("GET", "http://127.0.0.1:7480/api/greeting", nil)
	req.Header.Set("Accept", "*/*")
	req.Header.Set("Sec-Fetch-Mode", "cors")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized || !strings.HasPrefix(w.Header().Get("Content-Type"), "application/json") {
		t.Fatalf("a fetch: status %d, %s", w.Code, w.Header().Get("Content-Type"))
	}
}

// A contract the app does not serve is a page to a browser and the envelope to a program.
func TestAContractNotServedIsAPageToABrowser(t *testing.T) {
	var log bytes.Buffer
	h := shopHandler(t, &log)
	isPage(t, browse(t, h, "http://127.0.0.1:7480/api/greeting", map[string]string{"Authorization": "Bearer " + token, serve.ContractHeader: "v9"}),
		http.StatusNotAcceptable, "This contract version is not served", "GET /api/version")
	w := call(t, h, "GET", "/api/greeting", "", map[string]string{serve.ContractHeader: "v9"})
	if w.Code != http.StatusNotAcceptable || !strings.HasPrefix(w.Header().Get("Content-Type"), "application/json") {
		t.Fatalf("a program: status %d, %s", w.Code, w.Header().Get("Content-Type"))
	}
}

// A program that asks with */* or no Accept keeps the envelope on every path, a page path too, as curl and a health check do; html refused is no page either.
func TestAProgramKeepsTheEnvelopeWhateverThePath(t *testing.T) {
	var log bytes.Buffer
	h := shopHandler(t, &log)
	for _, c := range []struct{ target, accept string }{{"/", "*/*"}, {"/", ""}, {"/board", "*/*"}, {"/board", "text/html;q=0, */*"}, {"/board", "image/*,*/*;q=0.8"}, {"/api/greeting", "*/*"}} {
		req := httptest.NewRequest("GET", "http://elsewhere.example"+c.target, nil)
		if c.accept != "" {
			req.Header.Set("Accept", c.accept)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != http.StatusForbidden || !strings.HasPrefix(w.Header().Get("Content-Type"), "application/json") {
			t.Errorf("%s with Accept %q: status %d, %s, want the envelope", c.target, c.accept, w.Code, w.Header().Get("Content-Type"))
		}
	}
	// A browser asking a page path without navigating, as an old one does, still ranks html first.
	req := httptest.NewRequest("GET", "http://elsewhere.example/board", nil)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,*/*;q=0.8")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	isPage(t, w, http.StatusForbidden, "This address is not one the server answers on")
}
