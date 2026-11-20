package serve

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"slices"
	"strings"

	"github.com/tugbayatilla/mankit/v4/ask"
	"github.com/tugbayatilla/mankit/v4/features"
	"github.com/tugbayatilla/mankit/v4/logs"
	"github.com/tugbayatilla/mankit/v4/problem"
)

// The words of a request: the header a reader asks a contract with, and the query that carries the user's yes to a change.
// A person's yes on the page is confirm=<id>, the Question's one-time id, with remember=yes to stop asking for its type: the id makes the yes count for exactly the change they were shown.
// A program's yes in one call is confirm=yes with its token in Authorization: Bearer, as a script or curl sends it, HTTP's --yes.
// From the page's cookie, confirm=yes and confirm=remember are old forms, still taken in v4 and gone at the next major once every page sends the id. The guard already refuses a request from another page (Origin, Sec-Fetch-Site); the id is defence in depth.
const (
	ContractHeader  = "Accept-Contract"
	ConfirmParam    = "confirm"
	RememberParam   = "remember"
	RememberYes     = "yes"
	ConfirmYes      = "yes"      // with Bearer, for good; from the cookie, an old form
	ConfirmRemember = "remember" // an old form of confirm=<id>&remember=yes
)

// QuestionKind and QuestionAPIVersion are the kind and the one form of the answer to a change waiting for a yes, the same for every app and every contract version (contracts question/v1).
const (
	QuestionKind       = "Question"
	QuestionAPIVersion = "elderlygate/contracts/question/v1"
)

// VersionKind is the kind of the GET /api/version answer.
const VersionKind = "Version"

// Route is one API route of an app; a change (POST, PUT, PATCH, DELETE) names the action type it asks as, a read names none.
type Route struct {
	Pattern string // a method and a path under /api/, such as "POST /api/designs/{design}/blocks"
	Action  ask.Action
	Handler http.HandlerFunc
	Changes Change // the thing a change route changes, for a feature's module to hear of without the handler calling it
	// ActionFor, when set, is the change one request makes, and whether it asks at all: a request that only follows a yes the person already gave, rehearses, or changes nothing they would be asked about does not ask.
	// Nil is Action, asked every time. Action still names the route's type, the one asked when ActionFor gives none it knows, and a change to the system itself stays one whatever ActionFor says.
	// ActionFor may read the body: it gets a copy, and the handler gets the body whole. A request that does not ask goes through as it is, so confirm=remember on it remembers nothing; one that asks remembers the type ActionFor gave.
	// The confirm is a question to the person, never a guard: the token is the guard, so a client sending a request that does not ask only skips a question.
	ActionFor func(*http.Request) (a ask.Action, asks bool)
}

// Change is the thing a change route changes: its kind, and its id in the data the change answers with.
type Change struct {
	Thing string                // the kind of thing, such as resource
	ID    func(data any) string // the thing's id from the answer's data
}

// Part is what a feature's module attaches to the app's feature list for serve; while the feature is off serve neither serves its routes nor tells it of a change.
type Part interface {
	// Mount checks the app's routes when the server starts and gives the routes the module adds.
	Mount(routes []Route) ([]Route, error)
	// Changed is told of each change answered well with Write while the feature is on.
	Changed(a Answered)
	// Skipped is told instead of Changed while the feature is off or broken, so the module knows it missed one.
	Skipped(a Answered)
}

// Answered is a change serve answered well: the request, its route, and what Write answered.
type Answered struct {
	Request *http.Request
	Route   Route
	Status  int
	Kind    string
	Data    any
}

// Site is what an app serves: its routes, its page, and the thing it serves.
type Site struct {
	Name     string                                 // named in every answer's source.name; empty is the app's ID
	Routes   []Route                                // every API route but GET /api/version, which serve answers
	UI       http.Handler                           // the page and its files at /; nil serves the API alone
	Styles   func() (map[ask.Type]ask.Style, error) // the user's asking style per type, read on every change; nil is every default
	Remember func(ask.Type) error                   // saves never for a type after "Remember my answer" (confirm=remember), in the app's config, such as ask.Remember
	Features *features.List                         // the app's feature list; the Part attached to a feature adds routes and hears of changes while it is on
	Needs    []features.Value                       // the values the app's core cannot run without; serve refuses to start while one is missing (G-CODE-8)
	Lookup   features.Lookup                        // finds a value of Needs; nil finds none, so any Needs refuses the start
	Log      io.Writer                              // the app's log, for what goes wrong after an answer is sent; nil is standard error
	// Telemetry, when set, starts what also sends the server's log records elsewhere, such as otlp.For(list, id, version) for the telemetry feature (ADR-E5); nil sends nothing, and the server links no OpenTelemetry.
	Telemetry logs.Exporter
}

