package serve_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/ElderlyGate/contracts"
	"github.com/spf13/cobra"

	"github.com/tugbayatilla/mankit/ask"
	"github.com/tugbayatilla/mankit/serve"
)

// The tests serve as designer, so its version answer is checked against its own contract too.
var app = serve.App{ID: "designer", Command: "e-designer", Version: "5.0.0", Contracts: []string{"v1"}, Addr: "127.0.0.1:0"}

const token = "s3cret"

type greeting struct {
	Text string `json:"text"`
}

func site(styles func() (map[ask.Type]ask.Style, error)) serve.Site {
	return serve.Site{
		Name: "shop",
		Routes: []serve.Route{
			{Pattern: "GET /api/greeting", Handler: func(w http.ResponseWriter, r *http.Request) {
				serve.Write(w, r, http.StatusOK, "Greeting", greeting{Text: "hello"})
			}},
			{Pattern: "POST /api/greeting", Action: ask.Action{Type: ask.Edit, What: "change the greeting"}, Handler: func(w http.ResponseWriter, r *http.Request) {
				serve.Write(w, r, http.StatusOK, "Greeting", greeting{Text: "changed"})
			}},
			{Pattern: "DELETE /api/greeting", Action: ask.Action{Type: ask.Delete, System: true}, Handler: func(w http.ResponseWriter, r *http.Request) {
				serve.WriteError(w, r, http.StatusConflict, errors.New("the greeting is in use"))
			}},
		},
		UI:     http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "the page") }),
		Styles: styles,
	}
}

func handler(t *testing.T, styles func() (map[ask.Type]ask.Style, error)) http.Handler {
	t.Helper()
	h, err := app.Handler(site(styles), token, []string{"tunnel.example"})
	if err != nil {
		t.Fatal(err)
	}
	return h
}

