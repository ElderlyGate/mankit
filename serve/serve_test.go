package serve_test

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ElderlyGate/contracts"
	"github.com/spf13/cobra"

	"github.com/tugbayatilla/mankit/v3/features"
	"github.com/tugbayatilla/mankit/v3/serve"
)

// The serve command: in the foreground, or detached with --detach.

// Success

func TestServeDetachesAndLeavesARecord(t *testing.T) {
	dir := machine(t)
	rec := detached(t)
	spec := rec["spec"].(map[string]any)
	if spec["detached"] != true || spec["name"] != "shop" {
		t.Fatalf("spec %v", spec)
	}
	log, _ := spec["log"].(string)
	if filepath.Base(log) != strings.NewReplacer(":", "-").Replace(addrOf(rec))+".log" {
		t.Fatalf("the log %q is not named for the address bound", log)
	}
	if _, err := os.Stat(log); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(dir); err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("records folder %v, %v; want 0700", info.Mode(), err)
	}
}

func TestServeInTheForegroundLeavesARecordUntilItStops(t *testing.T) {
	dir := machine(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out := &syncBuffer{}
	r := root(nil)
	r.SetOut(out)
	r.SetArgs([]string{"serve"})
	done := make(chan error, 1)
	go func() { done <- r.ExecuteContext(ctx) }()

	var addr string
	for addr == "" && ctx.Err() == nil {
		time.Sleep(20 * time.Millisecond)
		if line, ok := strings.CutPrefix(out.String(), "e-designer serving on http://"); ok {
			addr, _, _ = strings.Cut(line, "\n")
		}
	}
	if strings.Contains(out.String(), token) || strings.Contains(out.String(), "?token=") {
		t.Fatalf("the link went to a stream that is not a terminal: %q", out.String())
	}
	if !strings.Contains(out.String(), "e-designer servers link "+addr) {
		t.Fatalf("it does not say where the link is: %q", out.String())
	}
	list, _ := answer(t, "mankit/v1/server-list.schema.json", "servers").Data.([]any)
	if len(list) != 1 || list[0].(map[string]any)["record"].(map[string]any)["spec"].(map[string]any)["detached"] != false {
		t.Fatalf("servers lists %v", list)
	}
	link := answer(t, "", "servers", "link", addr).Data.(map[string]any)["url"].(string)
	resp, err := http.Get(link)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if recs, _ := filepath.Glob(filepath.Join(dir, "*.json")); len(recs) != 0 {
		t.Fatalf("the record outlived the server: %v", recs)
	}
}

func TestServeLogsEachBrokenFeatureAtStartAndServes(t *testing.T) {
	machine(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out := &syncBuffer{}
	r := root(func(*cobra.Command) (serve.Site, error) {
		s := site(nil)
		s.Features = features.New([]features.Feature{{ID: "hear", Needs: []features.Value{{Name: "HEAR_URL", Purpose: "where changes go"}}}},
			features.Store{Off: func() ([]string, error) { return nil, nil }}).WithLookup(func(string) bool { return false })
		return s, nil
	})
	r.SetOut(out)
	r.SetErr(out)
	r.SetArgs([]string{"serve"})
	done := make(chan error, 1)
	go func() { done <- r.ExecuteContext(ctx) }()
	for !strings.Contains(out.String(), "serving on") && ctx.Err() == nil {
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if want := "e-designer: the feature hear needs values that are not set: HEAR_URL (where changes go); the rest serves\n"; !strings.HasPrefix(out.String(), want) {
		t.Fatalf("output %q, want it to start with %q", out.String(), want)
	}
}

func TestServeEndsALongAnswerWhenItStops(t *testing.T) {
	machine(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	waiting := make(chan struct{})
	open := func(*cobra.Command) (serve.Site, error) {
		s := site(nil)
		s.Routes = append(s.Routes, serve.Route{Pattern: "GET /api/wait", Handler: func(w http.ResponseWriter, r *http.Request) {
			close(waiting)
			<-serve.Stopping(r)
			serve.Write(w, r, http.StatusOK, "Greeting", greeting{Text: "bye"})
		}})
		return s, nil
	}
	out := &syncBuffer{}
	r := root(open)
	r.SetOut(out)
	r.SetArgs([]string{"serve"})
	done := make(chan error, 1)
	go func() { done <- r.ExecuteContext(ctx) }()
	var addr string
	for addr == "" && ctx.Err() == nil {
		time.Sleep(20 * time.Millisecond)
		if line, ok := strings.CutPrefix(out.String(), "e-designer serving on http://"); ok {
			addr, _, _ = strings.Cut(line, "\n")
		}
	}
	link, err := url.Parse(answer(t, "", "servers", "link", addr).Data.(map[string]any)["url"].(string))
	if err != nil {
		t.Fatal(err)
	}
	reqCtx, reqCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer reqCancel()
	req, _ := http.NewRequestWithContext(reqCtx, http.MethodGet, "http://"+addr+"/api/wait", nil)
	req.Header.Set("Authorization", "Bearer "+link.Query().Get("token"))
	status := make(chan int, 1)
	go func() {
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			status <- 0
			return
		}
		resp.Body.Close()
		status <- resp.StatusCode
	}()
	<-waiting
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("the server did not stop cleanly: %v", err)
	}
	if got := <-status; got != http.StatusOK {
		t.Fatalf("the long answer ended with status %d", got)
	}
}

// Contract

func TestServeDetachAnswersTheContractAskedFor(t *testing.T) {
	machine(t)
	e := answer(t, "", "serve", "--detach", "--contract", "v1")
	t.Cleanup(func() { _, _ = run(t, "servers", "stop", "--all", "--yes") })
	if e.Source.Contract != "v1" || e.APIVersion != apiVersion {
		t.Fatalf("got %+v", e)
	}
}

func TestServeRefusesAContractItDoesNotAnswerBeforeItStarts(t *testing.T) {
	dir := machine(t)
	_, err := run(t, "serve", "--detach", "-o", "json", "--contract", "v9")
	var e *serve.Problem
	if !errors.As(err, &e) || e.Type != serve.TypeUnsupportedContract {
		t.Fatalf("got %v", err)
	}
	if recs, _ := filepath.Glob(filepath.Join(dir, "*.json")); len(recs) != 0 {
		t.Fatalf("it started anyway: %v", recs)
	}
}

// Errors and exit codes

func TestServeRefusesToStartWithoutAValueTheAppCannotRunWithout(t *testing.T) {
	dir := machine(t)
	r := root(func(*cobra.Command) (serve.Site, error) {
		s := site(nil)
		s.Needs = []features.Value{{Name: "SHOP_DIR", Purpose: "where the shop lives"}}
		return s, nil
	})
	r.SetOut(&syncBuffer{})
	r.SetErr(&syncBuffer{})
	r.SetArgs([]string{"serve"})
	err := r.Execute()
	var e *serve.Problem
	if !errors.As(err, &e) || e.Type != serve.TypeFailed || e.Reason != serve.ReasonCoreNeedsValues || !strings.Contains(e.Detail, "SHOP_DIR (where the shop lives)") {
		t.Fatalf("got %v, want core_needs_values naming SHOP_DIR", err)
	}
	if recs, _ := filepath.Glob(filepath.Join(dir, "*.json")); len(recs) != 0 {
		t.Fatalf("it started anyway: %v", recs)
	}
}

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

func TestServeDetachStopsAtOnceOnARecordItCannotRead(t *testing.T) {
	dir := machine(t)
	bad := writeRecord(t, dir, "127.0.0.1:1", 0)
	start := time.Now()
	if _, err := run(t, "serve", "--detach"); err == nil || !strings.Contains(err.Error(), bad) {
		t.Fatalf("got %v", err)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatalf("it waited %s", time.Since(start))
	}
}

func TestServeRefusesATokenOthersCanReach(t *testing.T) {
	for name, plant := range map[string]func(t *testing.T, path string){
		"open to others": func(t *testing.T, path string) {
			if err := os.WriteFile(path, []byte("t\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(path, 0o644); err != nil {
				t.Fatal(err)
			}
		},
		"a link": func(t *testing.T, path string) {
			real := filepath.Join(t.TempDir(), "elsewhere")
			if err := os.WriteFile(real, []byte("t\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(real, path); err != nil {
				t.Fatal(err)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			dir := machine(t)
			if err := os.MkdirAll(dir, 0o700); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, "token")
			plant(t, path)
			for _, args := range [][]string{{"serve"}, {"servers", "link"}} {
				if _, err := run(t, args...); err == nil || !strings.Contains(err.Error(), path) {
					t.Errorf("%v: got %v, want a refusal naming %s", args, err, path)
				}
			}
		})
	}
}

func TestCommandsRefusesAnAppItCannotServe(t *testing.T) {
	open := func(*cobra.Command) (serve.Site, error) { return serve.Site{}, nil }
	if _, err := (serve.App{ID: "designer", Version: "1.0.0"}).Commands(open, nil); err == nil {
		t.Fatal("an app without contracts got commands")
	}
	if _, err := app.Commands(open, nil); err == nil {
		t.Fatal("commands without confirm were made")
	}
}

// Asking

func TestServeNeverAsks(t *testing.T) {
	machine(t)
	// No terminal and no --yes: starting a server changes no served thing, so it asks nothing.
	if _, err := run(t, "serve", "--detach"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = run(t, "servers", "stop", "--all", "--yes") })
}

// CLI and HTTP parity

func TestServeDetachAnswersLikeHTTP(t *testing.T) {
	machine(t)
	out, err := run(t, "serve", "--detach", "-o", "json")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = run(t, "servers", "stop", "--all", "--yes") })
	var e serve.Envelope
	if err := json.Unmarshal([]byte(out), &e); err != nil {
		t.Fatal(err)
	}
	if e.Source != (serve.Source{Name: "shop", App: "designer", Version: "5.0.0", Contract: "v1"}) {
		t.Fatalf("ran %+v", e.Source)
	}
	raw, _ := json.Marshal(e.Data)
	if err := contracts.Validate("mankit/v1/server.schema.json", raw); err != nil {
		t.Fatal(err)
	}
}

// Help

func TestServeHelpNamesItsFlags(t *testing.T) {
	out := help(t, "serve")
	for _, want := range []string{"--detach", "--addr", "--host", "--allow-remote", "--output", "--contract"} {
		if !strings.Contains(out, want) {
			t.Errorf("serve --help has no %s:\n%s", want, out)
		}
	}
}
