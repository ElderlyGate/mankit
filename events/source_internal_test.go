package events

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// broker of the shop on main, with thingFor, its log kept.
func sourceBroker(thingFor func(*http.Request) string) (*broker, *bytes.Buffer) {
	log := &bytes.Buffer{}
	return &broker{cfg: Config{Product: "shop", Thing: "main", ThingFor: thingFor}, source: "/shop/main", log: log}, log
}

// The project a real routed request names, as the README shows it: r.PathValue("project").
func TestThingForReadsTheRoutedRequest(t *testing.T) {
	b, _ := sourceBroker(func(r *http.Request) string { return r.PathValue("project") })
	var got string
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/projects/{project}/items/{id}", func(w http.ResponseWriter, r *http.Request) { got = b.sourceOf(r) })
	mux.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("POST", "/api/projects/alpha/items/item-1", nil))
	if got != "/shop/alpha" {
		t.Fatalf("source %q, want /shop/alpha", got)
	}
}

// An empty, bad or failing answer keeps the non-empty Thing; a bad one is said once, cut to 64 characters.
func TestABadThingKeepsThingAndIsSaidOnce(t *testing.T) {
	answer := ""
	b, log := sourceBroker(func(*http.Request) string {
		if answer == "boom" {
			panic("no project")
		}
		return answer
	})
	r := httptest.NewRequest("POST", "/api/items/x", nil)
	long := strings.Repeat("a", 65)
	for _, a := range []string{"", "a/b", ".", "..", long, "boom", "a/b", long, "boom"} {
		answer = a
		if got := b.sourceOf(r); got != "/shop/main" {
			t.Fatalf("answer %q: source %q, want /shop/main", a, got)
		}
	}
	said := log.String()
	for _, want := range []string{`"a/b"`, `"."`, `".."`, `"` + strings.Repeat("a", 64) + `…"`, `"no project"`} {
		if strings.Count(said, want) != 1 {
			t.Errorf("log says %s %d times, want once:\n%s", want, strings.Count(said, want), said)
		}
	}
	if strings.Contains(said, long) {
		t.Errorf("the log holds the whole long answer:\n%s", said)
	}
	answer = strings.Repeat("a", 64)
	if got := b.sourceOf(r); got != "/shop/"+answer {
		t.Fatalf("a thing of 64 is taken: %q", got)
	}
}
