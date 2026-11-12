package serve_test

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/ElderlyGate/contracts"

	"github.com/tugbayatilla/mankit/v2/ask"
	"github.com/tugbayatilla/mankit/v2/features"
	"github.com/tugbayatilla/mankit/v2/serve"
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
	h, err := serveHandler(t, two, site(nil), nil)
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

func TestServeTakesTheTokenAsABearerHeader(t *testing.T) {
	h := handler(t, nil)
	for _, value := range []string{"Bearer " + token, "bearer " + token} {
		if w := call(t, h, "GET", "/api/greeting", "", map[string]string{"Authorization": value}); w.Code != http.StatusOK {
			t.Fatalf("%q: status %d: %s", value, w.Code, w.Body)
		}
	}
}

func TestServeTakesARenewedTokenWithoutARestart(t *testing.T) {
	dir := machine(t)
	writeToken(t, dir, token)
	h, err := app.Handler(site(nil), nil)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(h)
	defer srv.Close()
	cookie := "designer-token-" + strconv.Itoa(srv.Listener.Addr().(*net.TCPAddr).Port)
	get := func(bearer, cookieValue string) int {
		t.Helper()
		req, _ := http.NewRequest("GET", srv.URL+"/api/greeting", nil)
		if bearer != "" {
			req.Header.Set("Authorization", "Bearer "+bearer)
		}
		if cookieValue != "" {
			req.AddCookie(&http.Cookie{Name: cookie, Value: cookieValue})
		}
		resp, err := srv.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	if got := get(token, ""); got != http.StatusOK {
		t.Fatalf("before the renew: status %d", got)
	}
	link, err := url.Parse(answer(t, linkSchema, "servers", "token", "renew", "--yes").Data.(map[string]any)["url"].(string))
	if err != nil {
		t.Fatal(err)
	}
	renewed := link.Query().Get("token")
	if renewed == "" || renewed == token {
		t.Fatalf("renewed to %q", renewed)
	}
	if got := get(token, ""); got != http.StatusUnauthorized {
		t.Fatalf("the old token: status %d, want %d", got, http.StatusUnauthorized)
	}
	if got := get("", token); got != http.StatusUnauthorized {
		t.Fatalf("the old cookie: status %d, want %d", got, http.StatusUnauthorized)
	}
	if got := get(renewed, ""); got != http.StatusOK {
		t.Fatalf("the new token: status %d", got)
	}
	if got := get("", renewed); got != http.StatusOK {
		t.Fatalf("the new cookie: status %d", got)
	}
	resp, err := srv.Client().Get(srv.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized || !strings.Contains(string(page), "e-designer servers link") {
		t.Fatalf("the page with no cookie: %d %s", resp.StatusCode, page)
	}
}

func TestServeRefusesEveryEarlierTokenAfterRenewsBackToBack(t *testing.T) {
	h := handler(t, nil)
	first := tokenOf(t, answer(t, linkSchema, "servers", "token", "renew", "--yes"))
	second := tokenOf(t, answer(t, linkSchema, "servers", "token", "renew", "--yes"))
	for _, old := range []string{token, first} {
		refusal(t, call(t, h, "GET", "/api/greeting", "", map[string]string{"Authorization": "Bearer " + old}), http.StatusUnauthorized, serve.CodeToken)
	}
	if w := call(t, h, "GET", "/api/greeting", "", map[string]string{"Authorization": "Bearer " + second}); w.Code != http.StatusOK {
		t.Fatalf("the last token: status %d", w.Code)
	}
}

func TestServeTakesATokenWrittenInPlaceOfTheSameSize(t *testing.T) {
	dir := machine(t)
	writeToken(t, dir, token)
	h, err := app.Handler(site(nil), nil)
	if err != nil {
		t.Fatal(err)
	}
	if w := call(t, h, "GET", "/api/greeting", "", nil); w.Code != http.StatusOK {
		t.Fatalf("before: status %d", w.Code)
	}
	// The same file, the same size and, on a coarse clock, the same mtime: only reading it shows the change.
	path := filepath.Join(dir, "token")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("t0ken!\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	refusal(t, call(t, h, "GET", "/api/greeting", "", nil), http.StatusUnauthorized, serve.CodeToken)
	if w := call(t, h, "GET", "/api/greeting", "", map[string]string{"Authorization": "Bearer t0ken!"}); w.Code != http.StatusOK {
		t.Fatalf("the new token: status %d", w.Code)
	}
}

func TestServeLetsNobodyInWhileTheTokenFolderIsOpenToOthers(t *testing.T) {
	dir := machine(t)
	writeToken(t, dir, token)
	h, err := app.Handler(site(nil), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	e := refusal(t, call(t, h, "GET", "/api/greeting", "", nil), http.StatusInternalServerError, serve.CodeConfig)
	if !strings.Contains(e.Message, dir) {
		t.Fatalf("the refusal does not name %s: %s", dir, e.Message)
	}
}

func TestServeAnswersANameItWasGiven(t *testing.T) {
	req := httptest.NewRequest("GET", "http://tunnel.example/api/version", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	handler(t, nil).ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
}

// many is one server for many projects: each answer about a project names it, every other answer names the server.
func many(t *testing.T) http.Handler {
	t.Helper()
	s := site(nil)
	greet := func(w http.ResponseWriter, r *http.Request) {
		serve.WriteNamed(w, r, r.PathValue("project"), http.StatusOK, "Greeting", greeting{Text: "hello from " + r.PathValue("project")})
	}
	s.Routes = append(s.Routes,
		serve.Route{Pattern: "GET /api/projects/{project}/greeting", Handler: greet},
		serve.Route{Pattern: "POST /api/projects/{project}/greeting", Action: ask.Action{Type: ask.Edit}, Handler: greet},
		serve.Route{Pattern: "DELETE /api/projects/{project}/greeting", Action: ask.Action{Type: ask.Delete}, Handler: func(w http.ResponseWriter, r *http.Request) {
			serve.WriteErrorNamed(w, r, r.PathValue("project"), http.StatusConflict, errors.New("the greeting is in use"))
		}},
		serve.Route{Pattern: "GET /api/projects/{project}/nameless", Handler: func(w http.ResponseWriter, r *http.Request) {
			serve.WriteNamed(w, r, "", http.StatusOK, "Greeting", greeting{Text: "hello"})
		}},
		serve.Route{Pattern: "PUT /api/projects/{project}/nameless", Action: ask.Action{Type: ask.Edit}, Handler: func(w http.ResponseWriter, r *http.Request) {
			serve.WriteErrorNamed(w, r, "", http.StatusConflict, errors.New("the greeting is in use"))
		}},
	)
	h, err := serveHandler(t, app, s, nil)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func TestOneServerNamesEachProjectInItsAnswers(t *testing.T) {
	h := many(t)
	for _, c := range []struct{ method, target, project string }{
		{"GET", "/api/projects/alpha/greeting", "alpha"},
		{"GET", "/api/projects/beta/greeting", "beta"},
		{"POST", "/api/projects/beta/greeting?confirm=yes", "beta"},
	} {
		w := call(t, h, c.method, c.target, "", nil)
		if w.Code != http.StatusOK {
			t.Fatalf("%s %s: status %d: %s", c.method, c.target, w.Code, w.Body)
		}
		want := serve.Ran{Name: c.project, App: "designer", Version: "5.0.0", Contract: "v1"}
		if e := envelope(t, w.Body.Bytes()); e.Ran != want {
			t.Fatalf("%s %s ran %+v, want %+v", c.method, c.target, e.Ran, want)
		}
	}
}

func TestOneServerNamesTheProjectInAnError(t *testing.T) {
	w := call(t, many(t), "DELETE", "/api/projects/alpha/greeting?confirm=yes", "", nil)
	if w.Code != http.StatusConflict {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	if e := envelope(t, w.Body.Bytes()); e.Ran.Name != "alpha" {
		t.Fatalf("ran %+v, want the project alpha", e.Ran)
	}
}

func TestOneServerNamesItselfWhereNoProjectIsNamed(t *testing.T) {
	h := many(t)
	for _, c := range []struct {
		method, target string
		header         map[string]string
		status         int
	}{
		{"GET", "/api/greeting", nil, http.StatusOK},
		{"GET", "/api/version", nil, http.StatusOK},
		{"GET", "/api/projects/alpha/nameless", nil, http.StatusOK},
		{"PUT", "/api/projects/alpha/nameless?confirm=yes", nil, http.StatusConflict},
		{"GET", "/api/projects/alpha/nothing", nil, http.StatusNotFound},
		{"GET", "/api/projects/alpha/greeting", map[string]string{serve.ContractHeader: "v9"}, http.StatusNotAcceptable},
		{"GET", "/api/projects/alpha/greeting", map[string]string{"Authorization": ""}, http.StatusUnauthorized},
		// serve's gate refuses a change before the handler can name its project.
		{"DELETE", "/api/projects/alpha/greeting", nil, http.StatusPreconditionRequired},
	} {
		w := call(t, h, c.method, c.target, "", c.header)
		if w.Code != c.status {
			t.Fatalf("%s %s %v: status %d, want %d: %s", c.method, c.target, c.header, w.Code, c.status, w.Body)
		}
		if e := envelope(t, w.Body.Bytes()); e.Ran.Name != "shop" {
			t.Fatalf("%s %s %v ran %+v, want the server's name shop", c.method, c.target, c.header, e.Ran)
		}
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
	req.Header.Set("Authorization", "Bearer "+token)
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
			e := refusal(t, call(t, handler(t, nil), "GET", "/api/version", "", map[string]string{"Authorization": "Bearer " + value}), http.StatusUnauthorized, serve.CodeToken)
			if strings.Contains(e.Message, token) {
				t.Fatal("the refusal tells the token")
			}
		})
	}
}

func TestServeRefusesTheTokenInAnyOtherHeader(t *testing.T) {
	for name, header := range map[string]map[string]string{
		"the product's own header": {"Authorization": "", "X-Designer-Token": token},
		"another scheme":           {"Authorization": "Basic " + token},
		"no scheme":                {"Authorization": token},
	} {
		t.Run(name, func(t *testing.T) {
			refusal(t, call(t, handler(t, nil), "GET", "/api/greeting", "", header), http.StatusUnauthorized, serve.CodeToken)
		})
	}
}

func TestServeLetsNobodyInWhileTheTokenFileCannotBeTrusted(t *testing.T) {
	for name, spoil := range map[string]func(t *testing.T, path string){
		"open to others": func(t *testing.T, path string) {
			if err := os.Chmod(path, 0o644); err != nil {
				t.Fatal(err)
			}
		},
		"empty": func(t *testing.T, path string) {
			if err := os.WriteFile(path, nil, 0o600); err != nil {
				t.Fatal(err)
			}
		},
		"gone": func(t *testing.T, path string) {
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			dir := machine(t)
			writeToken(t, dir, token)
			h, err := app.Handler(site(nil), nil)
			if err != nil {
				t.Fatal(err)
			}
			// The handler has read the token once, so what it holds from before must not let anyone in either.
			if w := call(t, h, "GET", "/api/greeting", "", nil); w.Code != http.StatusOK {
				t.Fatalf("before: status %d", w.Code)
			}
			path := filepath.Join(dir, "token")
			spoil(t, path)
			for _, header := range []map[string]string{nil, {"Authorization": ""}} {
				e := refusal(t, call(t, h, "GET", "/api/greeting", "", header), http.StatusInternalServerError, serve.CodeConfig)
				if strings.Contains(e.Message, token) {
					t.Fatal("the refusal tells the token")
				}
			}
			req := httptest.NewRequest("GET", "http://127.0.0.1:7480/", nil)
			req.AddCookie(&http.Cookie{Name: "designer-token-7480", Value: token})
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)
			if w.Code != http.StatusInternalServerError || strings.Contains(w.Body.String(), token) {
				t.Fatalf("the page: %d %s", w.Code, w.Body)
			}
		})
	}
}

func TestHandlerRefusesToStartOnATokenFileOthersCanRead(t *testing.T) {
	dir := machine(t)
	writeToken(t, dir, token)
	path := filepath.Join(dir, "token")
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := app.Handler(site(nil), nil); err == nil || !strings.Contains(err.Error(), path) {
		t.Fatalf("got %v, want a refusal naming %s", err, path)
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
	req.Header.Set("Authorization", "Bearer "+token)
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
			if _, err := serveHandler(t, app, serve.Site{Routes: []serve.Route{rt}}, nil); err == nil {
				t.Fatal("taken")
			}
		})
	}
	if _, err := serveHandler(t, (serve.App{ID: "designer"}), serve.Site{}, nil); err == nil {
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
	h, err := serveHandler(t, app, s, nil)
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
	h, err := serveHandler(t, app, s, nil)
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
	h, err := serveHandler(t, app, s, nil)
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
	_, err := serveHandler(t, app, serve.Site{Routes: []serve.Route{{Pattern: "GET /api/x", Handler: ok}, {Pattern: "GET /api/x", Handler: ok}}}, nil)
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
	h, err := serveHandler(t, app, s, nil)
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

// needing is the test site with a feature "hear" that needs HEAR_URL, which is not set, whose module is m; closed by public when closed is set.
func needing(t *testing.T, m any, closed bool) http.Handler {
	t.Helper()
	var public []string
	if closed {
		public = []string{"hear"}
	}
	list := features.New([]features.Feature{{ID: "hear", Needs: []features.Value{{Name: "HEAR_URL", Purpose: "where changes go"}}}},
		features.Store{Off: func() ([]string, error) { return nil, nil }},
		features.Closer{By: "public", Closed: func() ([]string, error) { return public, nil }},
	).WithLookup(func(string) bool { return false })
	if err := list.Attach("hear", m); err != nil {
		t.Fatal(err)
	}
	s := site(nil)
	s.Features = list
	h, err := serveHandler(t, app, s, nil)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func TestAModuleMissingAValueAnswersWhatItNeedsAndTheRestRuns(t *testing.T) {
	m := &recorder{}
	h := needing(t, m, false)
	e := refusal(t, call(t, h, "GET", "/api/heard", "", nil), http.StatusServiceUnavailable, features.CodeNeedsValues)
	if !strings.Contains(e.Message, "HEAR_URL (where changes go)") {
		t.Fatalf("message %q does not name the missing value", e.Message)
	}
	if w := call(t, h, "POST", "/api/greeting?confirm=yes", `{"text":"hi"}`, nil); w.Code != http.StatusOK {
		t.Fatalf("the change answered %d beside the broken module", w.Code)
	}
	if len(m.heard) != 0 || len(m.skipped) != 1 || m.skipped[0].Route.Pattern != "POST /api/greeting" {
		t.Fatalf("heard %+v, skipped %+v, want the broken module to hear only that it missed the change", m.heard, m.skipped)
	}
}

func TestAModuleOffAndMissingAValueIsNotThere(t *testing.T) {
	refusal(t, call(t, needing(t, &recorder{}, true), "GET", "/api/heard", "", nil), http.StatusNotFound, serve.CodeNotFound)
}

func TestHandlerRefusesToStartWithoutAValueTheAppCannotRunWithout(t *testing.T) {
	s := site(nil)
	s.Needs = []features.Value{{Name: "SHOP_DIR", Purpose: "where the shop lives"}, {Name: "SHOP_KEY", Purpose: "what opens it"}}
	s.Lookup = func(name string) bool { return name == "SHOP_KEY" }
	_, err := serveHandler(t, app, s, nil)
	var e *serve.Error
	if !errors.As(err, &e) || e.Code != serve.CodeCoreNeedsValues || !strings.Contains(e.Message, "SHOP_DIR (where the shop lives)") || strings.Contains(e.Message, "SHOP_KEY") {
		t.Fatalf("err = %v, want core_needs_values naming SHOP_DIR alone", err)
	}
	s.Lookup = func(string) bool { return true }
	if _, err := serveHandler(t, app, s, nil); err != nil {
		t.Fatal(err)
	}
}

func TestFeaturesThatCannotBeReadAfterAChangeAreLoggedAndEveryModuleMissedIt(t *testing.T) {
	m := &recorder{}
	list := features.New([]features.Feature{{ID: "hear"}}, features.Store{Off: func() ([]string, error) { return nil, errors.New("disk gone") }})
	if err := list.Attach("hear", m); err != nil {
		t.Fatal(err)
	}
	var log bytes.Buffer
	s := site(nil)
	s.Features, s.Log = list, &log
	h, err := serveHandler(t, app, s, nil)
	if err != nil {
		t.Fatal(err)
	}
	if w := call(t, h, "POST", "/api/greeting?confirm=yes", `{"text":"hi"}`, nil); w.Code != http.StatusOK {
		t.Fatalf("the change answered %d", w.Code)
	}
	if want := "POST /api/greeting: the features cannot be read, so every module missed this change: disk gone\n"; log.String() != want || len(m.skipped) != 1 {
		t.Fatalf("log %q, skipped %+v", log.String(), m.skipped)
	}
}

func TestHandlerRefusesAModuleItCannotUse(t *testing.T) {
	list := features.New([]features.Feature{{ID: "hear"}}, features.Store{Off: func() ([]string, error) { return nil, nil }})
	if err := list.Attach("hear", "not a part"); err != nil {
		t.Fatal(err)
	}
	if _, err := serveHandler(t, app, serve.Site{Features: list}, nil); err == nil {
		t.Fatal("a module that is not a serve.Part was taken")
	}
}
