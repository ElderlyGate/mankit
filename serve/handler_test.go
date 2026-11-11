package serve_test

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/ElderlyGate/contracts"

	"github.com/tugbayatilla/mankit/ask"
	"github.com/tugbayatilla/mankit/features"
	"github.com/tugbayatilla/mankit/serve"
)

// The HTTP door of every app: App.Handler and what it answers.

// Success

func TestServeAnswersARouteInTheEnvelope(t *testing.T) {
	w := call(t, handler(t, nil), "GET", "/api/greeting", "", nil)
	if w.Code != http.StatusOK || !strings.HasPrefix(w.Header().Get("Content-Type"), "application/json") {
		t.Fatalf("status %d, type %q", w.Code, w.Header().Get("Content-Type"))
	}
	e := envelope(t, w.Body.Bytes())
	want := serve.Ran{Name: "shop", App: "designer", Version: "5.0.0", Contract: "v1"}
	if e.APIVersion != apiVersion || e.Kind != "Greeting" || e.Ran != want || len(e.Errors) != 0 {
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

func TestServeRefusesAChunkedBodyThatIsNotJSON(t *testing.T) {
	req := httptest.NewRequest("POST", "http://127.0.0.1:7480/api/greeting?confirm=yes", strings.NewReader("text=hi"))
	req.ContentLength = -1
	req.Header.Set(app.TokenHeader(), token)
	req.Header.Set("Content-Type", "text/plain")
	w := httptest.NewRecorder()
	handler(t, nil).ServeHTTP(w, req)
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
		"a read that changes":   {Pattern: "GET /api/x", Changes: serve.Change{Thing: "x", ID: func(any) string { return "1" }}, Handler: ok},
		"a change without id":   {Pattern: "PUT /api/x", Action: ask.Action{Type: ask.Edit}, Changes: serve.Change{Thing: "x"}, Handler: ok},
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

// More success

func TestServeMarksTheCookieSecureBehindHTTPS(t *testing.T) {
	for name, set := range map[string]func(*http.Request){
		"tls":   func(r *http.Request) { r.TLS = &tls.ConnectionState{} },
		"proxy": func(r *http.Request) { r.Header.Set("X-Forwarded-Proto", "https") },
	} {
		t.Run(name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "http://127.0.0.1:7480/?token="+token, nil)
			set(req)
			w := httptest.NewRecorder()
			handler(t, nil).ServeHTTP(w, req)
			if c := w.Result().Cookies(); len(c) != 1 || !c[0].Secure {
				t.Fatalf("cookies %+v", c)
			}
		})
	}
	req := httptest.NewRequest("GET", "http://127.0.0.1:7480/?token="+token, nil)
	w := httptest.NewRecorder()
	handler(t, nil).ServeHTTP(w, req)
	if c := w.Result().Cookies(); len(c) != 1 || c[0].Secure {
		t.Fatalf("plain HTTP cookie %+v", c)
	}
}

func TestServeOnwardPageDropsTheTokenFromTheHistory(t *testing.T) {
	w := httptest.NewRecorder()
	handler(t, nil).ServeHTTP(w, httptest.NewRequest("GET", "http://127.0.0.1:7480/board?token="+token, nil))
	for header, want := range map[string]string{"Referrer-Policy": "no-referrer", "X-Frame-Options": "DENY", "X-Content-Type-Options": "nosniff", "Cache-Control": "no-store"} {
		if got := w.Header().Get(header); got != want {
			t.Errorf("%s is %q, want %q", header, got, want)
		}
	}
	if !strings.Contains(w.Body.String(), `location.replace("/board")`) {
		t.Fatalf("the page does not replace its history entry: %s", w.Body)
	}
}

func TestServeOnwardPageStaysOnThisServer(t *testing.T) {
	for _, path := range []string{"//evil.example/x", "/\\evil"} {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequest("GET", "http://127.0.0.1:7480/", nil)
			req.URL.Path, req.URL.RawPath, req.URL.RawQuery = path, "", "token="+token
			w := httptest.NewRecorder()
			handler(t, nil).ServeHTTP(w, req)
			_, onward, _ := strings.Cut(w.Body.String(), "url=")
			onward, _, _ = strings.Cut(onward, `"`)
			if !strings.HasPrefix(onward, "/") || strings.HasPrefix(onward, "//") || strings.HasPrefix(onward, "/\\") || strings.Contains(onward, "evil.example") {
				t.Fatalf("the page leads off this server, to %q: %s", onward, w.Body)
			}
		})
	}
}

func TestServeRemembersTheAnswerInTheAppsConfig(t *testing.T) {
	var saved []ask.Type
	s := site(nil)
	s.Remember = func(t ask.Type) error { saved = append(saved, t); return nil }
	h, err := app.Handler(s, token, nil)
	if err != nil {
		t.Fatal(err)
	}
	if w := call(t, h, "POST", "/api/greeting?confirm=remember", `{"text":"hi"}`, nil); w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	// A change to the system itself is never remembered.
	call(t, h, "DELETE", "/api/greeting?confirm=remember", "", nil)
	if len(saved) != 1 || saved[0] != ask.Edit {
		t.Fatalf("remembered %v, want edit only", saved)
	}
}

func TestServeChangesNothingWhenTheAnswerCannotBeSaved(t *testing.T) {
	changed := false
	s := serve.Site{Routes: []serve.Route{{Pattern: "POST /api/x", Action: ask.Action{Type: ask.Edit}, Handler: func(http.ResponseWriter, *http.Request) { changed = true }}},
		Remember: func(ask.Type) error { return errors.New("config.yaml: read-only") }}
	h, err := app.Handler(s, token, nil)
	if err != nil {
		t.Fatal(err)
	}
	w := call(t, h, "POST", "/api/x?confirm=remember", "", nil)
	if w.Code != http.StatusInternalServerError || changed || !strings.Contains(w.Body.String(), serve.CodeConfig) {
		t.Fatalf("status %d, changed %t: %s", w.Code, changed, w.Body)
	}
}

