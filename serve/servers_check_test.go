package serve_test

import (
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/ElderlyGate/contracts"

	"github.com/tugbayatilla/mankit/v4/helpout"
	"github.com/tugbayatilla/mankit/v4/problem"
	"github.com/tugbayatilla/mankit/v4/serve"
)

// server list --check: the list of servers, and a failure unless at least one server is listed and every one answers, said in the answer's errors.

func TestServersCheckPassesWhenEveryServerAnswers(t *testing.T) {
	machine(t)
	detached(t)
	if out, err := run(t, "server", "list", "--check"); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if e := answer(t, listSchema, "server", "list", "--check"); len(e.Errors) != 0 {
		t.Fatalf("errors %v", e.Errors)
	}
}

// In a table the reason is said once, on standard error, and the failure is one the runner prints nothing more for.
func TestServersCheckFailsWhenAServerDoesNotAnswer(t *testing.T) {
	dir := machine(t)
	detached(t)
	// Something listens there, so the record stays, but it is not this app.
	other := httptest.NewServer(http.NotFoundHandler())
	defer other.Close()
	writeRecord(t, dir, other.Listener.Addr().String(), 999999)

	out, err := run(t, "server", "list", "--check")
	why := "1 of 2 servers do not answer: " + other.Listener.Addr().String()
	if !errors.As(err, new(helpout.Printed)) || strings.Count(out, why) != 1 {
		t.Fatalf("table: %v\n%s", err, out)
	}
	var p *problem.Problem
	if !errors.As(err, &p) || p.Type != problem.TypeNotFound || p.Reason != serve.ReasonNotAnswering {
		t.Fatalf("not a not_found problem for not_answering: %v", err)
	}
}

// With -o json the answer is one envelope: the same list as servers, and the reason in its errors.
func TestServersCheckAnswersOneEnvelopeWithItsReason(t *testing.T) {
	dir := machine(t)
	other := httptest.NewServer(http.NotFoundHandler())
	defer other.Close()
	writeRecord(t, dir, other.Listener.Addr().String(), 999999)

	out, err := run(t, "server", "list", "--check", "-o", "json")
	if !errors.As(err, new(helpout.Printed)) {
		t.Fatalf("got %v", err)
	}
	if err := contracts.Validate(listSchema, []byte(out)); err != nil {
		t.Fatalf("not %s: %v\n%s", listSchema, err, out)
	}
	dec := json.NewDecoder(strings.NewReader(out))
	var checked, second struct {
		Kind   string
		Data   json.RawMessage
		Errors []struct{ Code, Message string }
	}
	if dec.Decode(&checked) != nil || dec.Decode(&second) == nil {
		t.Fatalf("not exactly one envelope:\n%s", out)
	}
	plain := answer(t, listSchema, "server", "list")
	data, _ := json.Marshal(plain.Data)
	var same, was any
	_ = json.Unmarshal(checked.Data, &same)
	_ = json.Unmarshal(data, &was)
	sameJSON, _ := json.Marshal(same)
	wasJSON, _ := json.Marshal(was)
	if checked.Kind != serve.ServerListKind || string(sameJSON) != string(wasJSON) {
		t.Fatalf("kind %q, data differs from servers:\n%s\n%s", checked.Kind, sameJSON, wasJSON)
	}
	// envelope/v1 writes the reason as error/v1's code, the word a program decides by.
	if len(checked.Errors) != 1 || checked.Errors[0].Code != serve.ReasonNotAnswering || !strings.Contains(checked.Errors[0].Message, "do not answer") {
		t.Fatalf("errors %+v", checked.Errors)
	}
}

func TestServersCheckFailsWhenNoServerIsListed(t *testing.T) {
	machine(t)
	out, err := run(t, "server", "list", "--check")
	var p *problem.Problem
	if !errors.As(err, new(helpout.Printed)) || strings.Count(out, "no server is listed") != 1 || !errors.As(err, &p) || p.Type != problem.TypeNotFound || p.Reason != serve.ReasonNoneListed {
		t.Fatalf("%v\n%s", err, out)
	}
	out, _ = run(t, "server", "list", "--check", "-o", "json")
	var said struct{ Errors []struct{ Code string } }
	if json.Unmarshal([]byte(out), &said) != nil || len(said.Errors) != 1 || said.Errors[0].Code != serve.ReasonNoneListed {
		t.Fatalf("json: %s", out)
	}
}

