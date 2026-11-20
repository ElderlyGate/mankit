package ask

import (
	"net/url"
	"testing"
	"time"
)

func request(body string) Request {
	return Request{Method: "POST", Path: "/api/shelves", Query: url.Values{"shelf": {"top"}}, Body: []byte(body), ContentType: "application/json"}
}

func TestAQuestionIsTakenOnceForTheRequestItAsked(t *testing.T) {
	q := NewQuestions()
	a := Action{Type: Delete, What: "remove the top shelf", Name: "top", System: true}
	got := q.Ask(a, Name, request(`{"a":1}`))
	if len(got.ID) < 16 || got.Action != Delete || !got.System || got.Style != QuestionTypeName || got.Detail != "remove the top shelf" {
		t.Fatalf("question %+v", got)
	}
	if taken, ok := q.Take(got.ID, request(`{"a":1}`)); !ok || taken != a {
		t.Fatalf("Take = %+v, %v; want the change it asked about", taken, ok)
	}
	if _, ok := q.Take(got.ID, request(`{"a":1}`)); ok {
		t.Fatal("a question was taken twice")
	}
}

func TestAQuestionBindsToItsOwnRequest(t *testing.T) {
	q := NewQuestions()
	for name, other := range map[string]Request{
		"another body":   request(`{"a":2}`),
		"another path":   {Method: "POST", Path: "/api/racks", Query: url.Values{"shelf": {"top"}}, Body: []byte(`{"a":1}`), ContentType: "application/json"},
		"another query":  {Method: "POST", Path: "/api/shelves", Query: url.Values{"shelf": {"low"}}, Body: []byte(`{"a":1}`), ContentType: "application/json"},
		"another method": {Method: "PUT", Path: "/api/shelves", Query: url.Values{"shelf": {"top"}}, Body: []byte(`{"a":1}`), ContentType: "application/json"},
	} {
		id := q.Ask(Action{Type: Edit}, YesNo, request(`{"a":1}`)).ID
		if _, ok := q.Take(id, other); ok {
			t.Errorf("%s: a yes for one request ran another", name)
		}
	}
}

func TestAQuestionExpires(t *testing.T) {
	was := keepFor
	keepFor = time.Millisecond
	defer func() { keepFor = was }()
	q := NewQuestions()
	id := q.Ask(Action{Type: Edit}, YesNo, request("")).ID
	time.Sleep(5 * time.Millisecond)
	if _, ok := q.Take(id, request("")); ok {
		t.Fatal("an expired question was taken")
	}
	// Asking sweeps what expired, so the keeper does not grow.
	q.Ask(Action{Type: Edit}, YesNo, request(""))
	time.Sleep(5 * time.Millisecond)
	q.Ask(Action{Type: Edit}, YesNo, request(""))
	if n := len(q.waiting); n != 1 {
		t.Fatalf("%d questions wait, want the one asked last", n)
	}
}

func TestAQuestionAsksYesNoWithoutAName(t *testing.T) {
	if got := NewQuestions().Ask(Action{Type: Delete}, Name, request("")); got.Style != QuestionYesNo || got.Detail != "delete" {
		t.Fatalf("question %+v, want yes_no and the type as its detail", got)
	}
}

// No more questions wait than atMost: the oldest goes first.
func TestNoMoreQuestionsWaitThanAtMost(t *testing.T) {
	was := atMost
	atMost = 3
	defer func() { atMost = was }()
	q := NewQuestions()
	first := q.Ask(Action{Type: Edit}, YesNo, request("1")).ID
	for _, b := range []string{"2", "3", "4"} {
		q.Ask(Action{Type: Edit}, YesNo, request(b))
	}
	if n := len(q.waiting); n != 3 {
		t.Fatalf("%d questions wait, want 3", n)
	}
	if _, ok := q.Take(first, request("1")); ok {
		t.Fatal("the oldest question still waits")
	}
}
