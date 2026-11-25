package events_test

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"go.naywee.com/contracts/v2"

	"go.naywee.com/mankit/v5/ask"
	"go.naywee.com/mankit/v5/events"
	"go.naywee.com/mankit/v5/features"
	"go.naywee.com/mankit/v5/home"
	"go.naywee.com/mankit/v5/problem"
	"go.naywee.com/mankit/v5/serve"
)

var app = serve.App{ID: "shop", Version: "1.0.0", Contracts: []serve.Contract{{Version: "v1"}}}

const token = "s3cret"

// serveHandler is a's handler on a machine of the test's own, whose token is token.
func serveHandler(t *testing.T, a serve.App, s serve.Site, hosts []string) (http.Handler, error) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "data")
	t.Setenv("XDG_DATA_HOME", dir)
	servers := filepath.Join(dir, home.Org, home.Folder(a.ID), "servers")
	if err := os.MkdirAll(servers, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(servers, "token"), []byte(token+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return a.Handler(s, hosts)
}

var catalog = []string{"shop.item.added", "shop.item.removed"}

type item struct {
	ID string `json:"id"`
}

var (
	serveCreate = ask.Action{Type: ask.Create}
	serveDelete = ask.Action{Type: ask.Delete}
	serveEdit   = ask.Action{Type: ask.Edit}
)

func itemID(data any) string { return data.(item).ID }

// routes are the shop's routes, the same with events and without: each change says the thing it changes, and nothing about events.
func routes() []serve.Route {
	changes := serve.Change{Thing: "item", ID: itemID}
	answer := func(status int) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if r.PathValue("id") == "missing" {
				serve.WriteError(w, r, http.StatusNotFound, problem.Fail(problem.TypeNotFound, "", "no such item"))
				return
			}
			serve.Write(w, r, status, "Item", item{ID: r.PathValue("id")})
		}
	}
	return []serve.Route{
		{Pattern: "POST /api/items/{id}", Action: serveCreate, Changes: changes, Handler: answer(http.StatusCreated)},
		{Pattern: "DELETE /api/items/{id}", Action: serveDelete, Changes: changes, Handler: answer(http.StatusOK)},
		{Pattern: "PUT /api/items/{id}", Action: serveEdit, Changes: changes, Handler: answer(http.StatusOK)},
		{Pattern: "PATCH /api/items/{id}", Action: serveEdit, Changes: changes, Handler: answer(http.StatusOK)},
		{Pattern: "POST /api/nameless", Action: serveCreate, Changes: serve.Change{Thing: "item", ID: func(any) string { return "" }}, Handler: answer(http.StatusCreated)},
	}
}

// causes are the events the shop's changes cause: PUT carries a large data, PATCH one that is not JSON, nameless no subject.
var causes = map[string]events.Cause{
	"POST /api/items/{id}":   {Type: "shop.item.added"},
	"DELETE /api/items/{id}": {Type: "shop.item.removed", Data: func(any) any { return nil }},
	"PUT /api/items/{id}":    {Type: "shop.item.added", Data: func(any) any { return strings.Repeat("x", 128<<10) }},
	"PATCH /api/items/{id}":  {Type: "shop.item.added", Data: func(any) any { return func() {} }},
	"POST /api/nameless":     {Type: "shop.item.added"},
}

func keep(n int) func(*events.Config) { return func(c *events.Config) { c.Keep = n } }

// switches is the owner's switches, held in the test as an app's config would hold them.
type switches struct {
	mu         sync.Mutex
	off        []string
	on         []string
	fail       error
	unlicensed bool
}

// license is a closer that is not the owner's switch, as a license would be: it closes events while unlicensed is set.
func (s *switches) license() features.Closer {
	return features.Closer{By: features.License, Closed: func() ([]string, error) {
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.unlicensed {
			return []string{events.Feature.ID}, nil
		}
		return nil, nil
	}}
}

func (s *switches) licensed(on bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.unlicensed = !on
}

func (s *switches) store() features.Store {
	return features.Store{
		Off: func() ([]string, error) {
			s.mu.Lock()
			defer s.mu.Unlock()
			return slices.Clone(s.off), s.fail
		},
		Keep: func(id string, off bool) error {
			s.mu.Lock()
			defer s.mu.Unlock()
			s.off = slices.DeleteFunc(s.off, func(o string) bool { return o == id })
			s.on = slices.DeleteFunc(s.on, func(o string) bool { return o == id })
			if off {
				s.off = append(s.off, id)
			} else {
				s.on = append(s.on, id)
			}
			return nil
		},
		On: func() ([]string, error) {
			s.mu.Lock()
			defer s.mu.Unlock()
			return slices.Clone(s.on), nil
		},
	}
}

func (s *switches) failing(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.fail = err
}

type shop struct {
	list *features.List
	sw   *switches
	url  string
}

