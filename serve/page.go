package serve

import (
	"fmt"
	"html/template"
	"mime"
	"net/http"
	"strconv"
	"strings"

	"github.com/tugbayatilla/mankit/v4/problem"
)

// wantsPage says whether r comes from a person's browser, which reads a page, rather than a program, which reads the envelope: a navigation, or an Accept that ranks text/html above application/json, as a browser's does.
// A program, such as the app's own page fetching /api/, curl or a health check, asks with */*, for JSON or with no Accept, and keeps the envelope on every path.
func wantsPage(r *http.Request) bool {
	if r.Header.Get("Sec-Fetch-Mode") == "navigate" {
		return true
	}
	accept := r.Header.Get("Accept")
	return quality(accept, "text", "html") > quality(accept, "application", "json")
}

// quality is the weight accept gives the type kind/sub, by its most precise entry (text/html, then text/*, then */*), 0 when none takes it.
func quality(accept, kind, sub string) float64 {
	best, precision := 0.0, -1
	for _, part := range strings.Split(accept, ",") {
		t, params, err := mime.ParseMediaType(strings.TrimSpace(part))
		if err != nil {
			continue
		}
		k, s, _ := strings.Cut(t, "/")
		p := -1
		switch {
		case k == kind && s == sub:
			p = 2
		case k == kind && s == "*":
			p = 1
		case k == "*" && s == "*":
			p = 0
		}
		if p < precision || p < 0 {
			continue
		}
		q := 1.0
		if v, ok := params["q"]; ok {
			if f, err := strconv.ParseFloat(v, 64); err == nil {
				q = f
			}
		}
		if p > precision || q > best {
			best, precision = q, p
		}
	}
	return best
}

// refusal is what a page says for one reason: a short title, why it happens and what to do, with %s for the app's command.
type refusal struct{ title, why, todo string }

// refusals are the pages of the reasons a person meets in a browser; any other gets a page from the Problem's type alone.
var refusals = map[string]refusal{
	ReasonHost: {"This address is not one the server answers on",
		"The server answers only on this machine's own address and the names it was started with, so another site cannot reach it through yours.",
		"Open it at this machine's own address, such as http://localhost:<port>, or start the server with `%s serve --host <name>` for the name you used."},
	ReasonToken: {"Open this page from its link",
		"The server asks every request for its token, which only the link it prints carries, so nobody else on this machine or network can use it.",
		"Run `%s server link` and open the link it prints."},
	ReasonOrigin: {"This request did not come from the app's own page",
		"The server takes requests only from its own page, so another site cannot act on it in your name.",
		"Open the app's own page and use it from there."},
	problem.TypeUnsupportedContract: {"This contract version is not served",
		"The server answers only in the contract versions it serves, so a reader always gets a form it knows.",
		"Ask again with a version it serves: GET /api/version lists them."},
	ReasonNotJSON: {"The request body is not JSON",
		"The server takes a request body as JSON only.",
		"Send it again with Content-Type: application/json."},
	ReasonTooLarge: {"The request is too large",
		fmt.Sprintf("The server takes a request body of up to %d bytes, so one request cannot wear it out.", maxBody),
		"Send a smaller body."},
	ReasonConfigUnreadable: {"The server cannot read its own settings",
		"A file the server needs, such as its token, cannot be read or is open to other users, so it lets nobody in until it is fixed.",
		"Fix the file the detail names, then open the link again."},
}

// byType is the title of a page for a reason with no page of its own.
var byType = map[string]string{
	problem.TypeRefused:  "The request was refused",
	problem.TypeNotFound: "Nothing is here",
	problem.TypeFailed:   "Something went wrong",
}

// page is the readable page a browser gets for p: what went wrong, why and what to do, built from the Problem and the app's command alone, so nothing from the request, such as a token in the link, ever reaches it.
var page = template.Must(template.New("page").Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.Title}}</title>
<style>
:root{color-scheme:light dark;--bg:#f7f7f5;--fg:#1d2420;--muted:#5d6a63;--line:#d9ded9;--card:#fff}
@media (prefers-color-scheme:dark){:root{--bg:#141917;--fg:#e4ebe6;--muted:#9aa8a0;--line:#2f3a35;--card:#1c2320}}
body{margin:0;background:var(--bg);color:var(--fg);font:16px/1.5 system-ui,-apple-system,"Segoe UI",sans-serif}
main{max-width:40rem;margin:12vh auto;padding:0 16px}
section{background:var(--card);border:1px solid var(--line);border-radius:10px;padding:20px 24px}
h1{font-size:1.4rem;margin:0 0 12px}
h2{font-size:.8rem;letter-spacing:.06em;text-transform:uppercase;color:var(--muted);margin:16px 0 4px}
p{margin:0}code{font:.92em ui-monospace,monospace}
footer{color:var(--muted);font-size:.85rem;margin-top:12px}
</style></head>
<body><main><section>
<h1>{{.Title}}</h1>
<h2>What went wrong</h2><p>{{.Detail}}</p>
{{if .Why}}<h2>Why</h2><p>{{.Why}}</p>{{end}}
{{if .Todo}}<h2>What to do</h2><p>{{.Todo}}</p>{{end}}
</section><footer>{{.App}} · {{.Word}} · {{.Status}}</footer></main></body></html>
`))

// writePage answers a browser with the page for p, in place of the envelope a program gets, with the same status.
func writePage(w http.ResponseWriter, status int, command string, p problem.Problem) {
	word := p.Reason
	if word == "" {
		word = p.Type
	}
	r, ok := refusals[word]
	if !ok {
		r = refusal{title: byType[p.Type]}
		if r.title == "" {
			r.title = byType[problem.TypeFailed]
		}
	}
	todo := strings.ReplaceAll(r.todo, "%s", command)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.WriteHeader(status)
	_ = page.Execute(w, map[string]any{"Title": r.title, "Detail": p.Detail, "Why": r.why, "Todo": todo,
		"App": command, "Word": word, "Status": status})
}

// writeRefusal answers err with status: a page for a browser, the envelope in contract for a program.
func (a App) writeRefusal(w http.ResponseWriter, r *http.Request, status int, name, contract string, err error) {
	if wantsPage(r) {
		writePage(w, status, a.command(), problemOf(err))
		return
	}
	writeEnvelope(w, status, a.Failure(name, contract, err))
}
