package cli

import (
	"io"
	"net/http"

	"github.com/tugbayatilla/mankit/v4/ask"
	"github.com/tugbayatilla/mankit/v4/features"
	"github.com/tugbayatilla/mankit/v4/licenses"
	"github.com/tugbayatilla/mankit/v4/serve"
)

// Path is the thing license over HTTP (G-STACK-4).
const Path = "/api/license"

// Routes are the thing license on the app's server, which every app adds to its Site's routes beside mounting Command, with the same c and list; each calls the Checker as the CLI does (ADR-L8):
// GET Path answers LicenseShow as `license show` does; POST Path takes a license file as its body and keeps it as `license add <file>` does; DELETE Path takes away what `license remove` would.
// A change asks first, as serve asks every change (a Question, then confirm=<id>, or confirm=yes with Bearer), with the words the CLI asks; a file that cannot be kept, or nothing to remove, is answered without a question.
func Routes(c *licenses.Checker, list *features.List) []serve.Route {
	show := func(w http.ResponseWriter, r *http.Request) {
		s, err := c.Show(list)
		if err != nil {
			serve.WriteError(w, r, serve.StatusOf(err), err)
			return
		}
		serve.Write(w, r, http.StatusOK, licenses.ShowKind, s)
	}
	return []serve.Route{
		{Pattern: http.MethodGet + " " + Path, Handler: show},
		{
			Pattern: http.MethodPost + " " + Path,
			Action:  ask.Action{Type: ask.Settings, What: "keep a license, read by every app", Name: "license"},
			ActionFor: func(r *http.Request) (ask.Action, bool) {
				raw, err := io.ReadAll(r.Body)
				if err != nil {
					return ask.Action{}, false
				}
				action, err := adding(c, raw)
				return action, err == nil
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
				show(w, r)
			},
		},
		{
			Pattern: http.MethodDelete + " " + Path,
			Action:  ask.Action{Type: ask.Settings, What: "remove the license file; the trial stays", Name: "license"},
			ActionFor: func(*http.Request) (ask.Action, bool) {
				_, action, err := removing(c)
				return action, err == nil
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
				show(w, r)
			},
		},
	}
}
