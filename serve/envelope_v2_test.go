package serve_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/ElderlyGate/contracts"

	"github.com/tugbayatilla/mankit/v3/serve"
)

// moved is designer answering v1 in envelope/v1 and v2 in envelope/v2, as an app that moved with its next contract.
var moved = serve.App{ID: "designer", Command: "e-designer", Version: "6.0.0", Contracts: []string{"v1", "v2"}, Addr: "127.0.0.1:0", EnvelopeV2From: "v2"}

func movedHandler(t *testing.T) http.Handler {
	t.Helper()
	h, err := serveHandler(t, moved, site(nil), nil)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func TestAnAppAnswersEachContractInItsOwnEnvelope(t *testing.T) {
	h := movedHandler(t)
	for contract, schema := range map[string]string{"v1": "envelope/v1/envelope.schema.json", "v2": "envelope/v2/envelope.schema.json"} {
		for _, c := range []struct{ method, target string }{{"GET", "/api/greeting"}, {"DELETE", "/api/greeting?confirm=yes"}, {"GET", "/api/nothing"}} {
			body := call(t, h, c.method, c.target, "", map[string]string{serve.ContractHeader: contract}).Body.Bytes()
			if err := contracts.Validate(schema, body); err != nil {
				t.Errorf("%s %s in %s: not %s: %v\n%s", c.method, c.target, contract, schema, err, body)
			}
		}
	}
}

func TestAProblemMeetsProblemV1(t *testing.T) {
	body := call(t, movedHandler(t), "GET", "/api/nothing", "", nil).Body.Bytes()
	var e struct {
		Errors []json.RawMessage `json:"errors"`
	}
	if err := json.Unmarshal(body, &e); err != nil || len(e.Errors) != 1 {
		t.Fatalf("errors %s: %v", body, err)
	}
	if err := contracts.Validate("problem/v1/problem.schema.json", e.Errors[0]); err != nil {
		t.Fatalf("not a Problem: %v\n%s", err, e.Errors[0])
	}
}

// A refusal of the guard is in the contract the reader asked for when the app serves it, so a v1 reader can read it (CT-RULE-1).
func TestTheGuardRefusesInTheContractAskedFor(t *testing.T) {
	h := movedHandler(t)
	for asked, schema := range map[string]string{"v1": "envelope/v1/envelope.schema.json", "v2": "envelope/v2/envelope.schema.json", "": "envelope/v2/envelope.schema.json", "v9": "envelope/v2/envelope.schema.json"} {
		w := call(t, h, "GET", "/api/greeting", "", map[string]string{"Authorization": "Bearer wrong", serve.ContractHeader: asked})
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("asked %q: status %d", asked, w.Code)
		}
		if err := contracts.Validate(schema, w.Body.Bytes()); err != nil {
			t.Errorf("asked %q: not %s: %v\n%s", asked, schema, err, w.Body)
		}
		var e serve.Envelope
		if err := json.Unmarshal(w.Body.Bytes(), &e); err != nil || len(e.Errors) != 1 || e.Errors[0].Type != serve.TypeRefused || e.Errors[0].Reason != serve.ReasonToken {
			t.Errorf("asked %q: %+v, %v", asked, e.Errors, err)
		}
	}
}

// servers finds this app on a port whether its token refusal is in envelope/v1 (an app on mankit v2) or envelope/v2.
func TestServersFindsTheAppInEitherEnvelope(t *testing.T) {
	v1, err := os.ReadFile(filepath.Join("testdata", "v1-golden", "token_refused.json"))
	if err != nil {
		t.Fatal(err)
	}
	v2, err := json.Marshal(serve.App{ID: "designer", Version: "5.0.0", Contracts: []string{"v2"}, EnvelopeV2From: "v2"}.Failure("shop", "v2", serve.Fail(serve.TypeRefused, serve.ReasonToken, "no token")))
	if err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string][]byte{"envelope/v1": v1, "envelope/v2": v2} {
		t.Run(name, func(t *testing.T) {
			dir := machine(t)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write(body)
			}))
			defer srv.Close()
			sleeper := exec.Command("sleep", "30")
			if err := sleeper.Start(); err != nil {
				t.Fatal(err)
			}
			defer func() { _ = sleeper.Process.Kill(); _ = sleeper.Wait() }()
			writeRecord(t, dir, srv.Listener.Addr().String(), sleeper.Process.Pid)
			e := answer(t, listSchema, "servers")
			list, _ := e.Data.([]any)
			if len(list) != 1 || list[0].(map[string]any)["answering"] != true {
				t.Fatalf("listed %v", e.Data)
			}
		})
	}
}

