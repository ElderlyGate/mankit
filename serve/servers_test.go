package serve_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ElderlyGate/contracts"
	"github.com/spf13/cobra"

	"github.com/tugbayatilla/mankit/ask"
	"github.com/tugbayatilla/mankit/serve"
)

// machine gives the test its own data folder and lets serve --detach start this test binary as the app.
func machine(t *testing.T) string {
	t.Helper()
	data := t.TempDir()
	t.Setenv("XDG_DATA_HOME", data)
	t.Setenv("SERVE_TEST_CHILD", "1")
	return filepath.Join(data, "elderlygate", "designer", "servers")
}

// run is one command of the app with no terminal, as a script or an agent runs it.
func run(t *testing.T, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	r := root(func(*cobra.Command) (serve.Site, error) { return site(nil), nil })
	r.SetOut(&out)
	r.SetErr(&out)
	r.SetIn(strings.NewReader(""))
	r.SetArgs(args)
	err := r.Execute()
	return out.String(), err
}

// answer runs a command with -o json and checks its answer against the contracts envelope.
func answer(t *testing.T, args ...string) serve.Envelope {
	t.Helper()
	out, err := run(t, append(args, "-o", "json")...)
	if err != nil {
		t.Fatalf("%v: %v\n%s", args, err, out)
	}
	return envelope(t, []byte(out))
}

