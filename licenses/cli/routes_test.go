package cli_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"go.naywee.com/contracts/v2"

	"github.com/tugbayatilla/mankit/v4/home"
	"github.com/tugbayatilla/mankit/v4/licenses"
	"github.com/tugbayatilla/mankit/v4/licenses/cli"
	"github.com/tugbayatilla/mankit/v4/problem"
	"github.com/tugbayatilla/mankit/v4/serve"
)

const token = "s3cret"

// server is the app's server with the thing license mounted by cli.Routes, behind serve's guard with a token of the test's own.
func (w *world) server() http.Handler {
	w.t.Helper()
	data := w.t.TempDir()
	w.t.Setenv("XDG_DATA_HOME", data)
	tokens := filepath.Join(data, home.Org, home.Folder(w.app.ID), "servers")
	if err := os.MkdirAll(tokens, 0o700); err != nil {
		w.t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tokens, "token"), []byte(token+"\n"), 0o600); err != nil {
		w.t.Fatal(err)
	}
	c, list := w.door()
	h, err := w.app.Handler(serve.Site{Routes: cli.Routes(c, list)}, nil)
	if err != nil {
		w.t.Fatal(err)
	}
	return h
}

// call is one request to h with the token, as a script sends it, with body as JSON when there is one.
func call(t *testing.T, h http.Handler, method, target string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, "http://127.0.0.1:7480"+target, bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// question is the Question a 428 answers with.
func question(t *testing.T, rec *httptest.ResponseRecorder) (id, detail string) {
	t.Helper()
	if rec.Code != http.StatusPreconditionRequired {
		t.Fatalf("status %d, want 428:\n%s", rec.Code, rec.Body)
	}
	if err := contracts.Validate("question/v1/question.schema.json", rec.Body.Bytes()); err != nil {
		t.Fatalf("not a Question: %v\n%s", err, rec.Body)
	}
	var e struct {
		Data struct{ ID, Detail string }
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &e); err != nil {
		t.Fatal(err)
	}
	return e.Data.ID, e.Data.Detail
}

// refusal is the one Problem of a failed answer, checked against problem/v1 through the envelope.
func refusal(t *testing.T, rec *httptest.ResponseRecorder, status int) problem.Problem {
	t.Helper()
	if rec.Code != status {
		t.Fatalf("status %d, want %d:\n%s", rec.Code, status, rec.Body)
	}
	var e struct{ Errors []problem.Problem }
	if err := json.Unmarshal(rec.Body.Bytes(), &e); err != nil || len(e.Errors) != 1 {
		t.Fatalf("%v\n%s", err, rec.Body)
	}
	if e.Errors[0].APIVersion != problem.APIVersion || e.Errors[0].Kind != problem.Kind {
		t.Fatalf("not a problem/v1: %+v", e.Errors[0])
	}
	return e.Errors[0]
}

// The door's GET is the CLI's `license show -o json`, byte for byte, for free, trial and licensed, and a LicenseShow of contracts mankit/v3.
func TestHTTPShowIsTheCLIAnswer(t *testing.T) {
	for _, c := range []struct {
		name  string
		put   func(*world)
		state string
	}{
		{"free", func(*world) {}, licenses.StateFree},
		{"trial", func(w *world) { w.note("2026-12-03") }, licenses.StateTrial},
		{"licensed", func(w *world) { w.putOrg(w.license([]string{"events"}, "2026-11-06", "2027-11-06")) }, licenses.StateLicensed},
	} {
		t.Run(c.name, func(t *testing.T) {
			w := newWorld(t, "2026-12-07")
			c.put(w)
			rec := call(t, w.server(), http.MethodGet, cli.Path, nil)
			if rec.Code != http.StatusOK {
				t.Fatalf("status %d:\n%s", rec.Code, rec.Body)
			}
			if err := contracts.Validate(schema, rec.Body.Bytes()); err != nil {
				t.Fatalf("not a LicenseShow answer: %v\n%s", err, rec.Body)
			}
			out, err := w.run("show", "-o", "json")
			if err != nil {
				t.Fatal(err)
			}
			if rec.Body.String() != out {
				t.Fatalf("HTTP:\n%s\nCLI:\n%s", rec.Body, out)
			}
			if s := w.show(); s.State != c.state {
				t.Fatalf("state %s, want %s", s.State, c.state)
			}
		})
	}
}

// POST keeps nothing without a yes, asks the CLI's question, and keeps the license on the yes to it, or on a script's confirm=yes; org=true is --org.
func TestHTTPAddAsksAndKeeps(t *testing.T) {
	for _, c := range []struct {
		query string
		flags []string
		kept  func(w *world) string
	}{
		{"", nil, func(w *world) string { return filepath.Join(w.appDir(), "licenses", "acme-2026.license") }},
		{"?org=true", []string{"--org"}, func(w *world) string { return filepath.Join(w.org, "licenses", "acme-2026.license") }},
	} {
		w := newWorld(t, "2026-11-19")
		h := w.server()
		raw := w.license([]string{"sync"}, "2026-11-06", "2027-11-06")
		sep := "?"
		if c.query != "" {
			sep = "&"
		}
		id, detail := question(t, call(t, h, http.MethodPost, cli.Path+c.query, raw))
		if _, err := os.Stat(c.kept(w)); !os.IsNotExist(err) {
			t.Fatal("kept without a yes")
		}
		if _, err := w.run(append([]string{"add", w.file(raw)}, c.flags...)...); err == nil || detail != w.asked {
			t.Fatalf("HTTP asked %q, the CLI %q", detail, w.asked)
		}
		rec := call(t, h, http.MethodPost, cli.Path+c.query+sep+"confirm="+id, raw)
		if rec.Code != http.StatusOK || contracts.Validate(schema, rec.Body.Bytes()) != nil {
			t.Fatalf("status %d:\n%s", rec.Code, rec.Body)
		}
		if got, err := os.ReadFile(c.kept(w)); err != nil || !bytes.Equal(got, raw) {
			t.Fatalf("kept %q, %v", got, err)
		}
		if s := w.show(); s.State != licenses.StateLicensed || !s.Licenses[0].Active {
			t.Fatalf("show = %+v", s)
		}
		// A script's yes in one call keeps a license of the same id that replaces it.
		longer := w.license([]string{"sync"}, "2026-11-06", "2028-11-06")
		if rec := call(t, h, http.MethodPost, cli.Path+c.query+sep+"confirm=yes", longer); rec.Code != http.StatusOK {
			t.Fatalf("status %d:\n%s", rec.Code, rec.Body)
		}
		if got, _ := os.ReadFile(c.kept(w)); !bytes.Equal(got, longer) {
			t.Fatal("the license was not replaced")
		}
	}
}

// A body that is no license file, or a file the app does not take, is refused without a question and keeps nothing.
func TestHTTPAddRefusesWhatItCannotTake(t *testing.T) {
	w := newWorld(t, "2026-11-19")
	h := w.server()
	for name, c := range map[string]struct {
		body   []byte
		reason string
	}{
		"an empty body":              {nil, licenses.ReasonUnreadable},
		"JSON that is not a license": {[]byte(`{"hello": "world"}`), licenses.ReasonUnreadable},
		"an old trial":               {w.old(oldPersonTrial), licenses.ReasonRetiredKind},
		"an old org license":         {w.old(oldOrg), licenses.ReasonUnreadable},
	} {
		t.Run(name, func(t *testing.T) {
			for _, confirm := range []string{"", "?confirm=yes", "?org=true&confirm=yes"} {
				p := refusal(t, call(t, h, http.MethodPost, cli.Path+confirm, c.body), http.StatusBadRequest)
				if p.Type != problem.TypeRefused || p.Reason != c.reason {
					t.Fatalf("%q: %+v, want refused %s", confirm, p, c.reason)
				}
			}
			for _, d := range []string{filepath.Join(w.org, "licenses"), filepath.Join(w.appDir(), "licenses")} {
				if _, err := os.Stat(d); !os.IsNotExist(err) {
					t.Fatal("kept a refused file")
				}
			}
		})
	}
	// A body that is not JSON is refused by serve's guard.
	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:7480"+cli.Path+"?confirm=yes", bytes.NewReader([]byte("not json")))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "text/plain")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if p := refusal(t, rec, http.StatusUnsupportedMediaType); p.Reason != serve.ReasonNotJSON {
		t.Fatalf("%+v", p)
	}
}

