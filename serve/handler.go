package serve

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"slices"
	"strings"

	"github.com/tugbayatilla/mankit/ask"
	"github.com/tugbayatilla/mankit/features"
)

// The words of a request: the header a reader asks a contract with, and the query that carries the user's yes to a change.
const (
	ContractHeader  = "Accept-Contract"
	ConfirmParam    = "confirm"
	ConfirmYes      = "yes"
	ConfirmRemember = "remember"
)

// VersionKind is the kind of the GET /api/version answer.
const VersionKind = "Version"

// Route is one API route of an app; a change (POST, PUT, PATCH, DELETE) names the action type it asks as, a read names none.
type Route struct {
	Pattern string // a method and a path under /api/, such as "POST /api/designs/{design}/blocks"
	Action  ask.Action
	Handler http.HandlerFunc
	Changes Change // the thing a change route changes, for a feature's module to hear of without the handler calling it
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
	Name     string                                 // ran.name of every answer: the thing served; empty is the app's ID
	NameFor  func(*http.Request) string             // ran.name of an answer about one of many things served, such as a project in its path (r.PathValue); nil or "" is Name, as are the refusals given before a route is found
	Routes   []Route                                // every API route but GET /api/version, which serve answers
	UI       http.Handler                           // the page and its files at /; nil serves the API alone
	Styles   func() (map[ask.Type]ask.Style, error) // the user's asking style per type, read on every change; nil is every default
	Remember func(ask.Type) error                   // saves never for a type after "Remember my answer" (confirm=remember), in the app's config, such as ask.Remember
	Features *features.List                         // the app's feature list; the Part attached to a feature adds routes and hears of changes while it is on
	Needs    []features.Value                       // the values the app's core cannot run without; serve refuses to start while one is missing (G-CODE-8)
	Lookup   features.Lookup                        // finds a value of Needs; nil finds none, so any Needs refuses the start
	Log      io.Writer                              // the app's log, for what goes wrong after an answer is sent; nil is standard error
}

// VersionData is the data of the GET /api/version answer.
type VersionData struct {
	App       string   `json:"app"`
	Version   string   `json:"version"`
	Contract  string   `json:"contract"`
	Contracts []string `json:"contracts"`
}

var writing = map[string]bool{http.MethodPost: true, http.MethodPut: true, http.MethodPatch: true, http.MethodDelete: true}

// Handler serves site behind the guard: only on this machine's address or one of hosts, the page only with token's cookie,
// every API request with token, from the page itself, with a JSON body and a contract the app answers, and a change only with the user's yes.
func (a App) Handler(site Site, token string, hosts []string) (http.Handler, error) {
	if err := a.check(); err != nil {
		return nil, err
	}
	if token == "" {
		return nil, fmt.Errorf("serve: %s serves only with a token", a.command())
	}
	if missing := features.Missing(site.Needs, site.Lookup); len(missing) > 0 {
		return nil, Fail(CodeCoreNeedsValues, fmt.Sprintf("%s cannot start: it needs values that are not set: %s", a.command(), features.Names(missing)))
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/version", func(w http.ResponseWriter, r *http.Request) {
		s := answering(r)
		Write(w, r, http.StatusOK, VersionKind, VersionData{App: a.ID, Version: a.Version, Contract: s.contract, Contracts: a.Contracts})
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
	for _, rt := range site.Routes {
		if err := add(rt, a.gate(site, rt, parts)); err != nil {
			return nil, err
		}
	}
	for _, p := range parts {
		routes, err := p.part.Mount(site.Routes)
		if err != nil {
			return nil, fmt.Errorf("serve: feature %s: %w", p.id, err)
		}
		for _, rt := range routes {
			if err := add(rt, a.feature(site.Features, p.id, a.gate(site, rt, nil))); err != nil {
				return nil, err
			}
		}
	}
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		WriteError(w, r, http.StatusNotFound, Fail(CodeNotFound, fmt.Sprintf("%s has no %s %s", a.command(), r.Method, r.URL.Path)))
	})
	if site.UI != nil {
		mux.Handle("/", site.UI)
	}
	return a.guard(token, hosts, site.Name, a.choose(site.Name, site.NameFor, mux)), nil
}

// Write answers kind with data, in the envelope and the contract the request was answered in.
func Write(w http.ResponseWriter, r *http.Request, status int, kind string, data any) {
	if c, ok := r.Context().Value(answeredKey{}).(*Answered); ok {
		c.Status, c.Kind, c.Data = status, kind, data
	}
	s := answering(r)
	writeEnvelope(w, status, s.app.Answer(s.name, s.contract, kind, data))
}