// VersionData is the data of the GET /api/version answer (contracts version/v1): the app, its build and the contract the answer is in are in its ran.
type VersionData struct {
	SupportedContracts []string `json:"supportedContracts"`
}

// VersionAPIVersion is the one form of the version answer, the same for every app and every contract version (contracts version/v1).
const VersionAPIVersion = "elderlygate/contracts/version/v1"

var writing = map[string]bool{http.MethodPost: true, http.MethodPut: true, http.MethodPatch: true, http.MethodDelete: true}

// Handler serves site behind the guard, with this machine's token read on every request, so server token renew counts at once.
func (a App) Handler(site Site, hosts []string) (http.Handler, error) {
	if err := a.check(); err != nil {
		return nil, err
	}
	dir, err := a.serversDir()
	if err != nil {
		return nil, err
	}
	if _, err := a.ensureToken(dir); err != nil {
		return nil, err
	}
	if missing := features.Missing(site.Needs, site.Lookup); len(missing) > 0 {
		return nil, problem.Fail(problem.TypeFailed, ReasonCoreNeedsValues, fmt.Sprintf("%s cannot start: it needs values that are not set: %s", a.command(), features.Names(missing)))
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/version", func(w http.ResponseWriter, r *http.Request) {
		s := answering(r, "")
		// version/v1 stays on envelope/v1 for good, whatever contract the app answers in.
		e := a.Answer(s.name, s.contract, VersionKind, VersionData{SupportedContracts: a.versions()})
		e.APIVersion, e.form = VersionAPIVersion, "v1"
		writeEnvelope(w, http.StatusOK, e)
	})
	parts, err := attached(site.Features)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	add := func(rt Route, h http.Handler) error {
		if err := rt.check(); err != nil {
			return err
		}
		if seen[rt.Pattern] {
			return fmt.Errorf("serve: route %q is given twice", rt.Pattern)
		}
		seen[rt.Pattern] = true
		mux.Handle(rt.Pattern, h)
		return nil
	}
	questions := ask.NewQuestions()
	for _, rt := range site.Routes {
		if err := add(rt, a.gate(site, rt, parts, questions)); err != nil {
			return nil, err
		}
	}
	for _, p := range parts {
		routes, err := p.part.Mount(site.Routes)
		if err != nil {
			return nil, fmt.Errorf("serve: feature %s: %w", p.id, err)
		}
		for _, rt := range routes {
			if err := add(rt, a.feature(site.Features, p.id, a.gate(site, rt, nil, questions))); err != nil {
				return nil, err
			}
		}
	}
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		WriteError(w, r, http.StatusNotFound, problem.Fail(problem.TypeNotFound, "", fmt.Sprintf("%s has no %s %s", a.command(), r.Method, r.URL.Path)))
	})
	if site.UI != nil {
		mux.Handle("/", site.UI)
	}
	return a.guard(dir, hosts, site.Name, a.choose(site.Name, mux)), nil
}

// Write answers kind with data, in the envelope and the contract the request was answered in.
func Write(w http.ResponseWriter, r *http.Request, status int, kind string, data any) {
	WriteNamed(w, r, "", status, kind, data)
}

// WriteNamed answers like Write, naming the thing the answer is about, such as a project when one server serves many.
func WriteNamed(w http.ResponseWriter, r *http.Request, name string, status int, kind string, data any) {
	if c, ok := r.Context().Value(answeredKey{}).(*Answered); ok {
		c.Status, c.Kind, c.Data = status, kind, data
	}
	s := answering(r, name)
	writeEnvelope(w, status, s.app.Answer(s.name, s.contract, kind, data))
}

// WriteError answers err with status, in the envelope, or as a readable page to a person's browser; an err that is not a *Problem is type failed.
func WriteError(w http.ResponseWriter, r *http.Request, status int, err error) {
	WriteErrorNamed(w, r, "", status, err)
}

