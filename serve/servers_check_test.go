package serve_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ElderlyGate/contracts"
	"github.com/tugbayatilla/mankit/v3/serve"
)

// servers --check: the answer of servers, then a failing exit unless at least one server is listed and every one answers.

func TestServersCheckPassesWhenEveryServerAnswers(t *testing.T) {
	machine(t)
	detached(t)
	if out, err := run(t, "servers", "--check"); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
}

func TestServersCheckFailsWhenAServerDoesNotAnswer(t *testing.T) {
	dir := machine(t)
	detached(t)
	// Something listens there, so the record stays, but it is not this app.
	other := httptest.NewServer(http.NotFoundHandler())
	defer other.Close()
	writeRecord(t, dir, other.Listener.Addr().String(), 999999)

	out, err := run(t, "servers", "--check")
	var said serve.AlreadyAnswered
	if !errors.As(err, &said) || !strings.Contains(out, "1 of 2 servers do not answer: "+other.Listener.Addr().String()) {
		t.Fatalf("table: %v\n%s", err, out)
	}
	var p *serve.Problem
	if !errors.As(err, &p) || p.Type != serve.TypeFailed {
		t.Fatalf("not a failed problem: %v", err)
	}
}

// With -o json the answer is one envelope, the same as servers gives, and the failing exit adds nothing to it.
func TestServersCheckAnswersOneEnvelope(t *testing.T) {
	dir := machine(t)
	other := httptest.NewServer(http.NotFoundHandler())
	defer other.Close()
	writeRecord(t, dir, other.Listener.Addr().String(), 999999)

	out, err := run(t, "servers", "--check", "-o", "json")
	var said serve.AlreadyAnswered
	if !errors.As(err, &said) {
		t.Fatalf("got %v", err)
	}
	if err := contracts.Validate(listSchema, []byte(out)); err != nil {
		t.Fatalf("not %s: %v\n%s", listSchema, err, out)
	}
	dec := json.NewDecoder(strings.NewReader(out))
	var first, second json.RawMessage
	if dec.Decode(&first) != nil || dec.Decode(&second) == nil {
		t.Fatalf("not exactly one envelope:\n%s", out)
	}
	plain, err := run(t, "servers", "-o", "json")
	if err != nil || !bytes.Equal([]byte(plain), []byte(out)) {
		t.Fatalf("differs from servers: %v\n%s\n%s", err, plain, out)
	}
}

func TestServersCheckFailsWhenNoServerIsListed(t *testing.T) {
	machine(t)
	out, err := run(t, "servers", "--check")
	var said serve.AlreadyAnswered
	if !errors.As(err, &said) || !strings.Contains(out, "no server is listed") {
		t.Fatalf("%v\n%s", err, out)
	}
}
