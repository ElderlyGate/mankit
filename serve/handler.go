package serve

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/tugbayatilla/mankit/ask"
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
}

// Site is what an app serves: its routes, its page, and the thing it serves.
type Site struct {
	Name     string                                 // ran.name of every answer: the thing served; empty is the app's ID
	Routes   []Route                                // every API route but GET /api/version, which serve answers
	UI       http.Handler                           // the page and its files at /; nil serves the API alone
	Styles   func() (map[ask.Type]ask.Style, error) // the user's asking style per type, read on every change; nil is every default
	Remember func(ask.Type) error                   // saves never for a type after "Remember my answer" (confirm=remember), in the app's config, such as ask.Remember
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
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/version", func(w http.ResponseWriter, r *http.Request) {
		s := answering(r)
		Write(w, r, http.StatusOK, VersionKind, VersionData{App: a.ID, Version: a.Version, Contract: s.contract, Contracts: a.Contracts})
	})
	seen := map[string]bool{}
	for _, rt := range site.Routes {
		if err := rt.check(); err != nil {
			return nil, err
		}
		if seen[rt.Pattern] {
			return nil, fmt.Errorf("serve: route %q is given twice", rt.Pattern)
		}
		seen[rt.Pattern] = true
		mux.Handle(rt.Pattern, a.gate(site, rt))
	}
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		WriteError(w, r, http.StatusNotFound, Fail(CodeNotFound, fmt.Sprintf("%s has no %s %s", a.command(), r.Method, r.URL.Path)))
	})
	if site.UI != nil {
		mux.Handle("/", site.UI)
	}
	return a.guard(token, hosts, site.Name, a.choose(site.Name, mux)), nil
}

// Write answers kind with data, in the envelope and the contract the request was answered in.
func Write(w http.ResponseWriter, r *http.Request, status int, kind string, data any) {
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
	contract string
}

type stateKey struct{}

// answering is the request's state; a handler called outside Handler answers as an app without a name.
func answering(r *http.Request) state {
	if s, ok := r.Context().Value(stateKey{}).(state); ok {
		return s
	}
	return state{app: App{ID: "unknown", Version: "unknown"}, contract: "v1"}
}

type stoppingKey struct{}

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
		if err != nil {
			fallback, _ := a.Contract("")
			writeEnvelope(w, http.StatusNotAcceptable, a.Failure(name, fallback, err))
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), stateKey{}, state{app: a, name: name, contract: contract})))
	})
}

// gate answers 428 to a change that came without the user's yes (confirm=yes, or remember to stop asking), unless the user picked never for its type.
func (a App) gate(site Site, rt Route) http.Handler {
	if !writing[rt.method()] {
		return rt.Handler
	}
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
	}
	return nil
}
