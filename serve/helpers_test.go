package serve_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ElderlyGate/contracts"
	"github.com/spf13/cobra"

	"github.com/tugbayatilla/mankit/v3/ask"
	"github.com/tugbayatilla/mankit/v3/home"
	"github.com/tugbayatilla/mankit/v3/serve"
)

// The tests serve as designer, so its version answer is checked against its own contract too.
var app = serve.App{ID: "designer", Command: "e-designer", Version: "5.0.0", Contracts: []string{"v1"}, Addr: "127.0.0.1:0"}

// apiVersion is the apiVersion of designer's answers in contract v1.
var apiVersion = home.Org + "/designer/app/v1"

const token = "s3cret"

type greeting struct {
	Text string `json:"text"`
}

func site(styles func() (map[ask.Type]ask.Style, error)) serve.Site {
	return serve.Site{
		Name: "shop",
		Routes: []serve.Route{
			{Pattern: "GET /api/greeting", Handler: func(w http.ResponseWriter, r *http.Request) {
				serve.Write(w, r, http.StatusOK, "Greeting", greeting{Text: "hello"})
			}},
			{Pattern: "POST /api/greeting", Action: ask.Action{Type: ask.Edit, What: "change the greeting"}, Handler: func(w http.ResponseWriter, r *http.Request) {
				serve.Write(w, r, http.StatusOK, "Greeting", greeting{Text: "changed"})
			}},
			{Pattern: "DELETE /api/greeting", Action: ask.Action{Type: ask.Delete, System: true}, Handler: func(w http.ResponseWriter, r *http.Request) {
				serve.WriteError(w, r, http.StatusConflict, errors.New("the greeting is in use"))
			}},
		},
		UI:     http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "the page") }),
		Styles: styles,
	}
}

// serveHandler is a's handler on a machine of the test's own, whose token is token.
func serveHandler(t *testing.T, a serve.App, s serve.Site, hosts []string) (http.Handler, error) {
	t.Helper()
	writeToken(t, machine(t), token)
	return a.Handler(s, hosts)
}

