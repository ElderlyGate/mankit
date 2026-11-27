package cli

import (
	"errors"
	"io"
	"net/http"

	"github.com/tugbayatilla/mankit/v4/ask"
	"github.com/tugbayatilla/mankit/v4/features"
	"github.com/tugbayatilla/mankit/v4/licenses"
	"github.com/tugbayatilla/mankit/v4/problem"
	"github.com/tugbayatilla/mankit/v4/serve"
)

// Path is the thing license over HTTP (G-STACK-4).
const Path = "/api/license"

// Routes are the thing license on the app's server, which every app adds to its Site's routes beside mounting Command, with the same c and list; each calls the Checker as the CLI does (ADR-L8):
// GET Path answers LicenseShow as `license show` does; POST Path takes a license file as its body and keeps it as `license add <file>` does; DELETE Path takes away what `license remove` would.
// A change asks first, as serve asks every change (a Question, then confirm=<id>, or confirm=yes with Bearer), with the words the CLI asks; only a file refused, or nothing to remove, is answered without a question, and any other failure asks with the route's own words, so nothing is written without a yes (ADR-S04).
// The yes covers the request, not what was shown: a license file replaced between the question and the yes is removed as it is then.
func Routes(c *licenses.Checker, list *features.List) []serve.Route {
	show := func(w http.ResponseWriter, r *http.Request) {
		s, err := c.Show(list)
		if err != nil {
			serve.WriteError(w, r, serve.StatusOf(err), err)
			return
		}
		serve.Write(w, r, http.StatusOK, licenses.ShowKind, s)
	}
	// changed answers a change that was made: the show, or an error saying the change stands when the show fails.
	changed := func(w http.ResponseWriter, r *http.Request, done string) {
		if _, err := c.Show(list); err != nil {
			err = problem.Fail(problem.TypeFailed, "", "the license was "+done+", but showing it failed: "+err.Error())
			serve.WriteError(w, r, serve.StatusOf(err), err)
			return
		}
		show(w, r)
	}
	add := ask.Action{Type: ask.Settings, What: "keep a license, read by every app", Name: "license"}
	remove := ask.Action{Type: ask.Settings, What: "remove the license file; the trial stays", Name: "license"}
	return []serve.Route{
		{Pattern: http.MethodGet + " " + Path, Handler: show},
		{
			Pattern: http.MethodPost + " " + Path,
			Action:  add,
			ActionFor: func(r *http.Request) (ask.Action, bool) {
				raw, err := io.ReadAll(r.Body)
				if err != nil {
					return add, true
				}
				action, err := adding(c, raw)
				if err != nil {
					return add, !is(err, problem.TypeRefused)
				}
				return action, true
			},
			Handler: func(w http.ResponseWriter, r *http.Request) {
				raw, err := io.ReadAll(r.Body)
				if err == nil {
					_, err = c.Add(r.Context(), raw, true)
				}
				if err != nil {
					serve.WriteError(w, r, serve.StatusOf(err), err)
					return
				}
				changed(w, r, "kept")
			},
		},
		{
			Pattern: http.MethodDelete + " " + Path,
			Action:  remove,
			ActionFor: func(*http.Request) (ask.Action, bool) {
				_, action, err := removing(c)
				if err != nil {
					return remove, !is(err, problem.TypeNotFound)
				}
				return action, true
			},
			Handler: func(w http.ResponseWriter, r *http.Request) {
				shown, err := c.Removing()
				if err == nil {
					err = c.Remove(r.Context(), shown)
				}
				if err != nil {
					serve.WriteError(w, r, serve.StatusOf(err), err)
					return
				}
				changed(w, r, "removed")
			},
		},
	}
}

// is reports whether err is a Problem of type typ.
func is(err error, typ string) bool {
	var p *problem.Problem
	return errors.As(err, &p) && p.Type == typ
}