// POST .../{id}/use asks the CLI's question and makes the license active on the yes; a license not there is not_found and one refused is refused, unasked.
func TestHTTPUseAsksAndChooses(t *testing.T) {
	w := newWorld(t, "2026-12-07")
	w.putOrg(w.license([]string{"events"}, "2026-11-06", "2027-11-06"))
	w.putApp(w.licenseOf("ada-2026", "Ada", []string{"sync"}, "2026-01-01", "2027-11-06"))
	w.putApp([]byte(`{"metadata":{"id":"broken"}}`))
	h := w.server()
	use := cli.Path + "/ada-2026/use"
	id, detail := question(t, call(t, h, http.MethodPost, use, nil))
	if s := w.show(); s.Licenses[0].Active {
		t.Fatalf("chose without a yes: %+v", s.Licenses)
	}
	if _, err := w.run("use", "ada-2026"); err == nil || detail != w.asked {
		t.Fatalf("HTTP asked %q, the CLI %q", detail, w.asked)
	}
	rec := call(t, h, http.MethodPost, use+"?confirm="+id, nil)
	if rec.Code != http.StatusOK || contracts.Validate(schema, rec.Body.Bytes()) != nil {
		t.Fatalf("status %d:\n%s", rec.Code, rec.Body)
	}
	if s := w.show(); !s.Licenses[0].Active || s.Licenses[0].ID != "ada-2026" {
		t.Fatalf("show = %+v", s.Licenses)
	}
	for target, want := range map[string]int{cli.Path + "/nobody/use": http.StatusNotFound, cli.Path + "/broken/use": http.StatusBadRequest} {
		for _, confirm := range []string{"", "?confirm=yes"} {
			refusal(t, call(t, h, http.MethodPost, target+confirm, nil), want)
		}
	}
	if s := w.show(); !s.Licenses[0].Active {
		t.Fatalf("the choice changed: %+v", s.Licenses)
	}
}