// shopHandler is the shop's handler; with events, its list holds the feature and events is attached to it, and that is the only difference.
func shopHandler(t *testing.T, withEvents bool, tweak ...func(*events.Config)) (http.Handler, *features.List, *switches) {
	t.Helper()
	sw := &switches{}
	var all []features.Feature
	if withEvents {
		all = append(all, events.Feature)
	}
	list := features.New(all, sw.store(), sw.license())
	if withEvents {
		cfg := events.Config{Product: "shop", Thing: "main", Catalog: catalog, Causes: causes, Heartbeat: 20 * time.Millisecond}
		for _, f := range tweak {
			f(&cfg)
		}
		if err := events.Attach(list, cfg); err != nil {
			t.Fatal(err)
		}
		if err := list.Set(events.Feature.ID, true); err != nil {
			t.Fatal(err)
		}
	}
	h, err := serveHandler(t, app, serve.Site{Routes: routes(), Features: list}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return h, list, sw
}

// serving is the shop behind serve's guard, as an app serves it.
func serving(t *testing.T, withEvents bool, tweak ...func(*events.Config)) *shop {
	t.Helper()
	h, list, sw := shopHandler(t, withEvents, tweak...)
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return &shop{list: list, sw: sw, url: srv.URL}
}

func (s *shop) do(t *testing.T, method, path string, header map[string]string) (*http.Response, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	req, _ := http.NewRequestWithContext(ctx, method, s.url+path, nil)
	for k, v := range header {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	return resp, cancel
}

// change makes a change with the user's yes and the token, as the page does, and gives the status.
func (s *shop) change(t *testing.T, method, path string, header map[string]string) int {
	t.Helper()
	h := map[string]string{"Authorization": "Bearer " + token}
	for k, v := range header {
		h[k] = v
	}
	resp, cancel := s.do(t, method, path+"?confirm=yes", h)
	defer cancel()
	resp.Body.Close()
	return resp.StatusCode
}

func (s *shop) add(t *testing.T, id string) {
	t.Helper()
	if status := s.change(t, http.MethodPost, "/api/items/"+id, nil); status != http.StatusCreated {
		t.Fatalf("adding %s answered %d", id, status)
	}
}

// stream is an open GET /api/events.
type stream struct {
	resp *http.Response
	r    *bufio.Reader
	stop context.CancelFunc
}

func (s *shop) open(t *testing.T, lastID string) *stream {
	t.Helper()
	header := map[string]string{"Authorization": "Bearer " + token}
	if lastID != "" {
		header["Last-Event-ID"] = lastID
	}
	resp, cancel := s.do(t, http.MethodGet, events.Path, header)
	t.Cleanup(func() { cancel(); resp.Body.Close() })
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("status %d, Content-Type %q", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	return &stream{resp: resp, r: bufio.NewReader(resp.Body), stop: cancel}
}

// next is the next event on the stream, checked against the contract (mankit/v2 for a gapped one) and its id: line; it skips heartbeat comments.
func (st *stream) next(t *testing.T) (events.Event, []byte) {
	t.Helper()
	var id, data string
	for {
		line, err := st.r.ReadString('\n')
		if err != nil {
			t.Fatalf("the stream ended: %v", err)
		}
		line = strings.TrimSuffix(line, "\n")
		switch {
		case line == "" && data != "":
			var e events.Event
			if err := json.Unmarshal([]byte(data), &e); err != nil {
				t.Fatal(err)
			}
			if e.ID != id {
				t.Fatalf("id: %q, the event's id %q", id, e.ID)
			}
			if err := contracts.Validate("event/v1/event.schema.json", []byte(data)); err != nil {
				t.Fatalf("%s: %v", data, err)
			}
			if strings.HasSuffix(e.Type, "."+events.Gapped) {
				if err := contracts.Validate("mankit/v2/stream.gapped.schema.json", []byte(data)); err != nil {
					t.Fatalf("%s: %v", data, err)
				}
			}
			return e, []byte(data)
		case strings.HasPrefix(line, "id: "):
			id = strings.TrimPrefix(line, "id: ")
		case strings.HasPrefix(line, "data: "):
			data = strings.TrimPrefix(line, "data: ")
		}
	}
}

func (st *stream) ids(t *testing.T, n int) []string {
	t.Helper()
	var out []string
	for range n {
		e, _ := st.next(t)
		out = append(out, e.ID)
	}
	return out
}

// ended says whether the stream ends without another event; a stream still open after a few seconds fails the test.
func (st *stream) ended(t *testing.T) bool {
	t.Helper()
	done := make(chan bool, 1)
	go func() {
		for {
			line, err := st.r.ReadString('\n')
			if err != nil {
				done <- true
				return
			}
			if strings.HasPrefix(line, "data: ") {
				done <- false
				return
			}
		}
	}()
	select {
	case ended := <-done:
		return ended
	case <-time.After(5 * time.Second):
		t.Fatal("the stream is still open")
		return false
	}
}

func refusal(t *testing.T, resp *http.Response, status int, typ, reason string) {
	t.Helper()
	var env serve.Envelope
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != status || len(env.Errors) != 1 || env.Errors[0].Type != typ || env.Errors[0].Reason != reason {
		t.Fatalf("status %d, errors %+v; want %d %s %s", resp.StatusCode, env.Errors, status, typ, reason)
	}
}

// Success

func TestAChangeSendsItsEventToEveryOpenStream(t *testing.T) {
	s := serving(t, true)
	a, b := s.open(t, ""), s.open(t, "")
	s.add(t, "item-1")
	for _, st := range []*stream{a, b} {
		e, _ := st.next(t)
		if e.Type != "shop.item.added" || e.Source != "/shop/main" || e.Subject != "item-1" || string(e.Data) != `{"id":"item-1"}` {
			t.Fatalf("event %+v", e)
		}
	}
}

// One server serving two projects names each event's project in its source (ADR-S08); without ThingFor the source stays /<product>/<thing>.
func TestEachEventNamesTheProjectItIsAbout(t *testing.T) {
	s := serving(t, true, func(c *events.Config) {
		c.ThingFor = func(r *http.Request) string { return r.Header.Get("X-Project") }
	})
	live := s.open(t, "")
	s.change(t, http.MethodPost, "/api/items/item-1", map[string]string{"X-Project": "alpha"})
	s.change(t, http.MethodPost, "/api/items/item-2", map[string]string{"X-Project": "beta"})
	s.change(t, http.MethodPost, "/api/items/item-3", nil)
	s.change(t, http.MethodPost, "/api/items/item-4", map[string]string{"X-Project": "a/b"})
	for _, want := range []string{"/shop/alpha", "/shop/beta", "/shop/main", "/shop/main"} {
		e, data := live.next(t)
		if e.Source != want {
			t.Fatalf("source %q, want %q", e.Source, want)
		}
		if err := contracts.Validate("event/v1/event.schema.json", data); err != nil {
			t.Fatalf("%s: %v", data, err)
		}
	}
}

func TestARouteMayShapeTheEventsData(t *testing.T) {
	s := serving(t, true)
	st := s.open(t, "")
	s.change(t, http.MethodDelete, "/api/items/item-1", nil)
	if e, _ := st.next(t); e.Type != "shop.item.removed" || string(e.Data) != "null" {
		t.Fatalf("event %+v", e)
	}
}

func TestAnIdleStreamGetsAHeartbeat(t *testing.T) {
	s := serving(t, true)
	st := s.open(t, "")
	if line, err := st.r.ReadString('\n'); err != nil || line != ": heartbeat\n" {
		t.Fatalf("line %q, %v", line, err)
	}
}

func TestTheTraceOfTheRequestIsCarried(t *testing.T) {
	s := serving(t, true)
	st := s.open(t, "")
	tp := "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	s.change(t, http.MethodPost, "/api/items/item-1", map[string]string{"traceparent": tp, "tracestate": "shop=1"})
	s.change(t, http.MethodPost, "/api/items/item-2", map[string]string{"traceparent": "00-" + strings.Repeat("0", 32) + "-00f067aa0ba902b7-01", "tracestate": "x"})
	if e, _ := st.next(t); e.TraceParent != tp || e.TraceState != "shop=1" {
		t.Fatalf("event %+v", e)
	}
	if e, _ := st.next(t); e.TraceParent == "" || strings.Contains(e.TraceParent, strings.Repeat("0", 32)) || e.TraceState != "" {
		t.Fatalf("an invalid traceparent was carried: %+v", e)
	}
}

func TestAnAppWithoutEventsServesTheSameRoutes(t *testing.T) {
	s := serving(t, false)
	s.add(t, "item-1")
	resp, cancel := s.do(t, http.MethodGet, events.Path, map[string]string{"Authorization": "Bearer " + token})
	defer cancel()
	defer resp.Body.Close()
	refusal(t, resp, http.StatusNotFound, problem.TypeNotFound, "")
}

// Contract

func TestEveryEventMeetsTheContract(t *testing.T) {
	s := serving(t, true, keep(1))
	live := s.open(t, "")
	s.add(t, "item-1")
	s.change(t, http.MethodPost, "/api/items/item-2", map[string]string{"traceparent": "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01", "tracestate": "shop=1"})
	s.change(t, http.MethodDelete, "/api/items/item-1", nil)
	var sent [][]byte
	var first string
	for i := range 3 {
		e, data := live.next(t)
		if i == 0 {
			first = e.ID
		}
		sent = append(sent, data)
	}
	gap := s.open(t, first)
	for range 2 {
		_, data := gap.next(t)
		sent = append(sent, data)
	}
	for _, data := range sent {
		if err := contracts.Validate("event/v1/event.schema.json", data); err != nil {
			t.Fatalf("%s: %v", data, err)
		}
	}
}

// Resume

func TestLastEventIDResumesAfterTheEventGiven(t *testing.T) {
	s := serving(t, true)
	live := s.open(t, "")
	s.add(t, "item-1")
	s.add(t, "item-2")
	s.add(t, "item-3")
	sent := live.ids(t, 3)
	st := s.open(t, sent[0])
	if got := st.ids(t, 2); !slices.Equal(got, sent[1:]) {
		t.Fatalf("ids %v, want %v", got, sent[1:])
	}
	s.add(t, "item-4")
	if e, _ := st.next(t); e.Subject != "item-4" {
		t.Fatalf("event %+v, want the new one", e)
	}
}

func TestALastEventIDNoLongerKeptGetsAGapThenEveryEventKept(t *testing.T) {
	s := serving(t, true, keep(2))
	live := s.open(t, "")
	s.add(t, "item-1")
	s.add(t, "item-2")
	s.add(t, "item-3")
	s.add(t, "item-4")
	sent := live.ids(t, 4)
	st := s.open(t, sent[0])
	gap, _ := st.next(t)
	var data events.Gap
	if err := json.Unmarshal(gap.Data, &data); err != nil {
		t.Fatal(err)
	}
	if gap.Type != "shop."+events.Gapped || data != (events.Gap{LastKnown: sent[0], OldestKept: sent[2]}) {
		t.Fatalf("gap %+v, data %+v", gap, data)
	}
	if got := st.ids(t, 2); !slices.Equal(got, sent[2:]) {
		t.Fatalf("ids %v, want %v", got, sent[2:])
	}
	again := s.open(t, gap.ID)
	if got := again.ids(t, 2); !slices.Equal(got, sent[2:]) {
		t.Fatalf("coming back with the gap's id gave %v, want %v without another gap", got, sent[2:])
	}
}

func TestALastEventIDFromAnotherServerGetsAGap(t *testing.T) {
	s := serving(t, true)
	st := s.open(t, "0123456789abcdef-7")
	gap, _ := st.next(t)
	if gap.Type != "shop."+events.Gapped || string(gap.Data) != `{"lastKnown":""}` {
		t.Fatalf("gap %+v", gap)
	}
}

func TestANewStreamGetsOnlyNewEvents(t *testing.T) {
	s := serving(t, true)
	s.add(t, "item-1")
	st := s.open(t, "")
	s.add(t, "item-2")
	if e, _ := st.next(t); e.Subject != "item-2" {
		t.Fatalf("event %+v", e)
	}
}

// Errors and codes

func TestACauseThatDoesNotFitItsRouteIsRefusedAtStart(t *testing.T) {
	list := func() *features.List { return features.New([]features.Feature{events.Feature}, (&switches{}).store()) }
	if err := events.Attach(list(), events.Config{Product: "shop", Catalog: []string{"shop.item.added"}, Causes: causes}); !errors.Is(err, events.ErrNotInCatalog) {
		t.Fatalf("a cause not in the catalog: err = %v", err)
	}
	for name, cause := range map[string]map[string]events.Cause{
		"no such route": {"POST /api/nothing": {Type: "shop.item.added"}},
		"another thing": {"POST /api/items/{id}": {Type: "shop.order.added"}},
	} {
		l := list()
		if err := events.Attach(l, events.Config{Product: "shop", Catalog: []string{"shop.item.added", "shop.order.added"}, Causes: cause}); err != nil {
			t.Fatal(err)
		}
		if _, err := serveHandler(t, app, serve.Site{Routes: routes(), Features: l}, nil); err == nil {
			t.Errorf("%s was taken", name)
		}
	}
}

func TestAChangeThatFailsSendsNothing(t *testing.T) {
	s := serving(t, true)
	st := s.open(t, "")
	if status := s.change(t, http.MethodDelete, "/api/items/missing", nil); status != http.StatusNotFound {
		t.Fatalf("status %d", status)
	}
	resp, cancel := s.do(t, http.MethodPost, "/api/items/item-0", map[string]string{"Authorization": "Bearer " + token})
	resp.Body.Close()
	cancel()
	s.add(t, "item-1")
	if e, _ := st.next(t); e.Subject != "item-1" {
		t.Fatalf("event %+v: a change that failed or was not confirmed was sent", e)
	}
}

func TestAttachRefusesNamesThatBreakTheEventNames(t *testing.T) {
	for name, cfg := range map[string]events.Config{
		"another product's type":          {Product: "shop", Catalog: []string{"store.item.added"}},
		"a type of two parts":             {Product: "shop", Catalog: []string{"shop.added"}},
		"a type shorter than the product": {Product: "warehouse", Catalog: []string{"a.b.c"}},
		"a product not an ID":             {Product: "Shop"},
		"a thing with a slash":            {Product: "shop", Thing: "a/b"},
	} {
		if err := events.Attach(features.New([]features.Feature{events.Feature}, (&switches{}).store()), cfg); err == nil {
			t.Errorf("%s was taken", name)
		}
	}
	if err := events.Attach(nil, events.Config{Product: "shop"}); err == nil {
		t.Error("taken without a feature list")
	}
	if err := events.Attach(features.New(nil, (&switches{}).store()), events.Config{Product: "shop"}); err == nil {
		t.Error("taken by a list without the events feature")
	}
}

func TestTheGuardRefusesAStreamWithoutTheToken(t *testing.T) {
	s := serving(t, true)
	resp, cancel := s.do(t, http.MethodGet, events.Path, nil)
	defer cancel()
	defer resp.Body.Close()
	refusal(t, resp, http.StatusUnauthorized, problem.TypeRefused, serve.ReasonToken)
}

func TestSwitchesThatCannotBeReadAreSaid(t *testing.T) {
	s := serving(t, true)
	s.sw.failing(errors.New("config.yaml: cannot read"))
	resp, cancel := s.do(t, http.MethodGet, events.Path, map[string]string{"Authorization": "Bearer " + token})
	defer cancel()
	defer resp.Body.Close()
	refusal(t, resp, http.StatusInternalServerError, problem.TypeFailed, serve.ReasonConfigUnreadable)
}

// The feature off

func TestTheFeatureStartsOffUntilTheOwnerSwitchesItOn(t *testing.T) {
	sw := &switches{}
	list := features.New([]features.Feature{events.Feature}, sw.store())
	if err := events.Attach(list, events.Config{Product: "shop"}); err != nil {
		t.Fatal(err)
	}
	if on, err := list.On(events.Feature.ID); err != nil || on {
		t.Fatalf("events on = %v, %v before the owner switched it on", on, err)
	}
	if err := list.Set(events.Feature.ID, true); err != nil {
		t.Fatal(err)
	}
	if on, err := list.On(events.Feature.ID); err != nil || !on {
		t.Fatalf("events on = %v, %v after the owner switched it on", on, err)
	}
}

func TestTheFeatureOffServesNoStream(t *testing.T) {
	s := serving(t, true)
	if err := s.list.Set(events.Feature.ID, false); err != nil {
		t.Fatal(err)
	}
	resp, cancel := s.do(t, http.MethodGet, events.Path, map[string]string{"Authorization": "Bearer " + token})
	defer cancel()
	defer resp.Body.Close()
	refusal(t, resp, http.StatusNotFound, problem.TypeNotFound, "")
}

func TestTheFeatureOffKeepsNothingAndAStreamThatComesBackGetsAGap(t *testing.T) {
	s := serving(t, true)
	live := s.open(t, "")
	s.add(t, "item-1")
	first := live.ids(t, 1)[0]
	if err := s.list.Set(events.Feature.ID, false); err != nil {
		t.Fatal(err)
	}
	s.add(t, "item-2")
	if err := s.list.Set(events.Feature.ID, true); err != nil {
		t.Fatal(err)
	}
	s.add(t, "item-3")
	st := s.open(t, first)
	gap, _ := st.next(t)
	var data events.Gap
	if err := json.Unmarshal(gap.Data, &data); err != nil {
		t.Fatal(err)
	}
	e, _ := st.next(t)
	if gap.Type != "shop."+events.Gapped || gap.Subject != "stream" || data != (events.Gap{OldestKept: e.ID}) {
		t.Fatalf("gap %+v", gap)
	}
	if e.Subject != "item-3" {
		t.Fatalf("event %+v, want only item-3: something was kept while off", e)
	}
}

func TestAChangeMissedWhileAnotherCloserHeldTheFeatureGivesAGap(t *testing.T) {
	s := serving(t, true)
	live := s.open(t, "")
	s.add(t, "item-1")
	first := live.ids(t, 1)[0]
	keepOff := s.sw.store().Keep
	if err := keepOff(events.Feature.ID, true); err != nil {
		t.Fatal(err)
	}
	s.add(t, "item-2")
	if err := keepOff(events.Feature.ID, false); err != nil {
		t.Fatal(err)
	}
	st := s.open(t, first)
	if gap, _ := st.next(t); gap.Type != "shop."+events.Gapped {
		t.Fatalf("event %+v, want the gap: item-2 went by unseen", gap)
	}
}

func TestAnotherCloserEndsAnOpenStreamWithinAFewHeartbeats(t *testing.T) {
	s := serving(t, true)
	st := s.open(t, "")
	s.sw.licensed(false)
	if !st.ended(t) {
		t.Fatal("the stream sent an event after the license closed the feature")
	}
}

func TestSwitchingTheFeatureOffEndsAnOpenStreamAtOnce(t *testing.T) {
	s := serving(t, true, func(c *events.Config) { c.Heartbeat = time.Hour })
	st := s.open(t, "")
	if err := s.list.Set(events.Feature.ID, false); err != nil {
		t.Fatal(err)
	}
	if !st.ended(t) {
		t.Fatal("the stream sent an event after the feature was switched off")
	}
}

// More resume

func TestALastEventIDAheadOfTheServerGetsAGap(t *testing.T) {
	s := serving(t, true)
	live := s.open(t, "")
	s.add(t, "item-1")
	id := live.ids(t, 1)[0]
	run, _, _ := strings.Cut(id, "-")
	ahead := run + "-99999"
	st := s.open(t, ahead)
	gap, _ := st.next(t)
	if gap.Type != "shop."+events.Gapped || string(gap.Data) != `{"lastKnown":"`+ahead+`","oldestKept":"`+id+`"}` {
		t.Fatalf("gap %+v", gap)
	}
	if e, _ := st.next(t); e.ID != id {
		t.Fatalf("event %+v, want the kept %s", e, id)
	}
}

func TestASlowStreamIsEndedAndResumesWhereItStopped(t *testing.T) {
	s := serving(t, true, keep(400))
	slow := s.open(t, "")
	const n = 300
	for i := range n {
		if status := s.change(t, http.MethodPut, fmt.Sprintf("/api/items/item-%d", i), nil); status != http.StatusOK {
			t.Fatalf("status %d", status)
		}
	}
	var got []string
	for {
		line, err := slow.r.ReadString('\n')
		if err != nil {
			break
		}
		if id, ok := strings.CutPrefix(line, "id: "); ok {
			got = append(got, strings.TrimSpace(id))
		}
	}
	if len(got) == 0 || len(got) >= n {
		t.Fatalf("the slow stream got %d of %d events before it ended", len(got), n)
	}
	rest := s.open(t, got[len(got)-1]).ids(t, n-len(got))
	if all := append(got, rest...); len(slices.Compact(slices.Clone(all))) != n {
		t.Fatalf("%d events after resuming, want %d without a gap", len(all), n)
	}
}

func TestASlowStreamTooFarBehindGetsAGapThenWhatIsKept(t *testing.T) {
	s := serving(t, true, keep(100))
	slow := s.open(t, "")
	const n = 300
	for i := range n {
		if status := s.change(t, http.MethodPut, fmt.Sprintf("/api/items/item-%d", i), nil); status != http.StatusOK {
			t.Fatalf("status %d", status)
		}
	}
	var last string
	count := 0
	for {
		line, err := slow.r.ReadString('\n')
		if err != nil {
			break
		}
		if id, ok := strings.CutPrefix(line, "id: "); ok {
			last, count = strings.TrimSpace(id), count+1
		}
	}
	if count == 0 || count >= n-100 {
		t.Fatalf("the slow stream got %d events before it ended; it must stop more than 100 short of %d", count, n)
	}
	back := s.open(t, last)
	gap, _ := back.next(t)
	if gap.Type != "shop."+events.Gapped {
		t.Fatalf("event %+v, want the gap: %d events are no longer kept", gap, n-count-100)
	}
	if got := back.ids(t, 100); len(got) != 100 {
		t.Fatalf("got %d kept events after the gap, want 100", len(got))
	}
}

func TestStoppingTheServerEndsAStream(t *testing.T) {
	h, _, _ := shopHandler(t, true, func(c *events.Config) { c.Heartbeat = time.Hour })
	srv := httptest.NewUnstartedServer(h)
	serve.StopOnShutdown(srv.Config)
	srv.Start()
	t.Cleanup(srv.Close)
	st := (&shop{url: srv.URL}).open(t, "")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Config.Shutdown(ctx); err != nil {
		t.Fatalf("the stream held the server open: %v", err)
	}
	if !st.ended(t) {
		t.Fatal("the stream sent an event while the server stopped")
	}
}

// More errors and codes

// servingSaid is the shop with events, and what events says on stderr until the test reads it.
func servingSaid(t *testing.T, tweak ...func(*events.Config)) (*shop, func() string) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	stderr := os.Stderr
	os.Stderr = w
	s := serving(t, true, tweak...)
	os.Stderr = stderr
	return s, func() string {
		w.Close()
		said, _ := io.ReadAll(r)
		return string(said)
	}
}

func TestAnEventThatCannotBeBuiltIsSaidAndTakesItsPlaceAsAGap(t *testing.T) {
	s, said := servingSaid(t)
	live := s.open(t, "")
	s.add(t, "item-1")
	first := live.ids(t, 1)[0]
	s.change(t, http.MethodPatch, "/api/items/item-2", nil)
	gap, _ := live.next(t)
	if gap.Type != "shop."+events.Gapped || string(gap.Data) != `{"lastKnown":"`+first+`"}` {
		t.Fatalf("gap %+v", gap)
	}
	if again, _ := s.open(t, first).next(t); again.ID != gap.ID {
		t.Fatalf("a stream coming back got %+v, want the gap in its place", again)
	}
	if got := said(); !strings.Contains(got, "shop.item.added from PATCH /api/items/{id}") || strings.Contains(got, "item-2") || strings.Contains(got, token) {
		t.Fatalf("stderr %q, want the type and the route, without data or token", got)
	}
}

func TestAnEventWithoutItsSubjectIsSaidAndTakesItsPlaceAsAGap(t *testing.T) {
	s, said := servingSaid(t)
	live := s.open(t, "")
	if status := s.change(t, http.MethodPost, "/api/nameless", nil); status != http.StatusCreated {
		t.Fatalf("status %d", status)
	}
	if gap, _ := live.next(t); gap.Type != "shop."+events.Gapped || string(gap.Data) != `{"lastKnown":""}` {
		t.Fatalf("gap %+v", gap)
	}
	if got := said(); !strings.Contains(got, "shop.item.added from POST /api/nameless") || !strings.Contains(got, "subject") {
		t.Fatalf("stderr %q", got)
	}
}

func TestTooManyStreamsAreRefused(t *testing.T) {
	s := serving(t, true, func(c *events.Config) { c.Streams = 1 })
	s.open(t, "")
	resp, cancel := s.do(t, http.MethodGet, events.Path, map[string]string{"Authorization": "Bearer " + token})
	defer cancel()
	defer resp.Body.Close()
	refusal(t, resp, http.StatusServiceUnavailable, problem.TypeRefused, events.ReasonTooManyStreams)
}

// CLI and HTTP parity: none, since no command reads the stream; the decisions call for none.

// Leaks

func TestAClientThatLeavesLeavesNoGoroutine(t *testing.T) {
	s := serving(t, true)
	before := runtime.NumGoroutine()
	var streams []*stream
	for range 5 {
		streams = append(streams, s.open(t, ""))
	}
	s.add(t, "item-1")
	for _, st := range streams {
		st.next(t)
		st.stop()
		st.resp.Body.Close()
	}
	http.DefaultClient.CloseIdleConnections()
	deadline := time.Now().Add(5 * time.Second)
	for runtime.NumGoroutine() > before {
		if time.Now().After(deadline) {
			buf := make([]byte, 1<<16)
			t.Fatalf("%d goroutines, %d before:\n%s", runtime.NumGoroutine(), before, buf[:runtime.Stack(buf, true)])
		}
		time.Sleep(10 * time.Millisecond)
	}
	s.add(t, "item-2")
}

// A Source's events reach every open stream as a change's do: checked against the catalog, about the thing it names, with a trace of their own.
func TestASourcesEventsReachEveryOpenStream(t *testing.T) {
	sent := make(chan func(events.Found), 1)
	s := serving(t, true, func(c *events.Config) {
		c.Source = func(ctx context.Context, send func(events.Found)) {
			sent <- send
			<-ctx.Done()
		}
	})
	live := s.open(t, "")
	send := <-sent
	send(events.Found{Type: "shop.item.added", Thing: "alpha", Subject: "item-9", Data: item{ID: "item-9"}})
	send(events.Found{Type: "shop.item.removed", Subject: "item-9", Data: item{ID: "item-9"}})
	for _, want := range []string{"/shop/alpha", "/shop/main"} {
		e, data := live.next(t)
		if e.Source != want || e.Subject != "item-9" || !validTrace(e.TraceParent) {
			t.Fatalf("event %s", data)
		}
	}
}

// The Source runs only while somebody listens: it starts with the first stream and stops when the last one ends.
func TestASourceRunsOnlyWhileAStreamIsOpen(t *testing.T) {
	var mu sync.Mutex
	runs, running := 0, 0
	stopped := make(chan struct{}, 4)
	s := serving(t, true, func(c *events.Config) {
		c.Source = func(ctx context.Context, send func(events.Found)) {
			mu.Lock()
			runs++
			running++
			mu.Unlock()
			<-ctx.Done()
			mu.Lock()
			running--
			mu.Unlock()
			stopped <- struct{}{}
		}
	})
	count := func() (int, int) {
		mu.Lock()
		defer mu.Unlock()
		return runs, running
	}
	if r, _ := count(); r != 0 {
		t.Fatal("the source ran with nobody listening")
	}
	a := s.open(t, "")
	b := s.open(t, "")
	waitFor(t, func() bool { _, n := count(); return n == 1 })
	a.stop()
	b.stop()
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("the source still runs with no stream open")
	}
	if r, n := count(); r != 1 || n != 0 {
		t.Fatalf("runs %d, running %d", r, n)
	}
}

// An event a Source finds outside the catalog, or about a thing that breaks the event names, is said and not sent.
func TestASourcesEventOutsideTheCatalogIsNotSent(t *testing.T) {
	sent := make(chan func(events.Found), 1)
	s := serving(t, true, func(c *events.Config) {
		c.Source = func(ctx context.Context, send func(events.Found)) {
			sent <- send
			<-ctx.Done()
		}
	})
	live := s.open(t, "")
	send := <-sent
	send(events.Found{Type: "shop.item.painted", Subject: "item-1"})
	send(events.Found{Type: "shop.item.added", Thing: "a/b", Subject: "item-1"})
	send(events.Found{Type: "shop.item.added", Subject: "item-2", Data: item{ID: "item-2"}})
	if e, data := live.next(t); e.Subject != "item-2" {
		t.Fatalf("event %s", data)
	}
}

func validTrace(tp string) bool { return len(tp) == 55 && strings.HasPrefix(tp, "00-") }

// waitFor waits a few seconds for ok to hold.
func waitFor(t *testing.T, ok func() bool) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if ok() {
			return
		}
	}
	t.Fatal("it did not happen in time")
}