// WriteErrorNamed answers err like WriteError, naming the thing the answer is about.
func WriteErrorNamed(w http.ResponseWriter, r *http.Request, name string, status int, err error) {
	s := answering(r, name)
	s.app.writeRefusal(w, r, status, s.name, s.contract, err)
}

func writeEnvelope(w http.ResponseWriter, status int, e Envelope) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = Encode(w, e)
}

// state is what an answer needs to say where it comes from.
type state struct {
	app      App
	name     string
	contract string
}

type stateKey struct{}

// answering is the request's state, about the thing name when it is given, else the site's; a handler called outside Handler answers as an app without a name.
func answering(r *http.Request, name string) state {
	s, ok := r.Context().Value(stateKey{}).(state)
	if !ok {
		s = state{app: App{ID: "unknown", Version: "unknown"}, contract: "v1"}
	}
	if name != "" {
		s.name = name
	}
	return s
}

type stoppingKey struct{}

// StopOnShutdown makes Stopping close when srv begins to shut down; serve does it for its own server, an app that runs its own http.Server calls it once before serving.
func StopOnShutdown(srv *http.Server) {
	stopping := make(chan struct{})
	srv.BaseContext = func(net.Listener) context.Context {
		return context.WithValue(context.Background(), stoppingKey{}, stopping)
	}
	srv.RegisterOnShutdown(func() { close(stopping) })
}

// Stopping is closed once the server begins to stop, so a long answer such as a stream ends instead of holding the server open; outside serve it never closes.
func Stopping(r *http.Request) <-chan struct{} {
	ch, _ := r.Context().Value(stoppingKey{}).(chan struct{})
	return ch
}

// choose picks the contract an API reader asked for, or answers 406 with the ones the app answers; the page gets the default.
func (a App) choose(name string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked := ""
		if strings.HasPrefix(r.URL.Path, "/api/") {
			asked = strings.TrimSpace(r.Header.Get(ContractHeader))
		}
		contract, err := a.Contract(asked)
		// The version answer never refuses a contract: it is in the asked one when served, otherwise the newest (ADR-S03).
		if err != nil && r.URL.Path == "/api/version" {
			contract, err = a.Contract("")
		}
		if err != nil {
			fallback, _ := a.Contract("")
			a.writeRefusal(w, r, http.StatusNotAcceptable, name, fallback, err)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), stateKey{}, state{app: a, name: name, contract: contract})))
	})
}

type part struct {
	id   string
	part Part
}

// attached is every module on the list that serve can use; a module serve cannot use is refused, so a wrong wiring shows at start.
func attached(list *features.List) ([]part, error) {
	if list == nil {
		return nil, nil
	}
	var out []part
	for _, p := range list.Parts() {
		sp, ok := p.Part.(Part)
		if !ok {
			return nil, fmt.Errorf("serve: the module of feature %s is not a serve.Part", p.ID)
		}
		out = append(out, part{id: p.ID, part: sp})
	}
	return out, nil
}

// feature serves next only while the feature id is on; off, the route answers as if it were not there, and broken, it names the values it misses.
func (a App) feature(list *features.List, id string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		on, err := list.On(id)
		var nv *features.NeedsValues
		if errors.As(err, &nv) {
			WriteError(w, r, http.StatusServiceUnavailable, problem.Fail(problem.TypeFailed, features.ReasonNeedsValues, nv.Error()))
			return
		}
		if err != nil {
			WriteError(w, r, http.StatusInternalServerError, problem.Fail(problem.TypeFailed, ReasonConfigUnreadable, fmt.Sprintf("the feature %s cannot be read: %v", id, err)))
			return
		}
		if !on {
			WriteError(w, r, http.StatusNotFound, problem.Fail(problem.TypeNotFound, "", fmt.Sprintf("%s has no %s %s: the feature %s is off", a.command(), r.Method, r.URL.Path, id)))
			return
		}
		next.ServeHTTP(w, r)
	})
}

type answeredKey struct{}