// More errors and codes

func TestServeRefusesASameSiteRequest(t *testing.T) {
	refusal(t, call(t, handler(t, nil), "GET", "/api/version", "", map[string]string{"Sec-Fetch-Site": "same-site"}), http.StatusForbidden, serve.CodeOrigin)
}

func TestServeRefusesAWrongPageCookie(t *testing.T) {
	req := httptest.NewRequest("GET", "http://127.0.0.1:7480/board", nil)
	req.AddCookie(&http.Cookie{Name: "designer-token-7480", Value: "guess"})
	w := httptest.NewRecorder()
	handler(t, nil).ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized || w.Body.String() == "the page" {
		t.Fatalf("status %d: %q", w.Code, w.Body)
	}
}

func TestServeRefusesABodyTooLarge(t *testing.T) {
	var got error
	s := serve.Site{Routes: []serve.Route{{Pattern: "POST /api/x", Action: ask.Action{Type: ask.Edit}, Handler: func(w http.ResponseWriter, r *http.Request) {
		_, got = io.ReadAll(r.Body)
	}}}}
	h, err := app.Handler(s, token, nil)
	if err != nil {
		t.Fatal(err)
	}
	call(t, h, "POST", "/api/x?confirm=yes", `"`+strings.Repeat("x", 1<<20)+`"`, nil)
	var tooLarge *http.MaxBytesError
	if !errors.As(got, &tooLarge) {
		t.Fatalf("read %v, want the body cut at 1 MiB", got)
	}
}

func TestHandlerRefusesARouteGivenTwice(t *testing.T) {
	ok := func(http.ResponseWriter, *http.Request) {}
	_, err := app.Handler(serve.Site{Routes: []serve.Route{{Pattern: "GET /api/x", Handler: ok}, {Pattern: "GET /api/x", Handler: ok}}}, token, nil)
	if err == nil {
		t.Fatal("taken")
	}
}

// Feature modules

// recorder is a feature's module that adds GET /api/heard and keeps every change it is told of, and every one it missed.
type recorder struct{ heard, skipped []serve.Answered }

func (m *recorder) Mount(routes []serve.Route) ([]serve.Route, error) {
	return []serve.Route{{Pattern: "GET /api/heard", Handler: func(w http.ResponseWriter, r *http.Request) {
		serve.Write(w, r, http.StatusOK, "Heard", len(m.heard))
	}}}, nil
}

func (m *recorder) Changed(a serve.Answered) { m.heard = append(m.heard, a) }

func (m *recorder) Skipped(a serve.Answered) { m.skipped = append(m.skipped, a) }

// withModule is the test site with a feature "hear" whose module is m, switched off when off is set.
func withModule(t *testing.T, m any, off bool) http.Handler {
	t.Helper()
	var closed []string
	if off {
		closed = []string{"hear"}
	}
	list := features.New([]features.Feature{{ID: "hear", Title: "Hear", Covers: "changes"}}, features.Store{
		Off:  func() ([]string, error) { return slices.Clone(closed), nil },
		Keep: func(string, bool) error { return nil },
	})
	if err := list.Attach("hear", m); err != nil {
		t.Fatal(err)
	}
	s := site(nil)
	s.Features = list
	h, err := app.Handler(s, token, nil)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func TestAModuleIsToldOfAChangeAnsweredWell(t *testing.T) {
	m := &recorder{}
	h := withModule(t, m, false)
	call(t, h, "POST", "/api/greeting?confirm=yes", `{"text":"hi"}`, nil)
	call(t, h, "POST", "/api/greeting", `{"text":"hi"}`, nil)
	call(t, h, "DELETE", "/api/greeting?confirm=yes", "", nil)
	call(t, h, "GET", "/api/greeting", "", nil)
	if len(m.heard) != 1 || m.heard[0].Route.Pattern != "POST /api/greeting" || m.heard[0].Kind != "Greeting" || m.heard[0].Data != (greeting{Text: "changed"}) {
		t.Fatalf("heard %+v, want only the change answered well", m.heard)
	}
	if w := call(t, h, "GET", "/api/heard", "", nil); w.Code != http.StatusOK {
		t.Fatalf("the module's route answered %d", w.Code)
	}
}

func TestAModuleOffIsNotServedAndHearsOnlyThatItMissedAChange(t *testing.T) {
	m := &recorder{}
	h := withModule(t, m, true)
	call(t, h, "POST", "/api/greeting?confirm=yes", `{"text":"hi"}`, nil)
	call(t, h, "POST", "/api/greeting", `{"text":"hi"}`, nil)
	refusal(t, call(t, h, "GET", "/api/heard", "", nil), http.StatusNotFound, serve.CodeNotFound)
	if len(m.heard) != 0 || len(m.skipped) != 1 {
		t.Fatalf("heard %+v, skipped %+v with the feature off", m.heard, m.skipped)
	}
}

func TestHandlerRefusesAModuleItCannotUse(t *testing.T) {
	list := features.New([]features.Feature{{ID: "hear"}}, features.Store{Off: func() ([]string, error) { return nil, nil }})
	if err := list.Attach("hear", "not a part"); err != nil {
		t.Fatal(err)
	}
	if _, err := app.Handler(serve.Site{Features: list}, token, nil); err == nil {
		t.Fatal("a module that is not a serve.Part was taken")
	}
}