// sending is a Source that hands each run's send to the test and waits for its context, counting its runs.
type sending struct {
	sends chan func(events.Found)
	mu    sync.Mutex
	runs  int
	ended chan struct{}
}

func newSending() *sending {
	return &sending{sends: make(chan func(events.Found), 4), ended: make(chan struct{}, 4)}
}

func (s *sending) source(ctx context.Context, send func(events.Found)) {
	s.mu.Lock()
	s.runs++
	s.mu.Unlock()
	s.sends <- send
	<-ctx.Done()
	s.ended <- struct{}{}
}

func (s *sending) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.runs
}

func (s *sending) next(t *testing.T) func(events.Found) {
	t.Helper()
	select {
	case send := <-s.sends:
		return send
	case <-time.After(5 * time.Second):
		t.Fatal("the source did not run")
		return nil
	}
}

func (s *sending) stopped(t *testing.T) {
	t.Helper()
	select {
	case <-s.ended:
	case <-time.After(5 * time.Second):
		t.Fatal("the source was not stopped")
	}
}

func added(id string) events.Found {
	return events.Found{Type: "shop.item.added", Subject: id, Data: item{ID: id}}
}

// While somebody listens, a stream that comes back with Last-Event-ID gets the Source's events after that one.
func TestAStreamResumesAfterASourcesEvent(t *testing.T) {
	src := newSending()
	s := serving(t, true, func(c *events.Config) { c.Source = src.source })
	keepsRunning := s.open(t, "")
	send := src.next(t)
	b := s.open(t, "")
	send(added("item-1"))
	send(added("item-2"))
	first := b.ids(t, 1)[0]
	keepsRunning.ids(t, 2)
	b.stop()
	again := s.open(t, first)
	if e, data := again.next(t); e.Subject != "item-2" {
		t.Fatalf("resumed with %s", data)
	}
}