func TestAnEnvelopeV2FromThatIsNoContractIsRefused(t *testing.T) {
	wrong := moved
	wrong.EnvelopeV2From = "V2"
	if _, err := serveHandler(t, wrong, site(nil), nil); err == nil || !strings.Contains(err.Error(), "EnvelopeV2From") {
		t.Fatalf("got %v", err)
	}
}

// error/v1 has one place, so a Problem's first place is its at {file, field}.
func TestTheFirstPlaceIsTheV1At(t *testing.T) {
	p := serve.Fail(serve.TypeRefused, "duplicate", "another file already holds the request second")
	p.At = []serve.Place{{File: "request/second-copy.yml", Document: 1, Line: 3, Path: "metadata.id"}, {File: "request/second.yml", Path: "metadata.id"}}
	body, err := json.Marshal(app.Failure("shop", "v1", p))
	if err != nil {
		t.Fatal(err)
	}
	if err := contracts.Validate("envelope/v1/envelope.schema.json", body); err != nil {
		t.Fatalf("not envelope/v1: %v\n%s", err, body)
	}
	var e struct {
		Errors []struct {
			At map[string]string `json:"at"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(body, &e); err != nil || len(e.Errors) != 1 {
		t.Fatalf("%s: %v", body, err)
	}
	if want := map[string]string{"file": "request/second-copy.yml", "field": "metadata.id"}; !reflect.DeepEqual(e.Errors[0].At, want) {
		t.Fatalf("at %v, want %v", e.Errors[0].At, want)
	}
}

func TestA406OnEnvelopeV2IsAProblem(t *testing.T) {
	w := call(t, movedHandler(t), "GET", "/api/greeting", "", map[string]string{serve.ContractHeader: "v9"})
	if w.Code != http.StatusNotAcceptable {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	if err := contracts.Validate("envelope/v2/envelope.schema.json", w.Body.Bytes()); err != nil {
		t.Fatalf("not envelope/v2: %v\n%s", err, w.Body)
	}
	var e struct {
		Errors []json.RawMessage `json:"errors"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &e); err != nil || len(e.Errors) != 1 {
		t.Fatalf("%s: %v", w.Body, err)
	}
	if err := contracts.Validate("problem/v1/problem.schema.json", e.Errors[0]); err != nil {
		t.Fatalf("not a Problem: %v\n%s", err, e.Errors[0])
	}
}

// An empty detail or an empty place never reaches the wire, as problem/v1 refuses both.
func TestAFailureNeverWritesAnEmptyDetailOrPlace(t *testing.T) {
	p := serve.Fail(serve.TypeFailed, serve.ReasonConfigUnreadable, "")
	p.At = []serve.Place{{}, {Path: "queue.wait"}, {}}
	e := moved.Failure("shop", "v2", p)
	body, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	if err := contracts.Validate("envelope/v2/envelope.schema.json", body); err != nil {
		t.Fatalf("not envelope/v2: %v\n%s", err, body)
	}
	if got := e.Errors[0]; got.Detail != "failed: config_unreadable" || len(got.At) != 1 || got.At[0].Path != "queue.wait" {
		t.Fatalf("got %+v", got)
	}
	if len(p.At) != 3 {
		t.Fatalf("Failure changed the caller's places: %+v", p.At)
	}
}

// The page asks no contract, as choose reads Accept-Contract only under /api/, so a refusal there is in the default.
func TestTheGuardReadsTheContractOnlyForTheAPI(t *testing.T) {
	h := movedHandler(t)
	for target, schema := range map[string]string{"/board": "envelope/v2/envelope.schema.json", "/api/greeting": "envelope/v1/envelope.schema.json"} {
		req := httptest.NewRequest("GET", "http://evil.example"+target, nil)
		req.Header.Set(serve.ContractHeader, "v1")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != http.StatusForbidden {
			t.Fatalf("%s: status %d", target, w.Code)
		}
		if err := contracts.Validate(schema, w.Body.Bytes()); err != nil {
			t.Errorf("%s: not %s: %v\n%s", target, schema, err, w.Body)
		}
	}
}