// writeToken leaves value as the machine's token file, only the user's, as serve makes it.
func writeToken(t *testing.T, dir, value string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "token"), []byte(value+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func handler(t *testing.T, styles func() (map[ask.Type]ask.Style, error)) http.Handler {
	t.Helper()
	h, err := serveHandler(t, app, site(styles), []string{"tunnel.example"})
	if err != nil {
		t.Fatal(err)
	}
	return h
}

// call is one API request with the token, as a reader on this machine sends it.
func call(t *testing.T, h http.Handler, method, target, body string, header map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, "http://127.0.0.1:7480"+target, r)
	req.Header.Set("Authorization", "Bearer "+token)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range header {
		if v == "" {
			req.Header.Del(k)
			continue
		}
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

// envelope checks body against the contracts envelope and returns it.
func envelope(t *testing.T, body []byte) serve.Envelope {
	t.Helper()
	if err := contracts.Validate("envelope/v1/envelope.schema.json", body); err != nil {
		t.Fatalf("not a contracts envelope: %v\n%s", err, body)
	}
	var e serve.Envelope
	if err := json.Unmarshal(body, &e); err != nil {
		t.Fatal(err)
	}
	return e
}

// refusal checks an error answer: its status, its one Problem's type and reason, and where it ran.
func refusal(t *testing.T, w *httptest.ResponseRecorder, status int, typ, reason string) serve.Problem {
	t.Helper()
	if w.Code != status {
		t.Fatalf("status %d, want %d: %s", w.Code, status, w.Body)
	}
	e := envelope(t, w.Body.Bytes())
	if len(e.Errors) != 1 || e.Errors[0].Type != typ || e.Errors[0].Reason != reason {
		t.Fatalf("errors %+v, want one %s %s", e.Errors, typ, reason)
	}
	if e.Source != (serve.Source{Name: "shop", App: "designer", Version: "5.0.0", Contract: "v1"}) {
		t.Fatalf("ran %+v", e.Source)
	}
	return e.Errors[0]
}

// root is an app's root with serve and servers, answering the asking question yes-no.
func root(open func(*cobra.Command) (serve.Site, error)) *cobra.Command {
	r := &cobra.Command{Use: "e-designer", SilenceUsage: true, SilenceErrors: true}
	if open == nil {
		open = func(*cobra.Command) (serve.Site, error) { return site(nil), nil }
	}
	cmds, err := app.Commands(open, func(cmd *cobra.Command, a ask.Action) error {
		_, err := ask.Change(cmd, a, ask.YesNo)
		return err
	})
	if err != nil {
		panic(err)
	}
	r.AddCommand(cmds...)
	return r
}

func help(t *testing.T, args ...string) string {
	t.Helper()
	out, err := run(t, append(args, "--help")...)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// machine gives the test its own data folder and lets serve --detach start this test binary as the app.
func machine(t *testing.T) string {
	t.Helper()
	data := t.TempDir()
	t.Setenv("XDG_DATA_HOME", data)
	t.Setenv("SERVE_TEST_CHILD", "1")
	return filepath.Join(data, home.Org, "designer", "servers")
}

// run is one command of the app with no terminal, as a script or an agent runs it; it never runs past its deadline.
func run(t *testing.T, args ...string) (string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var out bytes.Buffer
	r := root(nil)
	r.SetOut(&out)
	r.SetErr(&out)
	r.SetIn(strings.NewReader(""))
	r.SetArgs(args)
	err := r.ExecuteContext(ctx)
	if ctx.Err() != nil {
		t.Fatalf("%v ran past its deadline", args)
	}
	return out.String(), err
}

// answer runs a command with -o json, checks it against the contracts envelope and, when schema is given, against that schema too.
func answer(t *testing.T, schema string, args ...string) serve.Envelope {
	t.Helper()
	out, err := run(t, append(args, "-o", "json")...)
	if err != nil {
		t.Fatalf("%v: %v\n%s", args, err, out)
	}
	if schema != "" {
		if err := contracts.Validate(schema, []byte(out)); err != nil {
			t.Fatalf("%v: not %s: %v\n%s", args, schema, err, out)
		}
	}
	return envelope(t, []byte(out))
}

// sameAsHTTP checks that a command's -o json answer is the bytes the HTTP door writes for the same thing, kind and data.
func sameAsHTTP(t *testing.T, name string, args ...string) {
	t.Helper()
	out, err := run(t, append(args, "-o", "json")...)
	if err != nil {
		t.Fatal(err)
	}
	var cli struct {
		Kind string          `json:"kind"`
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &cli); err != nil {
		t.Fatal(err)
	}
	// The HTTP door gets a machine of its own in a subtest, which gives the test's machine back when it ends.
	var w *httptest.ResponseRecorder
	t.Run("http", func(t *testing.T) {
		h, err := serveHandler(t, app, serve.Site{Name: name, Routes: []serve.Route{{Pattern: "GET /api/echo", Handler: func(w http.ResponseWriter, r *http.Request) {
			serve.Write(w, r, http.StatusOK, cli.Kind, cli.Data)
		}}}}, nil)
		if err != nil {
			t.Fatal(err)
		}
		w = call(t, h, "GET", "/api/echo", "", nil)
	})
	if w == nil {
		t.FailNow()
	}
	var a, b any
	_ = json.Unmarshal([]byte(out), &a)
	_ = json.Unmarshal(w.Body.Bytes(), &b)
	ja, _ := json.Marshal(a)
	jb, _ := json.Marshal(b)
	if !bytes.Equal(ja, jb) {
		t.Fatalf("CLI:\n%s\nHTTP:\n%s", out, w.Body)
	}
}

// detached starts a detached server and stops it when the test ends; it returns the server record from the answer.
func detached(t *testing.T) map[string]any {
	t.Helper()
	e := answer(t, "", "serve", "--detach")
	if e.Kind != serve.ServerKind {
		t.Fatalf("kind %q", e.Kind)
	}
	raw, _ := json.Marshal(e.Data)
	if err := contracts.Validate("mankit/v1/server.schema.json", raw); err != nil {
		t.Fatalf("not a contracts server record: %v\n%s", err, raw)
	}
	rec := e.Data.(map[string]any)
	t.Cleanup(func() { _, _ = run(t, "servers", "stop", "--all", "--yes") })
	return rec
}

func addrOf(rec map[string]any) string { return rec["metadata"].(map[string]any)["addr"].(string) }

// writeRecord leaves a record as a server would, with pid; the record need not be valid, so a test can break it.
func writeRecord(t *testing.T, dir, addr string, pid int) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	rec := map[string]any{
		"apiVersion": home.Org + "/mankit/server/v1", "kind": "Server",
		"metadata": map[string]any{"app": "designer", "addr": addr, "pid": pid},
		"spec":     map[string]any{"version": "5.0.0", "startedAt": "2026-11-11T13:00:00Z", "detached": true},
	}
	raw, _ := json.Marshal(rec)
	path := filepath.Join(dir, strings.NewReplacer(":", "-").Replace(addr)+".json")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// goneAddr is an address nothing listens on.
func goneAddr(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.NotFoundHandler())
	addr := srv.Listener.Addr().String()
	srv.Close()
	return addr
}

// syncBuffer is a buffer the server's goroutine writes while the test reads it.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

var _ io.Writer = (*syncBuffer)(nil)

func TestMain(m *testing.M) {
	// The child of serve --detach is this test binary started again: it runs the app's root, as a real app's binary would.
	if os.Getenv("SERVE_TEST_CHILD") == "1" && len(os.Args) > 1 && os.Args[1] == "serve" {
		if err := root(nil).Execute(); err != nil {
			os.Stderr.WriteString(err.Error() + "\n")
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}