// call is one API request with the token, as a reader on this machine sends it.
func call(t *testing.T, h http.Handler, method, target, body string, header map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, "http://127.0.0.1:7480"+target, r)
	req.Header.Set(app.TokenHeader(), token)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range header {
		if v == "" {
			req.Header.Del(k)
			continue
		}
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

// envelope checks body against the contracts envelope and returns it.
func envelope(t *testing.T, body []byte) serve.Envelope {
	t.Helper()
	if err := contracts.Validate("envelope/v1/envelope.schema.json", body); err != nil {
		t.Fatalf("not a contracts envelope: %v\n%s", err, body)
	}
	var e serve.Envelope
	if err := json.Unmarshal(body, &e); err != nil {
		t.Fatal(err)
	}
	return e
}

// refusal checks an error answer: its status, its one error's code, and where it ran.
func refusal(t *testing.T, w *httptest.ResponseRecorder, status int, code string) serve.Error {
	t.Helper()
	if w.Code != status {
		t.Fatalf("status %d, want %d: %s", w.Code, status, w.Body)
	}
	e := envelope(t, w.Body.Bytes())
	if len(e.Errors) != 1 || e.Errors[0].Code != code {
		t.Fatalf("errors %+v, want one %s", e.Errors, code)
	}
	if e.Ran != (serve.Ran{Name: "shop", App: "designer", Version: "5.0.0", Contract: "v1"}) {
		t.Fatalf("ran %+v", e.Ran)
	}
	return e.Errors[0]
}

// Success

func TestServeAnswersARouteInTheEnvelope(t *testing.T) {
	w := call(t, handler(t, nil), "GET", "/api/greeting", "", nil)
	if w.Code != http.StatusOK || !strings.HasPrefix(w.Header().Get("Content-Type"), "application/json") {
		t.Fatalf("status %d, type %q", w.Code, w.Header().Get("Content-Type"))
	}
	e := envelope(t, w.Body.Bytes())
	want := serve.Ran{Name: "shop", App: "designer", Version: "5.0.0", Contract: "v1"}
	if e.APIVersion != "elderlygate/designer/app/v1" || e.Kind != "Greeting" || e.Ran != want || len(e.Errors) != 0 {
		t.Fatalf("got %+v", e)
	}
}

func TestServeAnswersItsVersion(t *testing.T) {
	w := call(t, handler(t, nil), "GET", "/api/version", "", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	if err := contracts.Validate("designer/v1/version.schema.json", w.Body.Bytes()); err != nil {
		t.Fatalf("not designer's version answer: %v\n%s", err, w.Body)
	}
	two := app
	two.Contracts = []string{"v1", "v2"}
	h, err := two.Handler(site(nil), token, nil)
	if err != nil {
		t.Fatal(err)
	}
	var e struct {
		Data serve.VersionData `json:"data"`
	}
	if err := json.Unmarshal(call(t, h, "GET", "/api/version", "", nil).Body.Bytes(), &e); err != nil {
		t.Fatal(err)
	}
	if e.Data.Contract != "v2" || strings.Join(e.Data.Contracts, ",") != "v1,v2" {
		t.Fatalf("version lists %+v, want contract v2 of v1,v2", e.Data)
	}
}

func TestServeOpensThePageFromItsLinkWithAStrictCookie(t *testing.T) {
	h := handler(t, nil)
	req := httptest.NewRequest("GET", "http://127.0.0.1:7480/board?x=1&token="+token, nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	cookies := w.Result().Cookies()
	if w.Code != http.StatusOK || len(cookies) != 1 {
		t.Fatalf("status %d, cookies %v", w.Code, cookies)
	}
	c := cookies[0]
	if c.Name != "designer-token-7480" || c.Value != token || !c.HttpOnly || c.SameSite != http.SameSiteStrictMode {
		t.Fatalf("cookie %+v", c)
	}
	if !strings.Contains(w.Body.String(), `url=/board?x=1"`) || strings.Contains(w.Body.String(), token) {
		t.Fatalf("the page goes on to %q", w.Body)
	}

	req = httptest.NewRequest("GET", "http://127.0.0.1:7480/board", nil)
	req.AddCookie(c)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK || w.Body.String() != "the page" {
		t.Fatalf("with the cookie: %d %q", w.Code, w.Body)
	}
}

func TestServeTakesTheCookieForAnAPIRequest(t *testing.T) {
	req := httptest.NewRequest("GET", "http://localhost:7480/api/greeting", nil)
	req.AddCookie(&http.Cookie{Name: "designer-token-7480", Value: token})
	w := httptest.NewRecorder()
	handler(t, nil).ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
}

func TestServeAnswersANameItWasGiven(t *testing.T) {
	req := httptest.NewRequest("GET", "http://tunnel.example/api/version", nil)
	req.Header.Set(app.TokenHeader(), token)
	w := httptest.NewRecorder()
	handler(t, nil).ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
}

// Contract

func TestServeAnswersTheContractAskedFor(t *testing.T) {
	w := call(t, handler(t, nil), "GET", "/api/greeting", "", map[string]string{serve.ContractHeader: "v1"})
	if e := envelope(t, w.Body.Bytes()); w.Code != http.StatusOK || e.Ran.Contract != "v1" {
		t.Fatalf("status %d, ran %+v", w.Code, e.Ran)
	}
}

func TestServeRefusesAContractItDoesNotAnswer(t *testing.T) {
	w := call(t, handler(t, nil), "GET", "/api/version", "", map[string]string{serve.ContractHeader: "v9"})
	e := refusal(t, w, http.StatusNotAcceptable, serve.CodeUnsupportedContract)
	if strings.Join(e.Supported, ",") != "v1" {
		t.Fatalf("supported %v, want v1", e.Supported)
	}
}

func TestAppChoosesItsNewestContractByDefault(t *testing.T) {
	two := app
	two.Contracts = []string{"v1", "v2"}
	if c, err := two.Contract(""); err != nil || c != "v2" {
		t.Fatalf("got %q, %v", c, err)
	}
	_, err := two.Contract("v3")
	var e *serve.Error
	if !errors.As(err, &e) || e.Code != serve.CodeUnsupportedContract || strings.Join(e.Supported, ",") != "v1,v2" {
		t.Fatalf("got %v", err)
	}
}

// Errors and codes

func TestServeRefusesAnotherHost(t *testing.T) {
	req := httptest.NewRequest("GET", "http://evil.example/api/version", nil)
	req.Header.Set(app.TokenHeader(), token)
	w := httptest.NewRecorder()
	handler(t, nil).ServeHTTP(w, req)
	refusal(t, w, http.StatusForbidden, serve.CodeHost)
}

func TestServeRefusesAnotherOrigin(t *testing.T) {
	for name, header := range map[string]map[string]string{
		"origin":         {"Origin": "http://evil.example"},
		"sec-fetch-site": {"Sec-Fetch-Site": "cross-site"},
	} {
		t.Run(name, func(t *testing.T) {
			refusal(t, call(t, handler(t, nil), "GET", "/api/version", "", header), http.StatusForbidden, serve.CodeOrigin)
		})
	}
}

func TestServeTakesItsOwnOrigin(t *testing.T) {
	w := call(t, handler(t, nil), "GET", "/api/version", "", map[string]string{"Origin": "http://127.0.0.1:7480", "Sec-Fetch-Site": "same-origin"})
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
}

func TestServeRefusesARequestWithoutTheToken(t *testing.T) {
	for name, value := range map[string]string{"none": "", "wrong": "guess"} {
		t.Run(name, func(t *testing.T) {
			e := refusal(t, call(t, handler(t, nil), "GET", "/api/version", "", map[string]string{app.TokenHeader(): value}), http.StatusUnauthorized, serve.CodeToken)
			if strings.Contains(e.Message, token) {
				t.Fatal("the refusal tells the token")
			}
		})
	}
}

func TestServeShowsThePageWithoutTheCookieOnlyWhereItsLinkIs(t *testing.T) {
	for _, target := range []string{"/", "/?token=guess"} {
		w := httptest.NewRecorder()
		handler(t, nil).ServeHTTP(w, httptest.NewRequest("GET", "http://127.0.0.1:7480"+target, nil))
		if w.Code != http.StatusUnauthorized || !strings.Contains(w.Body.String(), "e-designer servers link") || len(w.Result().Cookies()) != 0 {
			t.Fatalf("%s: %d %q", target, w.Code, w.Body)
		}
	}
}

func TestServeRefusesABodyThatIsNotJSON(t *testing.T) {
	w := call(t, handler(t, nil), "POST", "/api/greeting?confirm=yes", "text=hi", map[string]string{"Content-Type": "application/x-www-form-urlencoded"})
	refusal(t, w, http.StatusUnsupportedMediaType, serve.CodeNotJSON)
}

func TestServeAnswersAnUnknownRouteInTheEnvelope(t *testing.T) {
	refusal(t, call(t, handler(t, nil), "GET", "/api/nothing", "", nil), http.StatusNotFound, serve.CodeNotFound)
}

func TestServeAnswersAnAppErrorWithItsCode(t *testing.T) {
	refusal(t, call(t, handler(t, nil), "DELETE", "/api/greeting?confirm=yes", "", nil), http.StatusConflict, serve.CodeFailed)
}

func TestHandlerRefusesARouteItCannotGuard(t *testing.T) {
	ok := func(http.ResponseWriter, *http.Request) {}
	for name, rt := range map[string]serve.Route{
		"no method":             {Pattern: "/api/x", Handler: ok},
		"outside the api":       {Pattern: "GET /x", Handler: ok},
		"the version":           {Pattern: "GET /api/version", Handler: ok},
		"no handler":            {Pattern: "GET /api/x"},
		"a change without":      {Pattern: "PUT /api/x", Handler: ok},
		"a change of no type":   {Pattern: "PUT /api/x", Action: ask.Action{Type: "launch"}, Handler: ok},
		"a read with an asking": {Pattern: "GET /api/x", Action: ask.Action{Type: ask.Edit}, Handler: ok},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := app.Handler(serve.Site{Routes: []serve.Route{rt}}, token, nil); err == nil {
				t.Fatal("taken")
			}
		})
	}
	if _, err := app.Handler(serve.Site{}, "", nil); err == nil {
		t.Fatal("served without a token")
	}
	if _, err := (serve.App{ID: "designer"}).Handler(serve.Site{}, token, nil); err == nil {
		t.Fatal("served an app without its version and contracts")
	}
}

// Asking

func TestServeRefusesAChangeWithoutTheUsersYes(t *testing.T) {
	e := refusal(t, call(t, handler(t, nil), "POST", "/api/greeting", `{"text":"hi"}`, nil), http.StatusPreconditionRequired, serve.CodeUnconfirmed)
	if !strings.Contains(e.Message, "change the greeting") {
		t.Fatalf("the refusal does not say what it would change: %q", e.Message)
	}
}

func TestServeTakesAChangeWithTheUsersYes(t *testing.T) {
	for _, answer := range []string{serve.ConfirmYes, serve.ConfirmRemember} {
		w := call(t, handler(t, nil), "POST", "/api/greeting?confirm="+answer, `{"text":"hi"}`, nil)
		if w.Code != http.StatusOK {
			t.Fatalf("confirm=%s: %d %s", answer, w.Code, w.Body)
		}
	}
}

func TestServeTakesAChangeTheUserNeverWantsToBeAskedAbout(t *testing.T) {
	never := func() (map[ask.Type]ask.Style, error) {
		return map[ask.Type]ask.Style{ask.Edit: ask.Never, ask.Delete: ask.Never}, nil
	}
	h := handler(t, never)
	if w := call(t, h, "POST", "/api/greeting", `{"text":"hi"}`, nil); w.Code != http.StatusOK {
		t.Fatalf("edit set to never: %d %s", w.Code, w.Body)
	}
	// A change to the system itself still asks, whatever was remembered.
	refusal(t, call(t, h, "DELETE", "/api/greeting", "", nil), http.StatusPreconditionRequired, serve.CodeUnconfirmed)
}

func TestServeSaysWhichConfigItCannotRead(t *testing.T) {
	broken := func() (map[ask.Type]ask.Style, error) { return nil, errors.New("config.yaml: confirm is not a map") }
	e := refusal(t, call(t, handler(t, broken), "POST", "/api/greeting", `{"text":"hi"}`, nil), http.StatusInternalServerError, serve.CodeConfig)
	if !strings.Contains(e.Message, "config.yaml") {
		t.Fatalf("message %q", e.Message)
	}
}

func TestServeNeverAsksForARead(t *testing.T) {
	broken := func() (map[ask.Type]ask.Style, error) { return nil, errors.New("unreadable") }
	if w := call(t, handler(t, broken), "GET", "/api/greeting", "", nil); w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
}

// CLI and HTTP parity

func TestTheCLIAndHTTPGiveTheSameAnswer(t *testing.T) {
	var cli bytes.Buffer
	if err := serve.Encode(&cli, app.Answer("shop", "v1", "Greeting", greeting{Text: "hello"})); err != nil {
		t.Fatal(err)
	}
	w := call(t, handler(t, nil), "GET", "/api/greeting", "", nil)
	if cli.String() != w.Body.String() {
		t.Fatalf("CLI:\n%s\nHTTP:\n%s", cli.String(), w.Body)
	}
}

func TestTheCLIAndHTTPGiveTheSameError(t *testing.T) {
	var cli bytes.Buffer
	_, err := app.Contract("v9")
	if err := serve.Encode(&cli, app.Failure("shop", "v1", err)); err != nil {
		t.Fatal(err)
	}
	envelope(t, cli.Bytes())
	w := call(t, handler(t, nil), "GET", "/api/greeting", "", map[string]string{serve.ContractHeader: "v9"})
	if cli.String() != w.Body.String() {
		t.Fatalf("CLI:\n%s\nHTTP:\n%s", cli.String(), w.Body)
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

// root is an app's root with serve and servers, answering the asking question in style.
func root(open func(*cobra.Command) (serve.Site, error)) *cobra.Command {
	r := &cobra.Command{Use: "e-designer", SilenceUsage: true, SilenceErrors: true}
	r.AddCommand(app.Commands(open, func(cmd *cobra.Command, a ask.Action) error {
		_, err := ask.Change(cmd, a, ask.YesNo)
		return err
	})...)
	return r
}

func help(t *testing.T, args ...string) string {
	t.Helper()
	var out bytes.Buffer
	r := root(nil)
	r.SetOut(&out)
	r.SetArgs(append(args, "--help"))
	if err := r.Execute(); err != nil {
		t.Fatal(err)
	}
	return out.String()
}

func TestMain(m *testing.M) {
	// The child of serve --detach is this test binary started again: it runs the app's root, as a real app's binary would.
	if os.Getenv("SERVE_TEST_CHILD") == "1" {
		r := root(func(*cobra.Command) (serve.Site, error) { return site(nil), nil })
		if err := r.Execute(); err != nil {
			os.Stderr.WriteString(err.Error() + "\n")
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}
