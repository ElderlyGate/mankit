package serve_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ElderlyGate/contracts"
	"github.com/tugbayatilla/mankit/v3/helpout"
	"github.com/tugbayatilla/mankit/v3/serve"
)

// servers --check: the list of servers, and a failure unless at least one server is listed and every one answers, said in the answer's errors.

func TestServersCheckPassesWhenEveryServerAnswers(t *testing.T) {
	machine(t)
	detached(t)
	if out, err := run(t, "servers", "--check"); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if e := answer(t, listSchema, "servers", "--check"); len(e.Errors) != 0 {
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

	out, err := run(t, "servers", "--check")
	why := "1 of 2 servers do not answer: " + other.Listener.Addr().String()
	if !errors.As(err, new(helpout.Printed)) || strings.Count(out, why) != 1 {
		t.Fatalf("table: %v\n%s", err, out)
	}
	var p *serve.Problem
	if !errors.As(err, &p) || p.Type != serve.TypeNotFound {
		t.Fatalf("not a not_found problem: %v", err)
	}
}

// With -o json the answer is one envelope: the same list as servers, and the reason in its errors.
func TestServersCheckAnswersOneEnvelopeWithItsReason(t *testing.T) {
	dir := machine(t)
	other := httptest.NewServer(http.NotFoundHandler())
	defer other.Close()
	writeRecord(t, dir, other.Listener.Addr().String(), 999999)

	out, err := run(t, "servers", "--check", "-o", "json")
	if !errors.As(err, new(helpout.Printed)) {
		t.Fatalf("got %v", err)
	}
	if err := contracts.Validate(listSchema, []byte(out)); err != nil {
		t.Fatalf("not %s: %v\n%s", listSchema, err, out)
	}
	dec := json.NewDecoder(strings.NewReader(out))
	var checked, second struct {
		Kind string
		Data json.RawMessage
	}
	if dec.Decode(&checked) != nil || dec.Decode(&second) == nil {
		t.Fatalf("not exactly one envelope:\n%s", out)
	}
	plain := answer(t, listSchema, "servers")
	data, _ := json.Marshal(plain.Data)
	var same, was any
	_ = json.Unmarshal(checked.Data, &same)
	_ = json.Unmarshal(data, &was)
	sameJSON, _ := json.Marshal(same)
	wasJSON, _ := json.Marshal(was)
	if checked.Kind != serve.ServerListKind || string(sameJSON) != string(wasJSON) {
		t.Fatalf("kind %q, data differs from servers:\n%s\n%s", checked.Kind, sameJSON, wasJSON)
	}
	if e := envelope(t, []byte(out)); len(e.Errors) != 1 || e.Errors[0].Type != serve.TypeNotFound || !strings.Contains(e.Errors[0].Detail, "do not answer") {
		t.Fatalf("errors %+v", e.Errors)
	}
}

func TestServersCheckFailsWhenNoServerIsListed(t *testing.T) {
	machine(t)
	out, err := run(t, "servers", "--check")
	if !errors.As(err, new(helpout.Printed)) || strings.Count(out, "no server is listed") != 1 {
		t.Fatalf("%v\n%s", err, out)
	}
}
