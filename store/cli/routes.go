package cli

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/tugbayatilla/mankit/v4/ask"
	"github.com/tugbayatilla/mankit/v4/problem"
	"github.com/tugbayatilla/mankit/v4/serve"
	"github.com/tugbayatilla/mankit/v4/store"
)

// The paths of store unfinished over HTTP (G-STACK-4).
const (
	UnfinishedPath = "/api/store/unfinished"
	// ClearPath takes the thing; each mark to clear is named by an id parameter, as list shows it.
	ClearPath = UnfinishedPath + "/{thing}"
)

// UnfinishedRoutes are store unfinished on the app's server, which every app with a store scope adds to its Site's routes beside mounting UnfinishedCommand:
// GET UnfinishedPath[?thing=] answers StoreUnfinishedList as list does, and DELETE ClearPath?id=…, a delete of the system itself that serve asks for first (G-STACK-6, G-STACK-13), removes the marks named that still hold thing and answers StoreUnfinishedCleared, as clear does.
// A client shows the changes list gave and asks for the thing's name before it sends confirm=yes; remember never silences the question, as for every change to the system itself.
func UnfinishedRoutes(open func() (*store.Scope, error)) []serve.Route {
	return []serve.Route{
		{Pattern: http.MethodGet + " " + UnfinishedPath, Handler: func(w http.ResponseWriter, r *http.Request) {
			data, err := listed(open, r.URL.Query().Get("thing"))
			if err != nil {
				serve.WriteError(w, r, statusOf(err), err)
				return
			}
			serve.Write(w, r, http.StatusOK, UnfinishedListKind, data)
		}},
		{
			Pattern: http.MethodDelete + " " + ClearPath,
			// Not a change to the system itself here, which would ask even a request naming no mark; ActionFor makes every clear one.
			Action: ask.Action{Type: ask.Delete, What: "clear changes cut short"},
			ActionFor: func(r *http.Request) (ask.Action, bool) {
				thing := r.PathValue("thing")
				// A request naming no mark is refused before anything is asked.
				if len(r.URL.Query()["id"]) == 0 {
					return ask.Action{Type: ask.Delete}, false
				}
				return ask.Action{Type: ask.Delete, System: true, Name: thing, What: fmt.Sprintf(
					"clear the changes cut short holding %s (marks %s); nothing is put back", thing, strings.Join(r.URL.Query()["id"], ", "))}, true
			},
			Handler: func(w http.ResponseWriter, r *http.Request) {
				thing, err := store.ParseThing(r.PathValue("thing"))
				if err != nil {
					serve.WriteError(w, r, statusOf(err), err)
					return
				}
				marks := r.URL.Query()["id"]
				if len(marks) == 0 {
					serve.WriteError(w, r, http.StatusBadRequest, problem.FailAt(problem.TypeRefused, "", string(thing),
						"nothing was cleared: name each mark to clear with id=, as "+UnfinishedPath+" shows them, so only the changes a person saw are cleared"))
					return
				}
				s, err := open()
				if err != nil {
					serve.WriteError(w, r, statusOf(err), err)
					return
				}
				defer s.Close()
				data, err := clearShown(r.Context(), s, thing, marks)
				if err != nil {
					serve.WriteError(w, r, statusOf(err), err)
					return
				}
				serve.Write(w, r, http.StatusOK, ClearedKind, data)
			},
		},
	}
}

// statusOf is the HTTP status of err: not_found 404, refused 400, anything else the server's own failure.
func statusOf(err error) int {
	switch problemType(err) {
	case problem.TypeNotFound:
		return http.StatusNotFound
	case problem.TypeRefused:
		return http.StatusBadRequest
	}
	return http.StatusInternalServerError
}