// detached starts a detached server and stops it when the test ends; it returns the server record from the answer.
func detached(t *testing.T) map[string]any {
	t.Helper()
	e := answer(t, "serve", "--detach")
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

// Success

func TestServeDetachesAndServersListsIt(t *testing.T) {
	machine(t)
	addr := addrOf(detached(t))

	e := answer(t, "servers")
	list, _ := e.Data.([]any)
	if e.Kind != serve.ServerListKind || len(list) != 1 {
		t.Fatalf("kind %q, data %v", e.Kind, e.Data)
	}
	s := list[0].(map[string]any)
	if s["answering"] != true || addrOf(s["record"].(map[string]any)) != addr {
		t.Fatalf("listed %v", s)
	}
	out, err := run(t, "servers")
	if err != nil || !strings.Contains(out, addr) || !strings.Contains(out, "shop") {
		t.Fatalf("table %q, %v", out, err)
	}
}

func TestServersLinkOpensThePage(t *testing.T) {
	machine(t)
	addr := addrOf(detached(t))
	e := answer(t, "servers", "link", addr)
	link := e.Data.(map[string]any)["url"].(string)
	if e.Kind != serve.LinkKind || !strings.HasPrefix(link, "http://"+addr+"/?token=") {
		t.Fatalf("link %q", link)
	}
	resp, err := http.Get(link)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || len(resp.Cookies()) != 1 {
		t.Fatalf("status %d, cookies %v", resp.StatusCode, resp.Cookies())
	}
}

func TestServersStopStopsTheServer(t *testing.T) {
	machine(t)
	addr := addrOf(detached(t))
	e := answer(t, "servers", "stop", addr, "--yes")
	got, _ := e.Data.([]any)
	if e.Kind != serve.StoppedKind || len(got) != 1 || got[0].(map[string]any)["stopped"] != true {
		t.Fatalf("stopped %v", e.Data)
	}
	if conn, err := net.DialTimeout("tcp", addr, time.Second); err == nil {
		conn.Close()
		t.Fatal("still listening")
	}
	if list, _ := answer(t, "servers").Data.([]any); len(list) != 0 {
		t.Fatalf("still listed: %v", list)
	}
}

func TestServersStopFindsAServerByItsName(t *testing.T) {
	machine(t)
	detached(t)
	if _, err := run(t, "servers", "stop", "shop", "--yes"); err != nil {
		t.Fatal(err)
	}
}

func TestServeInTheForegroundPrintsItsLinkAndStopsWhenAsked(t *testing.T) {
	dir := machine(t)
	ctx, cancel := context.WithCancel(context.Background())
	out := &syncBuffer{}
	r := root(func(*cobra.Command) (serve.Site, error) { return site(nil), nil })
	r.SetOut(out)
	r.SetArgs([]string{"serve"})
	done := make(chan error, 1)
	go func() { done <- r.ExecuteContext(ctx) }()

	var link string
	for deadline := time.Now().Add(5 * time.Second); link == "" && time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		for _, line := range strings.Split(out.String(), "\n") {
			if after, ok := strings.CutPrefix(line, "open: "); ok {
				link = after
			}
		}
	}
	if link == "" {
		cancel()
		t.Fatalf("no link printed: %q", out.String())
	}
	resp, err := http.Get(link)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if recs, _ := filepath.Glob(filepath.Join(dir, "*.json")); len(recs) != 0 {
		t.Fatalf("a foreground server wrote a record: %v", recs)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

// Contract

func TestServersAnswersTheContractAskedFor(t *testing.T) {
	machine(t)
	if e := answer(t, "servers", "--contract", "v1"); e.Ran.Contract != "v1" || e.APIVersion != "elderlygate/designer/app/v1" {
		t.Fatalf("got %+v", e)
	}
}

func TestServersRefusesAContractItDoesNotAnswer(t *testing.T) {
	machine(t)
	_, err := run(t, "servers", "-o", "json", "--contract", "v9")
	var e *serve.Error
	if !errors.As(err, &e) || e.Code != serve.CodeUnsupportedContract || strings.Join(e.Supported, ",") != "v1" {
		t.Fatalf("got %v", err)
	}
}

func TestServersPrintsYAMLWithTheSameNames(t *testing.T) {
	machine(t)
	out, err := run(t, "servers", "-o", "yaml")
	if err != nil || !strings.Contains(out, "apiVersion: elderlygate/designer/app/v1") || !strings.Contains(out, "kind: ServerList") {
		t.Fatalf("%q, %v", out, err)
	}
}

// Errors and exit codes

func TestServeRefusesAnAddressOtherMachinesReach(t *testing.T) {
	machine(t)
	if _, err := run(t, "serve", "--addr", "0.0.0.0:0"); err == nil || !strings.Contains(err.Error(), "--allow-remote") {
		t.Fatalf("got %v", err)
	}
}

func TestServeDetachSaysWhyTheServerDidNotStart(t *testing.T) {
	machine(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	if _, err := run(t, "serve", "--detach", "--addr", ln.Addr().String()); err == nil || !strings.Contains(err.Error(), "cannot listen") {
		t.Fatalf("got %v", err)
	}
}

func TestServersStopNeedsATarget(t *testing.T) {
	machine(t)
	for _, args := range [][]string{{"servers", "stop", "--yes"}, {"servers", "stop", "x", "--all", "--yes"}} {
		if _, err := run(t, args...); err == nil {
			t.Errorf("%v: taken", args)
		}
	}
	if _, err := run(t, "servers", "stop", "nowhere", "--yes"); err == nil || !strings.Contains(err.Error(), "e-designer servers") {
		t.Fatalf("got %v", err)
	}
}

func TestServersRemovesARecordWhoseServerIsGone(t *testing.T) {
	dir := machine(t)
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	gone := ln.Addr().String()
	ln.Close()
	writeRecord(t, dir, gone)

	if list, _ := answer(t, "servers").Data.([]any); len(list) != 0 {
		t.Fatalf("listed %v", list)
	}
	writeRecord(t, dir, gone)
	e := answer(t, "servers", "stop", gone, "--yes")
	if got, _ := e.Data.([]any); len(got) != 1 || got[0].(map[string]any)["stale"] != true {
		t.Fatalf("stopped %v", e.Data)
	}
}

func TestServersSaysWhichRecordItCannotRead(t *testing.T) {
	dir := machine(t)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	bad := filepath.Join(dir, "127.0.0.1-1.json")
	if err := os.WriteFile(bad, []byte(`{"addr":"127.0.0.1:1"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := run(t, "servers"); err == nil || !strings.Contains(err.Error(), bad) {
		t.Fatalf("got %v", err)
	}
	if _, err := os.Stat(bad); err != nil {
		t.Fatal("the record it could not read is gone")
	}
}

func TestServersLinkNeedsAToken(t *testing.T) {
	machine(t)
	if _, err := run(t, "servers", "link"); err == nil || !strings.Contains(err.Error(), "no server token") {
		t.Fatalf("got %v", err)
	}
}

func TestServersRefusesAnUnknownOutput(t *testing.T) {
	machine(t)
	if _, err := run(t, "servers", "-o", "xml"); err == nil {
		t.Fatal("taken")
	}
}

// Asking

func TestServersStopAsksFirstAndIsRefusedWithoutATerminal(t *testing.T) {
	machine(t)
	addr := addrOf(detached(t))
	_, err := run(t, "servers", "stop", addr)
	if !errors.Is(err, ask.ErrRefused) || !strings.Contains(err.Error(), "stop the e-designer server at "+addr) {
		t.Fatalf("got %v", err)
	}
	if list, _ := answer(t, "servers").Data.([]any); len(list) != 1 {
		t.Fatalf("stopped without a yes: %v", list)
	}
}

// CLI and HTTP parity

func TestServersAnswerSaysWhereItRanLikeHTTP(t *testing.T) {
	machine(t)
	e := answer(t, "servers")
	want := serve.Ran{Name: "designer", App: "designer", Version: "5.0.0", Contract: "v1"}
	if e.Ran != want {
		t.Fatalf("ran %+v, want %+v", e.Ran, want)
	}
}

// Help

func TestServersHelpNamesItsCommands(t *testing.T) {
	out := help(t, "servers")
	for _, want := range []string{"stop", "link", "--output", "--contract"} {
		if !strings.Contains(out, want) {
			t.Errorf("servers --help has no %s:\n%s", want, out)
		}
	}
	stop := help(t, "servers", "stop")
	for _, want := range []string{"--all", "--yes"} {
		if !strings.Contains(stop, want) {
			t.Errorf("servers stop --help has no %s:\n%s", want, stop)
		}
	}
	if !strings.Contains(help(t, "servers", "link"), "127.0.0.1:0") {
		t.Error("servers link --help does not name its default address")
	}
}

func writeRecord(t *testing.T, dir, addr string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	rec := map[string]any{
		"apiVersion": "elderlygate/mankit/server/v1", "kind": "Server",
		"metadata": map[string]any{"app": "designer", "addr": addr, "pid": 999999},
		"spec":     map[string]any{"version": "5.0.0", "startedAt": "2026-11-11T13:00:00Z"},
	}
	raw, _ := json.Marshal(rec)
	if err := contracts.Validate("mankit/v1/server.schema.json", raw); err != nil {
		t.Fatal(err)
	}
	name := strings.NewReplacer(":", "-").Replace(addr) + ".json"
	if err := os.WriteFile(filepath.Join(dir, name), raw, 0o600); err != nil {
		t.Fatal(err)
	}
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
