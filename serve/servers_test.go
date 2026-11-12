package serve_test

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/tugbayatilla/mankit/v2/serve"
)

// The servers command: every server of the app on this machine.

const listSchema = "mankit/v1/server-list.schema.json"

// Success

func TestServersListsADetachedServer(t *testing.T) {
	machine(t)
	addr := addrOf(detached(t))
	e := answer(t, listSchema, "servers")
	list, _ := e.Data.([]any)
	if e.Kind != serve.ServerListKind || len(list) != 1 {
		t.Fatalf("kind %q, data %v", e.Kind, e.Data)
	}
	s := list[0].(map[string]any)
	if s["answering"] != true || addrOf(s["record"].(map[string]any)) != addr {
		t.Fatalf("listed %v", s)
	}
	out, err := run(t, "servers")
	if err != nil || !strings.Contains(out, addr) || !strings.Contains(out, "shop") || !strings.Contains(out, "DETACHED") {
		t.Fatalf("table %q, %v", out, err)
	}
}

func TestServersRemovesARecordWhoseServerIsGone(t *testing.T) {
	dir := machine(t)
	writeRecord(t, dir, goneAddr(t), 999999)
	if list, _ := answer(t, listSchema, "servers").Data.([]any); len(list) != 0 {
		t.Fatalf("listed %v", list)
	}
}

func TestServersPrintsYAMLWithTheSameNames(t *testing.T) {
	machine(t)
	out, err := run(t, "servers", "-o", "yaml")
	if err != nil || !strings.Contains(out, "apiVersion: "+apiVersion) || !strings.Contains(out, "kind: ServerList") {
		t.Fatalf("%q, %v", out, err)
	}
}

// Contract

func TestServersAnswersTheContractAskedFor(t *testing.T) {
	machine(t)
	if e := answer(t, listSchema, "servers", "--contract", "v1"); e.Ran.Contract != "v1" || e.APIVersion != apiVersion {
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

// Errors and exit codes

func TestServersSaysWhichRecordItCannotRead(t *testing.T) {
	dir := machine(t)
	for name, pid := range map[string]int{"no pid": 0, "a group": -1} {
		t.Run(name, func(t *testing.T) {
			bad := writeRecord(t, dir, "127.0.0.1:1", pid)
			defer os.Remove(bad)
			if _, err := run(t, "servers"); err == nil || !strings.Contains(err.Error(), bad) {
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
	if _, err := run(t, "servers", "-o", "xml"); err == nil {
		t.Fatal("taken")
	}
}

// Asking

func TestServersNeverAsks(t *testing.T) {
	machine(t)
	if _, err := run(t, "servers"); err != nil {
		t.Fatalf("reading asked: %v", err)
	}
}

// CLI and HTTP parity

func TestServersAnswersLikeHTTP(t *testing.T) {
	machine(t)
	detached(t)
	sameAsHTTP(t, "", "servers")
}

// Help

func TestServersHelpNamesItsCommands(t *testing.T) {
	out := help(t, "servers")
	for _, want := range []string{"stop", "link", "--output", "--contract", "foreground"} {
		if !strings.Contains(out, want) {
			t.Errorf("servers --help has no %s:\n%s", want, out)
		}
	}
}
