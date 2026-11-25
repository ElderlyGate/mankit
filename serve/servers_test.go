package serve_test

import (
	"bytes"
	"context"
	"errors"
	"net"
	"os"
	"strings"
	"testing"

	"github.com/ElderlyGate/contracts"
	"github.com/spf13/cobra"

	"go.naywee.com/mankit/v5/helpout"
	"go.naywee.com/mankit/v5/helpout/helptest"
	"go.naywee.com/mankit/v5/problem"
	"go.naywee.com/mankit/v5/serve"
)

// The servers command: every server of the app on this machine.

const listSchema = "mankit/v1/server-list.schema.json"

// Success

func TestServersListsADetachedServer(t *testing.T) {
	machine(t)
	addr := addrOf(detached(t))
	e := answer(t, listSchema, "server", "list")
	list, _ := e.Data.([]any)
	if e.Kind != serve.ServerListKind || len(list) != 1 {
		t.Fatalf("kind %q, data %v", e.Kind, e.Data)
	}
	s := list[0].(map[string]any)
	if s["answering"] != true || addrOf(s["record"].(map[string]any)) != addr {
		t.Fatalf("listed %v", s)
	}
	out, err := run(t, "server", "list")
	if err != nil || !strings.Contains(out, addr) || !strings.Contains(out, "shop") || !strings.Contains(out, "DETACHED") {
		t.Fatalf("table %q, %v", out, err)
	}
}

// A server on every address records it as such, 0.0.0.0 or [::]; it is asked at loopback, which the guard takes, so it is listed as answering.
func TestServersFindsAServerOnEveryAddressAnswering(t *testing.T) {
	for _, every := range []string{"0.0.0.0", "::"} {
		t.Run(every, func(t *testing.T) {
			machine(t)
			e := answer(t, "", "serve", "--detach", "--addr", net.JoinHostPort(every, "0"), "--allow-remote")
			t.Cleanup(func() { _, _ = run(t, "server", "stop", "--all", "--yes") })
			list, _ := answer(t, listSchema, "server", "list").Data.([]any)
			if len(list) != 1 || list[0].(map[string]any)["answering"] != true {
				t.Fatalf("started %v, listed %v", e.Data, list)
			}
		})
	}
}

// A record on every address whose port nothing listens on, at either loopback, is removed as one gone.
func TestServersRemovesAnEveryAddressRecordWhoseServerIsGone(t *testing.T) {
	dir := machine(t)
	_, port, _ := net.SplitHostPort(goneAddr(t))
	writeRecord(t, dir, net.JoinHostPort("0.0.0.0", port), 999999)
	if list, _ := answer(t, listSchema, "server", "list").Data.([]any); len(list) != 0 {
		t.Fatalf("listed %v", list)
	}
}

func TestServersRemovesARecordWhoseServerIsGone(t *testing.T) {
	dir := machine(t)
	writeRecord(t, dir, goneAddr(t), 999999)
	if list, _ := answer(t, listSchema, "server", "list").Data.([]any); len(list) != 0 {
		t.Fatalf("listed %v", list)
	}
}

func TestServersPrintsYAMLWithTheSameNames(t *testing.T) {
	machine(t)
	out, err := run(t, "server", "list", "-o", "yaml")
	if err != nil || !strings.Contains(out, "apiVersion: "+apiVersion) || !strings.Contains(out, "kind: ServerList") {
		t.Fatalf("%q, %v", out, err)
	}
}

// Contract

func TestServersAnswersTheContractAskedFor(t *testing.T) {
	machine(t)
	if e := answer(t, listSchema, "server", "list", "--contract", "v1"); e.Source.Contract != "v1" || e.APIVersion != apiVersion {
		t.Fatalf("got %+v", e)
	}
}

func TestServersRefusesAContractItDoesNotAnswer(t *testing.T) {
	machine(t)
	_, err := run(t, "server", "list", "-o", "json", "--contract", "v9")
	var e *problem.Problem
	if !errors.As(err, &e) || e.Type != problem.TypeUnsupportedContract || !strings.Contains(e.Detail, "it answers v1") {
		t.Fatalf("got %v", err)
	}
}

// Errors and exit codes

func TestServersSaysWhichRecordItCannotRead(t *testing.T) {
	dir := machine(t)
	for name, pid := range map[string]int{"no pid": 0, "a group": -1} {
		t.Run(name, func(t *testing.T) {
			bad := writeRecord(t, dir, "127.0.0.1:1", pid)
			defer os.Remove(bad)
			if _, err := run(t, "server", "list"); err == nil || !strings.Contains(err.Error(), bad) {
				t.Fatalf("got %v", err)
			}
			if _, err := os.Stat(bad); err != nil {
				t.Fatal("the record it could not read is gone")
			}
		})
	}
}

func TestServersRefusesAnUnknownOutput(t *testing.T) {
	machine(t)
	if _, err := run(t, "server", "list", "-o", "xml"); err == nil {
		t.Fatal("taken")
	}
}

// Asking

func TestServersNeverAsks(t *testing.T) {
	machine(t)
	if _, err := run(t, "server", "list"); err != nil {
		t.Fatalf("reading asked: %v", err)
	}
}

// CLI and HTTP parity

func TestServersAnswersLikeHTTP(t *testing.T) {
	machine(t)
	detached(t)
	sameAsHTTP(t, "", "server", "list")
}

// Help

func TestServersHelpNamesItsCommands(t *testing.T) {
	out := help(t, "server")
	for _, want := range []string{"list", "stop", "link", "token"} {
		if !strings.Contains(out, want) {
			t.Errorf("server --help has no %s:\n%s", want, out)
		}
	}
	out = help(t, "server", "list")
	for _, want := range []string{"--output", "--contract", "foreground"} {
		if !strings.Contains(out, want) {
			t.Errorf("server list --help has no %s:\n%s", want, out)
		}
	}
}

// The old forms under servers still run, for one release, as the full forms under server do (G-STACK-5).
func TestTheOldServersFormsRunTheirFullForms(t *testing.T) {
	machine(t)
	helptest.Check(t, func() *cobra.Command {
		r := root(nil)
		helpout.Apply(r, helpout.Info{What: "a test app"})
		return r
	})
}

// help -o json, printed through the app's Printer, is contracts mankit/v1's Help in the app's envelope.
func TestHelpAsJSONIsTheHelpAnswer(t *testing.T) {
	r := root(nil)
	helpout.Apply(r, helpout.Info{What: "a test app", Start: []string{"e-designer serve"}, More: "https://github.com/nayweelabs/e-designer",
		Answer: func(c *cobra.Command) helpout.Answerer { return app.Printer(c) }})
	var out, errOut bytes.Buffer
	r.SetOut(&out)
	r.SetErr(&errOut)
	r.SetArgs([]string{"help", "-o", "json"})
	if code, err := helpout.Execute(context.Background(), r); code != 0 || err != nil {
		t.Fatalf("code %d, err %v, stderr %s", code, err, errOut.String())
	}
	if err := contracts.Validate("mankit/v1/help.schema.json", out.Bytes()); err != nil {
		t.Fatalf("not a Help answer: %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), `"words": "servers stop"`) || !strings.Contains(out.String(), `"now": "server stop"`) {
		t.Errorf("the old forms are not marked:\n%s", out.String())
	}
}