// told runs the change, then tells each module of a change answered well, or that it missed one while its feature is off or broken; the answer is already sent, so a module never changes it.
func told(h http.HandlerFunc, rt Route, list *features.List, parts []part, log io.Writer) http.HandlerFunc {
	if len(parts) == 0 {
		return h
	}
	return func(w http.ResponseWriter, r *http.Request) {
		c := &Answered{Request: r, Route: rt}
		h(w, r.WithContext(context.WithValue(r.Context(), answeredKey{}, c)))
		if c.Status < 200 || c.Status > 299 {
			return
		}
		// The states are read once for every module; unreadable, every module hears it missed the change, and the log says why.
		states, err := list.States()
		if err != nil {
			fmt.Fprintf(log, "%s %s: the features cannot be read, so every module missed this change: %v\n", r.Method, r.URL.Path, err)
		}
		for _, p := range parts {
			if i := slices.IndexFunc(states, func(s features.State) bool { return s.ID == p.id }); i >= 0 && states[i].On && !states[i].Broken() {
				p.part.Changed(*c)
			} else {
				p.part.Skipped(*c)
			}
		}
	}
}

// gate answers 428 with a Question to a change that asks and came without the user's yes, unless the user picked never for its type; the yes is confirm=<id> for the Question asked, once, or a program's confirm=yes with Bearer.
func (a App) gate(site Site, rt Route, parts []part, questions *ask.Questions) http.Handler {
	if !writing[rt.method()] {
		return rt.Handler
	}
	log := site.Log
	if log == nil {
		log = os.Stderr
	}
	rt.Handler = told(rt.Handler, rt, site.Features, parts, log)
	remember := func(w http.ResponseWriter, r *http.Request, action ask.Action) bool {
		// A change to the system itself is never remembered (G-STACK-6); without Site.Remember the app keeps no answer, so remember is a yes.
		if site.Remember == nil || action.System {
			return true
		}
		if err := site.Remember(action.Type); err != nil {
			WriteError(w, r, http.StatusInternalServerError, problem.Fail(problem.TypeFailed, ReasonConfigUnreadable, fmt.Sprintf(
				"nothing was changed: the answer for %s could not be saved: %v; send the request again and answer its question", action.Type, err)))
			return false
		}
		return true
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		action, asks, err := rt.actionOf(r, log)
		if err != nil {
			writeBodyError(w, r, err)
			return
		}
		if !asks {
			rt.Handler(w, r)
			return
		}
		confirm := r.URL.Query().Get(ConfirmParam)
		switch confirm {
		case ConfirmYes:
			if bearer(r) != "" {
				// A program's yes in one call, its token in the header the guard checked.
				rt.Handler(w, r)
				return
			}
			// The old form from the page's cookie, taken in v4 only.
			rt.Handler(w, r)
			return
		case ConfirmRemember:
			if remember(w, r, action) {
				rt.Handler(w, r)
			}
			return
		}
		// The yes to the Question asked: taken once, for the same request; any other id asks again, as the token, not the id, is the guard.
		if confirm != "" {
			req, err := requestOf(r)
			if err != nil {
				writeBodyError(w, r, err)
				return
			}
			if asked, ok := questions.Take(confirm, req); ok {
				if r.URL.Query().Get(RememberParam) == RememberYes && !remember(w, r, asked) {
					return
				}
				rt.Handler(w, r)
				return
			}
		}
		styles := map[ask.Type]ask.Style{}
		if site.Styles != nil {
			read, err := site.Styles()
			if err != nil {
				WriteError(w, r, http.StatusInternalServerError, problem.Fail(problem.TypeFailed, ReasonConfigUnreadable, err.Error()))
				return
			}
			styles = read
		}
		// G-STACK-13: the user may pick "not at all" for a type; Resolve never gives that for a change to the system itself.
		style, _ := ask.Resolve(action, styles)
		if style == ask.Never {
			rt.Handler(w, r)
			return
		}
		req, err := requestOf(r)
		if err != nil {
			writeBodyError(w, r, err)
			return
		}
		s := answering(r, "")
		e := s.app.Answer(s.name, s.contract, QuestionKind, questions.Ask(action, style, req))
		e.APIVersion = QuestionAPIVersion
		writeEnvelope(w, http.StatusPreconditionRequired, e)
	})
}