// What changes while nobody listens is not looked for, so a stream that comes back after a quiet time gets the gapped event and reads everything again; the Source runs again for it.
func TestAStreamComingBackAfterAQuietTimeGetsAGap(t *testing.T) {
	src := newSending()
	s := serving(t, true, func(c *events.Config) { c.Source = src.source })
	a := s.open(t, "")
	src.next(t)(added("item-1"))
	last := a.ids(t, 1)[0]
	a.stop()
	src.stopped(t)
	again := s.open(t, last)
	if e, data := again.next(t); e.Type != "shop."+events.Gapped {
		t.Fatalf("came back with %s", data)
	}
	src.next(t)(added("item-2"))
	if e, _ := again.next(t); e.Subject != "item-2" {
		t.Fatalf("the second run's event %+v", e)
	}
	if src.count() != 2 {
		t.Fatalf("runs %d", src.count())
	}
}

// A send from a run that was stopped is dropped, even when it comes after the next run started.
func TestASendFromAStoppedRunIsDropped(t *testing.T) {
	src := newSending()
	s := serving(t, true, func(c *events.Config) { c.Source = src.source })
	a := s.open(t, "")
	stale := src.next(t)
	a.stop()
	src.stopped(t)
	b := s.open(t, "")
	fresh := src.next(t)
	stale(added("item-stale"))
	fresh(added("item-fresh"))
	if e, data := b.next(t); e.Subject != "item-fresh" {
		t.Fatalf("got %s", data)
	}
}

// Switching the feature off stops the Source.
func TestSwitchingTheFeatureOffStopsTheSource(t *testing.T) {
	src := newSending()
	s := serving(t, true, func(c *events.Config) { c.Source = src.source; c.Heartbeat = time.Hour })
	s.open(t, "")
	src.next(t)
	if err := s.list.Set(events.Feature.ID, false); err != nil {
		t.Fatal(err)
	}
	src.stopped(t)
}

// A Source that panics is said once and its run ends; the next stream starts it again.
func TestAPanickingSourceIsSaidAndStartsAgain(t *testing.T) {
	var mu sync.Mutex
	runs := 0
	started := make(chan struct{}, 4)
	s, said := servingSaid(t, func(c *events.Config) {
		c.Source = func(ctx context.Context, send func(events.Found)) {
			mu.Lock()
			runs++
			n := runs
			mu.Unlock()
			started <- struct{}{}
			if n == 1 {
				panic("broken")
			}
			<-ctx.Done()
		}
	})
	a := s.open(t, "")
	<-started
	a.stop()
	b := s.open(t, "")
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("the source did not start again")
	}
	b.stop()
	if got := said(); strings.Count(got, "the source panicked with broken") != 1 {
		t.Fatalf("said %q", got)
	}
}
