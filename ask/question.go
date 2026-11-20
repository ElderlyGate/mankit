package ask

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Question is a change waiting for a person's yes (contracts question/v1, ADR-S04, G-STACK-13): its one-time id, the change's type, whether it changes the system itself, how it asks and what will change.
type Question struct {
	ID     string `json:"id"`
	Action Type   `json:"action"`
	System bool   `json:"system"`
	Style  string `json:"style"` // the page asks for the name or a yes; the server checks only the id
	Detail string `json:"detail"`
}

// The styles a Question names, in question/v1's words.
const (
	QuestionTypeName = "type_name"
	QuestionYesNo    = "yes_no"
)

// Request is what a person's yes binds to: the same method, path, query, body and type, so an id taken for one request runs no other.
type Request struct {
	Method      string
	Path        string
	Query       url.Values // without the answer's own words, such as confirm and remember
	Body        []byte
	ContentType string
}

// binding is r as one string, the query in its sorted form and the body as its sum, so two requests bind alike only when they are the same change.
func (r Request) binding() string {
	sum := sha256.Sum256(r.Body)
	return strings.Join([]string{r.Method, r.Path, r.Query.Encode(), hex.EncodeToString(sum[:]), r.ContentType}, "\n")
}

// keepFor is how long a question waits for its yes, and atMost how many wait at once, the oldest going first past it; vars only so a test can shorten them.
var (
	keepFor = 5 * time.Minute
	atMost  = 1000
)

// Questions keeps the questions one server asked until each is answered once or expires; a server keeps one, so a yes reaches the server that asked.
type Questions struct {
	mu      sync.Mutex
	waiting map[string]pending
}

type pending struct {
	binding string
	action  Action
	until   time.Time
}

// NewQuestions is a server's questions, none waiting.
func NewQuestions() *Questions { return &Questions{waiting: map[string]pending{}} }

// Ask is the question a asks in style for req, kept for its yes; expired questions go as it asks.
func (q *Questions) Ask(a Action, style Style, req Request) Question {
	id := newID()
	now := time.Now()
	q.mu.Lock()
	for k, p := range q.waiting {
		if now.After(p.until) {
			delete(q.waiting, k)
		}
	}
	for len(q.waiting) >= atMost {
		oldest := ""
		for k, p := range q.waiting {
			if oldest == "" || p.until.Before(q.waiting[oldest].until) {
				oldest = k
			}
		}
		delete(q.waiting, oldest)
	}
	q.waiting[id] = pending{binding: req.binding(), action: a, until: now.Add(keepFor)}
	q.mu.Unlock()
	word := QuestionYesNo
	if style == Name && a.Name != "" {
		word = QuestionTypeName
	}
	detail := a.What
	if detail == "" {
		detail = string(a.Type)
	}
	return Question{ID: id, Action: a.Type, System: a.System, Style: word, Detail: detail}
}

// Take is the change the question id asked about, when it waits and req is the request it was asked for; it is taken once, so a second yes with the same id finds nothing.
func (q *Questions) Take(id string, req Request) (Action, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	p, ok := q.waiting[id]
	if !ok {
		return Action{}, false
	}
	delete(q.waiting, id)
	if time.Now().After(p.until) || p.binding != req.binding() {
		return Action{}, false
	}
	return p.action, true
}

// newID is a question's id: 16 random bytes in URL-safe base64, 22 characters, no trace id.
func newID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}
