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
// GET Path answers LicenseShow as `license show` does; POST Path takes a license file as its body and keeps it as `license add <file>` does, with org=true for --org; POST Path/{id}/use makes the license active as `license use <id>` does; DELETE Path/{id} takes it away as `license remove <id>` does.
// A change asks first, as serve asks every change (a Question, then confirm=<id>, or confirm=yes with Bearer), with the words the CLI asks; only a file refused, or a license that is not there, is answered without a question, and any other failure asks with the route's own words, so nothing is written without a yes (ADR-S04).
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
	// changed answers a change that was made: the show, or an error saying the change stands when the show fails; a failed change is answered with its error.
	changed := func(w http.ResponseWriter, r *http.Request, done string, err error) {
		if err != nil {
			serve.WriteError(w, r, serve.StatusOf(err), err)
			return
		}
		if _, err := c.Show(list); err != nil {
			err = problem.Fail(problem.TypeFailed, "", "the license was "+done+", but showing it failed: "+err.Error())
			serve.WriteError(w, r, serve.StatusOf(err), err)
			return
		}
		show(w, r)
	}
	// asks is the action a change asks, from the CLI's words, or the route's own when they cannot be made: no question at all for a refused file or a license not there (quiet).
	asks := func(fallback ask.Action, action ask.Action, err error, quiet string) (ask.Action, bool) {
		if err != nil {
			return fallback, !is(err, quiet)
		}
		return action, true
	}
	org := func(r *http.Request) bool { return r.URL.Query().Get("org") == "true" }
	add := ask.Action{Type: ask.Settings, What: "keep a license and make it the active license of this app", Name: "license"}
	use := ask.Action{Type: ask.Settings, What: "make a license the active license of this app", Name: "license"}
	remove := ask.Action{Type: ask.Settings, What: "remove a license; the trial stays", Name: "license"}
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
				action, err := adding(c, raw, org(r))
				return asks(add, action, err, problem.TypeRefused)
			},
			Handler: func(w http.ResponseWriter, r *http.Request) {
				raw, err := io.ReadAll(r.Body)
				if err == nil {
					_, err = c.Add(r.Context(), raw, org(r))
				}
				changed(w, r, "kept", err)
			},
		},
		{
			Pattern: http.MethodPost + " " + Path + "/{id}/use",
			Action:  use,
			ActionFor: func(r *http.Request) (ask.Action, bool) {
				action, err := using(c, r.PathValue("id"))
				if is(err, problem.TypeNotFound) {
					return use, false
				}
				return asks(use, action, err, problem.TypeRefused)
			},
			Handler: func(w http.ResponseWriter, r *http.Request) {
				_, err := c.Use(r.Context(), r.PathValue("id"))
				changed(w, r, "made active", err)
			},
		},
		{
			Pattern: http.MethodDelete + " " + Path + "/{id}",
			Action:  remove,
			ActionFor: func(r *http.Request) (ask.Action, bool) {
				_, action, err := removing(c, r.PathValue("id"))
				return asks(remove, action, err, problem.TypeNotFound)
			},
			Handler: func(w http.ResponseWriter, r *http.Request) {
				shown, err := c.Removing(r.PathValue("id"))
				if err == nil {
					err = c.Remove(r.Context(), shown)
				}
				changed(w, r, "removed", err)
			},
		},
	}
}

// is reports whether err is a Problem of type typ.
func is(err error, typ string) bool {
	var p *problem.Problem
	return errors.As(err, &p) && p.Type == typ
}