// readBody is r's body, read once within serve's cap and handed on whole; one past the cap is too_large.
func readBody(r *http.Request) ([]byte, error) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return nil, problem.Fail(problem.TypeRefused, ReasonTooLarge, fmt.Sprintf("the request body is larger than the %d bytes serve takes", tooLarge.Limit))
		}
		return nil, problem.Fail(problem.TypeFailed, "", "nothing was changed: the request body could not be read: "+err.Error())
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	return body, nil
}

// writeBodyError answers a body readBody could not take: 413 for one too large, 500 for one that could not be read.
func writeBodyError(w http.ResponseWriter, r *http.Request, err error) {
	status := http.StatusInternalServerError
	if p, ok := err.(*problem.Problem); ok && p.Reason == ReasonTooLarge {
		status = http.StatusRequestEntityTooLarge
	}
	WriteError(w, r, status, err)
}

// requestOf is what a yes to r binds to: its method, path, query without the answer's own words, body and type.
func requestOf(r *http.Request) (ask.Request, error) {
	body, err := readBody(r)
	if err != nil {
		return ask.Request{}, err
	}
	q := r.URL.Query()
	q.Del(ConfirmParam)
	q.Del(RememberParam)
	return ask.Request{Method: r.Method, Path: r.URL.Path, Query: q, Body: body, ContentType: r.Header.Get("Content-Type")}, nil
}

// actionOf is the change the request r makes on rt, and whether it asks: ActionFor's when set, with rt's type for one it does not know, and never lowered from a change to the system itself.
// ActionFor reads a copy of the body, which is read once within serve's cap, so the handler still gets it whole; an ActionFor that panics is said on log and answered failed, and nothing is changed.
func (rt Route) actionOf(r *http.Request, log io.Writer) (a ask.Action, asks bool, err error) {
	if rt.ActionFor == nil {
		return rt.Action, true, nil
	}
	body, err := readBody(r)
	if err != nil {
		return ask.Action{}, false, err
	}
	read := r.Clone(r.Context())
	read.Body = io.NopCloser(bytes.NewReader(body))
	defer func() {
		if p := recover(); p != nil {
			fmt.Fprintf(log, "%s %s: the route's ActionFor failed: %v\n", r.Method, r.URL.Path, p)
			a, asks, err = ask.Action{}, false, problem.Fail(problem.TypeFailed, "", fmt.Sprintf("nothing was changed: %s %s could not say what it changes", r.Method, r.URL.Path))
		}
	}()
	a, asks = rt.ActionFor(read)
	if !ask.ValidType(string(a.Type)) {
		a.Type = rt.Action.Type
	}
	a.System = a.System || rt.Action.System
	return a, asks || a.System, nil
}

func (rt Route) method() string {
	method, _, _ := strings.Cut(rt.Pattern, " ")
	return method
}

// check refuses a route serve cannot guard: one without a method, outside /api/, or a change without its action type.
func (rt Route) check() error {
	method, path, ok := strings.Cut(rt.Pattern, " ")
	switch {
	case !ok || !writing[method] && method != http.MethodGet && method != http.MethodHead:
		return fmt.Errorf("serve: route %q needs a method: GET, HEAD, POST, PUT, PATCH or DELETE", rt.Pattern)
	case !strings.HasPrefix(path, "/api/"):
		return fmt.Errorf("serve: route %q is not under /api/", rt.Pattern)
	case path == "/api/version":
		return fmt.Errorf("serve: route %q is answered by serve", rt.Pattern)
	case rt.Handler == nil:
		return fmt.Errorf("serve: route %q has no handler", rt.Pattern)
	case writing[method] && !ask.ValidType(string(rt.Action.Type)):
		return fmt.Errorf("serve: change %q needs its action type, one of %v", rt.Pattern, ask.Types())
	case !writing[method] && rt.Action.Type != "":
		return fmt.Errorf("serve: read %q never asks, so it takes no action type", rt.Pattern)
	case !writing[method] && rt.ActionFor != nil:
		return fmt.Errorf("serve: read %q never asks, so it takes no ActionFor", rt.Pattern)
	case !writing[method] && rt.Changes.Thing != "":
		return fmt.Errorf("serve: read %q changes nothing", rt.Pattern)
	case rt.Changes.Thing != "" && rt.Changes.ID == nil:
		return fmt.Errorf("serve: change %q says the thing it changes but not how to find its id", rt.Pattern)
	}
	return nil
}