// DELETE .../{id} asks the CLI's question, takes the license away on the yes and leaves the trial; a license not there is not_found, unasked.
func TestHTTPRemoveAsksAndLeavesTheTrial(t *testing.T) {
	w := newWorld(t, "2026-12-07")
	w.putOrg(w.license([]string{"events"}, "2026-11-06", "2027-11-06"))
	w.note("2026-12-03")
	h := w.server()
	target := cli.Path + "/acme-2026"
	id, detail := question(t, call(t, h, http.MethodDelete, target, nil))
	if s := w.show(); s.State != licenses.StateLicensed {
		t.Fatalf("removed without a yes: %+v", s)
	}
	if _, err := w.run("remove", "acme-2026"); err == nil || detail != w.asked {
		t.Fatalf("HTTP asked %q, the CLI %q", detail, w.asked)
	}
	rec := call(t, h, http.MethodDelete, target+"?confirm="+id, nil)
	if rec.Code != http.StatusOK || contracts.Validate(schema, rec.Body.Bytes()) != nil {
		t.Fatalf("status %d:\n%s", rec.Code, rec.Body)
	}
	if s := w.show(); s.State != licenses.StateTrial || len(s.Licenses) != 0 || s.Trial == nil {
		t.Fatalf("show = %+v", s)
	}
	for _, confirm := range []string{"", "?confirm=yes"} {
		if p := refusal(t, call(t, h, http.MethodDelete, target+confirm, nil), http.StatusNotFound); p.Type != problem.TypeNotFound {
			t.Fatalf("%q: %+v", confirm, p)
		}
	}
}

// A failure that is not a refusal or a missing license, here a config folder that cannot be found, still asks, and a script's yes is answered with the error: nothing is kept or removed.
func TestHTTPChangeWithAnotherFailureWritesNothing(t *testing.T) {
	w := newWorld(t, "2026-12-07")
	kept := w.license([]string{"events"}, "2026-11-06", "2027-11-06")
	w.putOrg(kept)
	h := w.server()
	file := filepath.Join(w.org, "licenses", "acme-2026.license")
	config := os.Getenv("XDG_CONFIG_HOME")
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("HOME", "")

	for _, c := range []struct{ method, target string }{{http.MethodPost, cli.Path}, {http.MethodPost, cli.Path + "/acme-2026/use"}, {http.MethodDelete, cli.Path + "/acme-2026"}} {
		body := []byte(nil)
		if c.target == cli.Path {
			body = w.perpetual()
		}
		question(t, call(t, h, c.method, c.target, body))
		if p := refusal(t, call(t, h, c.method, c.target+"?confirm=yes", body), http.StatusInternalServerError); p.Type == problem.TypeRefused || p.Type == problem.TypeNotFound {
			t.Fatalf("%s %s: %+v", c.method, c.target, p)
		}
	}
	t.Setenv("XDG_CONFIG_HOME", config)
	if got, err := os.ReadFile(file); err != nil || !bytes.Equal(got, kept) {
		t.Fatalf("the license changed: %q, %v", got, err)
	}
}
