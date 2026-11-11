package serve_test

import (
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/tugbayatilla/mankit/ask"
	"github.com/tugbayatilla/mankit/serve"
)

// The servers stop command: stop a server by its address or its name, or every one.

const stoppedSchema = "mankit/v1/stopped-list.schema.json"

// Success

func TestServersStopStopsTheServer(t *testing.T) {
	machine(t)
	addr := addrOf(detached(t))
	e := answer(t, stoppedSchema, "servers", "stop", addr, "--yes")
	got, _ := e.Data.([]any)
	if e.Kind != serve.StoppedKind || len(got) != 1 || got[0].(map[string]any)["stopped"] != true {
		t.Fatalf("stopped %v", e.Data)
	}
	if conn, err := net.DialTimeout("tcp", addr, time.Second); err == nil {
		conn.Close()
		t.Fatal("still listening")
	}
	if list, _ := answer(t, listSchema, "servers").Data.([]any); len(list) != 0 {
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

func TestServersStopRemovesTheRecordOfAServerAlreadyGone(t *testing.T) {
	dir := machine(t)
	gone := goneAddr(t)
	writeRecord(t, dir, gone, 999999)
	e := answer(t, stoppedSchema, "servers", "stop", gone, "--yes")
	if got, _ := e.Data.([]any); len(got) != 1 || got[0].(map[string]any)["stale"] != true {
		t.Fatalf("stopped %v", e.Data)
	}
}

// Contract

func TestServersStopRefusesAContractItDoesNotAnswer(t *testing.T) {
	machine(t)
	_, err := run(t, "servers", "stop", "--all", "--yes", "-o", "json", "--contract", "v9")
	var e *serve.Error
	if !errors.As(err, &e) || e.Code != serve.CodeUnsupportedContract {
		t.Fatalf("got %v", err)
	}
}

// Errors and exit codes

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

func TestServersStopSignalsNothingItCannotName(t *testing.T) {
	foreign := map[string]http.Handler{
		"another program": http.NotFoundHandler(),
		"another app": http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
			_ = serve.Encode(w, serve.App{ID: "kanban", Version: "1.0.0", Contracts: []string{"v1"}}.Failure("", "v1", serve.Fail(serve.CodeToken, "no token")))
		}),
	}
	for name, h := range foreign {
		t.Run(name, func(t *testing.T) {
			dir := machine(t)
			srv := httptest.NewServer(h)
			defer srv.Close()
			sleeper := exec.Command("sleep", "30")
			if err := sleeper.Start(); err != nil {
				t.Fatal(err)
			}
			defer func() { _ = sleeper.Process.Kill(); _ = sleeper.Wait() }()
			addr := srv.Listener.Addr().String()
			writeRecord(t, dir, addr, sleeper.Process.Pid)

			if _, err := run(t, "servers", "stop", addr, "--yes"); err == nil || !strings.Contains(err.Error(), "does not answer as e-designer") {
				t.Fatalf("got %v", err)
			}
			if err := sleeper.Process.Signal(syscall.Signal(0)); err != nil {
				t.Fatalf("the process behind the address was signalled: %v", err)
			}
		})
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
	if list, _ := answer(t, listSchema, "servers").Data.([]any); len(list) != 1 {
		t.Fatalf("stopped without a yes: %v", list)
	}
}

// CLI and HTTP parity

func TestServersStopAnswersLikeHTTP(t *testing.T) {
	dir := machine(t)
	writeRecord(t, dir, goneAddr(t), 999999)
	sameAsHTTP(t, "", "servers", "stop", "--all", "--yes")
}

// Help

func TestServersStopHelpNamesItsFlags(t *testing.T) {
	out := help(t, "servers", "stop")
	for _, want := range []string{"--all", "--yes", "--output", "--contract", "SIGTERM"} {
		if !strings.Contains(out, want) {
			t.Errorf("servers stop --help has no %s:\n%s", want, out)
		}
	}
}