// WriteError answers err with status, in the envelope; an err that is not an *Error gets CodeFailed.
func WriteError(w http.ResponseWriter, r *http.Request, status int, err error) {
	s := answering(r)
	writeEnvelope(w, status, s.app.Failure(s.name, s.contract, err))
}

func writeEnvelope(w http.ResponseWriter, status int, e Envelope) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = Encode(w, e)
}

// state is what an answer needs to say where it ran.
type state struct {
	app      App
	name     string
	nameFor  func(*http.Request) string
	contract string
}

type stateKey struct{}

// answering is the request's state, named for the thing r is about once routed; a handler called outside Handler answers as an app without a name.
func answering(r *http.Request) state {
	if s, ok := r.Context().Value(stateKey{}).(state); ok {
		if s.nameFor != nil {
			if name := s.nameFor(r); name != "" {
				s.name = name
			}
		}
		return s
	}
	return state{app: App{ID: "unknown", Version: "unknown"}, contract: "v1"}
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
func (a App) choose(name string, nameFor func(*http.Request) string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked := ""
		if strings.HasPrefix(r.URL.Path, "/api/") {
			asked = strings.TrimSpace(r.Header.Get(ContractHeader))
		}
		contract, err := a.Contract(asked)
		if err != nil {
			fallback, _ := a.Contract("")
			writeEnvelope(w, http.StatusNotAcceptable, a.Failure(name, fallback, err))
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), stateKey{}, state{app: a, name: name, nameFor: nameFor, contract: contract})))
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
			WriteError(w, r, http.StatusServiceUnavailable, Fail(features.CodeNeedsValues, nv.Error()))
			return
		}
		if err != nil {
			WriteError(w, r, http.StatusInternalServerError, Fail(CodeConfig, fmt.Sprintf("the feature %s cannot be read: %v", id, err)))
			return
		}
		if !on {
			WriteError(w, r, http.StatusNotFound, Fail(CodeNotFound, fmt.Sprintf("%s has no %s %s: the feature %s is off", a.command(), r.Method, r.URL.Path, id)))
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

// gate answers 428 to a change that came without the user's yes (confirm=yes, or remember to stop asking), unless the user picked never for its type.
func (a App) gate(site Site, rt Route, parts []part) http.Handler {
	if !writing[rt.method()] {
		return rt.Handler
	}
	log := site.Log
	if log == nil {
		log = os.Stderr
	}
	rt.Handler = told(rt.Handler, rt, site.Features, parts, log)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get(ConfirmParam) {
		case ConfirmYes:
			rt.Handler(w, r)
			return
		case ConfirmRemember:
			// A change to the system itself is never remembered (G-STACK-6); without Site.Remember the app keeps no answer, so remember is a yes.
			if site.Remember != nil && !rt.Action.System {
				if err := site.Remember(rt.Action.Type); err != nil {
					WriteError(w, r, http.StatusInternalServerError, Fail(CodeConfig, fmt.Sprintf(
						"nothing was changed: the answer for %s could not be saved: %v; send it again with %s=%s", rt.Action.Type, err, ConfirmParam, ConfirmYes)))
					return
				}
			}
			rt.Handler(w, r)
			return
		}
		styles := map[ask.Type]ask.Style{}
		if site.Styles != nil {
			read, err := site.Styles()
			if err != nil {
				WriteError(w, r, http.StatusInternalServerError, Fail(CodeConfig, err.Error()))
				return
			}
			styles = read
		}
		// G-STACK-13: the user may pick "not at all" for a type; Resolve never gives that for a change to the system itself.
		if style, _ := ask.Resolve(rt.Action, styles); style != ask.Never {
			what := string(rt.Action.Type)
			if rt.Action.What != "" {
				what = rt.Action.What
			}
			WriteError(w, r, http.StatusPreconditionRequired, Fail(CodeUnconfirmed, fmt.Sprintf(
				"nothing was changed: %s asks first; send it again with %s=%s once the user said yes", what, ConfirmParam, ConfirmYes)))
			return
		}
		rt.Handler(w, r)
	})
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
	case !writing[method] && rt.Changes.Thing != "":
		return fmt.Errorf("serve: read %q changes nothing", rt.Pattern)
	case rt.Changes.Thing != "" && rt.Changes.ID == nil:
		return fmt.Errorf("serve: change %q says the thing it changes but not how to find its id", rt.Pattern)
	}
	return nil
}