// server list --check <addr>: only that server is asked, and no record is removed, as a container's health check shares the records of servers it cannot reach.

func TestServersCheckAnAddressPassesWhenItAnswers(t *testing.T) {
	machine(t)
	addr := addrOf(detached(t))
	e := answer(t, listSchema, "server", "list", "--check", addr)
	if list, _ := e.Data.([]any); len(e.Errors) != 0 || len(list) != 1 || list[0].(map[string]any)["answering"] != true {
		t.Fatalf("got %+v", e)
	}
}

// A server on every address is checked at loopback, and an address no record names still passes while it answers.
func TestServersCheckAnAddressAtLoopback(t *testing.T) {
	machine(t)
	e := answer(t, "", "serve", "--detach", "--addr", "0.0.0.0:0", "--allow-remote")
	t.Cleanup(func() { _, _ = run(t, "server", "stop", "--all", "--yes") })
	_, port, _ := net.SplitHostPort(addrOf(e.Data.(map[string]any)))
	if out, err := run(t, "server", "list", "--check", net.JoinHostPort("127.0.0.1", port)); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
}

// One that does not answer fails with not_answering, in one envelope, and every record stays, its own and the others'.
func TestServersCheckAnAddressFailsAndRemovesNothing(t *testing.T) {
	dir := machine(t)
	gone := goneAddr(t)
	mine := writeRecord(t, dir, gone, 999999)
	other := writeRecord(t, dir, goneAddr(t), 999998)

	out, err := run(t, "server", "list", "--check", gone, "-o", "json")
	if !errors.As(err, new(helpout.Printed)) {
		t.Fatalf("got %v\n%s", err, out)
	}
	if err := contracts.Validate(listSchema, []byte(out)); err != nil {
		t.Fatalf("not %s: %v\n%s", listSchema, err, out)
	}
	dec := json.NewDecoder(strings.NewReader(out))
	var said, second struct {
		Data   []map[string]any
		Errors []struct{ Code string }
	}
	if dec.Decode(&said) != nil || dec.Decode(&second) == nil {
		t.Fatalf("not exactly one envelope:\n%s", out)
	}
	if len(said.Data) != 1 || said.Data[0]["answering"] != false || len(said.Errors) != 1 || said.Errors[0].Code != serve.ReasonNotAnswering {
		t.Fatalf("got %+v", said)
	}
	for _, f := range []string{mine, other} {
		if _, err := os.Stat(f); err != nil {
			t.Errorf("a record was removed: %v", err)
		}
	}
	var p *problem.Problem
	if _, err := run(t, "server", "list", "--check", gone); !errors.As(err, &p) || p.Type != problem.TypeNotFound || p.Reason != serve.ReasonNotAnswering {
		t.Fatalf("table: %v", err)
	}
}

// An address is taken only with --check.
func TestServersTakesAnAddressOnlyWithCheck(t *testing.T) {
	machine(t)
	for _, args := range [][]string{{"server", "list", "127.0.0.1:7799"}, {"server", "list", "--check=false", "127.0.0.1:7799"}} {
		if out, err := run(t, args...); err == nil || !strings.Contains(out+err.Error(), "only with --check") {
			t.Errorf("%v: %v\n%s", args, err, out)
		}
	}
	// A typo is a usage mistake, never a server reported as not answering.
	for _, addr := range []string{"5173", "not an address", "127.0.0.1:"} {
		out, err := run(t, "server", "list", "--check", addr)
		if err == nil || errors.As(err, new(helpout.Printed)) || !strings.Contains(out+err.Error(), "is not an address") {
			t.Errorf("%q: %v\n%s", addr, err, out)
		}
	}
}
