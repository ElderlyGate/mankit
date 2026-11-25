package serve_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"testing"

	"go.naywee.com/contracts/v2"

	"go.naywee.com/mankit/v5/problem"
	"go.naywee.com/mankit/v5/serve"
)

// both is designer answering contracts v1 and v2, each in envelope/v2.
var both = serve.App{ID: "designer", Command: "e-designer", Version: "6.0.0", Contracts: []serve.Contract{{Version: "v1"}, {Version: "v2", Envelope: "v2"}}, Addr: "127.0.0.1:0"}

func bothHandler(t *testing.T) http.Handler {
	t.Helper()
	h, err := serveHandler(t, both, site(nil), nil)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func TestEveryContractAnswersInEnvelopeV2(t *testing.T) {
	h := bothHandler(t)
	for _, contract := range []string{"v1", "v2"} {
		for _, c := range []struct{ method, target string }{{"GET", "/api/greeting"}, {"DELETE", "/api/greeting?confirm=yes"}, {"GET", "/api/nothing"}} {
			body := call(t, h, c.method, c.target, "", map[string]string{serve.ContractHeader: contract}).Body.Bytes()
			if err := contracts.Validate("envelope/v2/envelope.schema.json", body); err != nil {
				t.Errorf("%s %s in %s: not envelope/v2: %v\n%s", c.method, c.target, contract, err, body)
			}
			var e serve.Envelope
			if err := json.Unmarshal(body, &e); err != nil || e.Source.Contract != contract {
				t.Errorf("%s %s in %s: source %+v, %v", c.method, c.target, contract, e.Source, err)
			}
		}
	}
}

func TestAProblemMeetsProblemV1(t *testing.T) {
	body := call(t, bothHandler(t), "GET", "/api/nothing", "", nil).Body.Bytes()
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

// A refusal of the guard is in the contract the reader asked for when the app serves it, else in the default (CT-RULE-1).
func TestTheGuardRefusesInTheContractAskedFor(t *testing.T) {
	h := bothHandler(t)
	for asked, want := range map[string]string{"v1": "v1", "v2": "v2", "": "v2", "v9": "v2"} {
		w := call(t, h, "GET", "/api/greeting", "", map[string]string{"Authorization": "Bearer wrong", serve.ContractHeader: asked})
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("asked %q: status %d", asked, w.Code)
		}
		if err := contracts.Validate("envelope/v2/envelope.schema.json", w.Body.Bytes()); err != nil {
			t.Errorf("asked %q: not envelope/v2: %v\n%s", asked, err, w.Body)
		}
		var e serve.Envelope
		if err := json.Unmarshal(w.Body.Bytes(), &e); err != nil || len(e.Errors) != 1 || e.Errors[0].Type != problem.TypeRefused || e.Errors[0].Reason != serve.ReasonToken || e.Source.Contract != want {
			t.Errorf("asked %q: %+v %+v, %v", asked, e.Source, e.Errors, err)
		}
	}
}

// servers finds this app on a port by its token refusal in envelope/v2; an answer in any other shape is not this app.
func TestServersFindsTheAppByItsEnvelopeV2Refusal(t *testing.T) {
	v2, err := json.Marshal(serve.App{ID: "designer", Version: "5.0.0", Contracts: []serve.Contract{{Version: "v2", Envelope: "v2"}}}.Failure("shop", "v2", problem.Fail(problem.TypeRefused, serve.ReasonToken, "no token")))
	if err != nil {
		t.Fatal(err)
	}
	other := []byte(`{"apiVersion":"elderlygate/designer/app/v1","kind":"Error","ran":{"name":"shop","app":"designer","version":"5.0.0","contract":"v1"},"data":null,"errors":[{"apiVersion":"elderlygate/designer/app/v1","kind":"Error","code":"token_refused","message":"no token"}]}`)
	for name, c := range map[string]struct {
		body      []byte
		answering bool
	}{"envelope/v2": {v2, true}, "another shape": {other, false}} {
		t.Run(name, func(t *testing.T) {
			dir := machine(t)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write(c.body)
			}))
			defer srv.Close()
			sleeper := exec.Command("sleep", "30")
			if err := sleeper.Start(); err != nil {
				t.Fatal(err)
			}
			defer func() { _ = sleeper.Process.Kill(); _ = sleeper.Wait() }()
			writeRecord(t, dir, srv.Listener.Addr().String(), sleeper.Process.Pid)
			e := answer(t, listSchema, "server", "list")
			list, _ := e.Data.([]any)
			if len(list) != 1 || list[0].(map[string]any)["answering"] != c.answering {
				t.Fatalf("listed %v, want answering %v", e.Data, c.answering)
			}
		})
	}
}

func TestAppContractsAreChecked(t *testing.T) {
	for name, c := range map[string]struct {
		contracts []serve.Contract
		says      string
	}{
		"envelope/v1":                      {[]serve.Contract{{Version: "v1", Envelope: "v1"}}, "serve writes only envelope/v2"},
		"an envelope serve does not write": {[]serve.Contract{{Version: "v1", Envelope: "V2"}}, "serve writes only envelope/v2"},
		"a version twice":                  {[]serve.Contract{{Version: "v1"}, {Version: "v1", Envelope: "v2"}}, "given twice"},
		"no contract":                      {nil, "at least one contract"},
		"an empty version":                 {[]serve.Contract{{Version: ""}}, `contract "" is not vN`},
		"a capital V":                      {[]serve.Contract{{Version: "V1"}}, `contract "V1" is not vN`},
		"a bare number":                    {[]serve.Contract{{Version: "1"}}, `contract "1" is not vN`},
	} {
		t.Run(name, func(t *testing.T) {
			wrong := both
			wrong.Contracts = c.contracts
			if _, err := serveHandler(t, wrong, site(nil), nil); err == nil || !strings.Contains(err.Error(), c.says) {
				t.Fatalf("got %v, want it to say %q", err, c.says)
			}
		})
	}
	// An empty envelope is v2.
	fine := both
	fine.Contracts = []serve.Contract{{Version: "v1"}, {Version: "v2", Envelope: "v2"}}
	if _, err := serveHandler(t, fine, site(nil), nil); err != nil {
		t.Fatal(err)
	}
}

func TestA406OnEnvelopeV2IsAProblem(t *testing.T) {
	w := call(t, bothHandler(t), "GET", "/api/greeting", "", map[string]string{serve.ContractHeader: "v9"})
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
	p := problem.Fail(problem.TypeFailed, serve.ReasonConfigUnreadable, "")
	p.At = []problem.Place{{}, {Path: "queue.wait"}, {}}
	e := both.Failure("shop", "v2", p)
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
	h := bothHandler(t)
	for target, want := range map[string]string{"/board": "v2", "/api/greeting": "v1"} {
		req := httptest.NewRequest("GET", "http://evil.example"+target, nil)
		req.Header.Set(serve.ContractHeader, "v1")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != http.StatusForbidden {
			t.Fatalf("%s: status %d", target, w.Code)
		}
		var e serve.Envelope
		if err := json.Unmarshal(w.Body.Bytes(), &e); err != nil || e.Source.Contract != want {
			t.Errorf("%s: source %+v, want contract %s: %v", target, e.Source, want, err)
		}
	}
}
