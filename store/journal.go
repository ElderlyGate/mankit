package store

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"math/big"
	"os"
	"os/user"
	"regexp"
	"strconv"
	"time"
)

// journalHead is a journal's first line (contracts mankit/v1 store-journal).
type journalHead struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Metadata   struct {
		ID   string `json:"id"`
		App  string `json:"app"`
		PID  int    `json:"pid"`
		Host string `json:"host"`
	} `json:"metadata"`
	Spec struct {
		Scope string `json:"scope"`
		Mark  string `json:"mark"`
	} `json:"spec"`
}

// journalLine is one write of a change, logged before it is made (contracts mankit/v1 store-journal-change).
type journalLine struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Metadata   struct {
		ID int `json:"id"`
	} `json:"metadata"`
	Spec line `json:"spec"`
}

// line is what one write does: a file's content before and after, nil where there is no file, or a folder made, taken away (Gone) or moved here whole (From).
type line struct {
	Key    Key     `json:"key"`
	Before *[]byte `json:"before,omitempty"`
	After  *[]byte `json:"after,omitempty"`
	Dir    bool    `json:"dir,omitempty"`
	Gone   bool    `json:"gone,omitempty"`
	From   Key     `json:"from,omitempty"`
	// mode is the file's mode, kept while the change runs so putting it back in this process gives it back; a journal does not hold it.
	mode fs.FileMode
}

// content is body as a line holds it: nil stays nil, and an empty file stays an empty file.
func content(body []byte) *[]byte {
	if body == nil {
		return nil
	}
	b := append([]byte{}, body...)
	return &b
}

// mark is the file .unfinished-<id>.json the store writes in the .queue/ of every turn a change holds (contracts mankit/v1 store-unfinished).
type mark struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Metadata   struct {
		ID string `json:"id"`
	} `json:"metadata"`
	Spec struct {
		User    string `json:"user"`
		Host    string `json:"host"`
		Journal string `json:"journal"`
		Folder  bool   `json:"folder,omitempty"`
		Keys    []Key  `json:"keys"`
	} `json:"spec"`
}

// Unfinished is a change cut short that waits to be put back: whose it is, where its journal is, and the keys it holds back.
// Its JSON is contracts mankit/v1 storeUnfinishedChange, which <app> store unfinished answers with.
type Unfinished struct {
	ID      string `json:"id"`
	User    string `json:"user"`
	Host    string `json:"host"`
	Journal string `json:"journal"`
	Folder  bool   `json:"folder,omitempty"`
	Keys    []Key  `json:"keys"`
	// File is the journal on this machine when it is this user's and still there, for a person to look at before clearing.
	File string `json:"file,omitempty"`
	// Left is a journal of this user's whose marks are gone, cleared or lost: it puts nothing back by itself and is kept, as the only copy of what was there.
	Left bool `json:"left,omitempty"`
}

func (m mark) unfinished() Unfinished {
	return Unfinished{ID: m.Metadata.ID, User: m.Spec.User, Host: m.Spec.Host, Journal: m.Spec.Journal, Folder: m.Spec.Folder, Keys: m.Spec.Keys}
}

const (
	markPrefix = ".unfinished-"
	markSuffix = ".json"
	journalExt = ".jsonl"
)

var (
	markID    = regexp.MustCompile(`^[0-9]{20}$`)
	markName  = regexp.MustCompile(`^\.unfinished-([0-9]{20})\.json$`)
	journalID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
)

// newID is a mark's and its journal's id: 13 digits of the time in milliseconds and 7 random ones, so it is one of a kind whichever user makes it.
func newID() string {
	n, _ := rand.Int(rand.Reader, big.NewInt(10_000_000))
	return fmt.Sprintf("%013d%07d", time.Now().UnixMilli(), n.Int64())
}

// userName is this user by the name the system knows them by, else by their number.
func userName() string {
	if u, err := user.Current(); err == nil && u.Username != "" {
		return u.Username
	}
	return "uid " + strconv.Itoa(os.Getuid())
}

func newMark(id, journal string, folder bool, keys []Key) mark {
	var m mark
	m.APIVersion, m.Kind = APIVersion(), UnfinishedKind
	m.Metadata.ID = id
	m.Spec.User, m.Spec.Host, m.Spec.Journal, m.Spec.Folder, m.Spec.Keys = userName(), hostname(), journal, folder, keys
	return m
}

// readMark is the mark in data, or why it is not one; a mark that cannot be read is never taken for absent (G-CODE-6).
func readMark(data []byte, id string) (mark, error) {
	var m mark
	if err := json.Unmarshal(data, &m); err != nil {
		return m, fmt.Errorf("is not JSON: %v", err)
	}
	s := m.Spec
	if m.APIVersion != APIVersion() || m.Kind != UnfinishedKind || m.Metadata.ID != id || s.User == "" || s.Host == "" || !journalID.MatchString(s.Journal) || len(s.Keys) == 0 {
		return m, fmt.Errorf("is not a %s %s whose id is its file name, with a user, a host, a journal and its keys", APIVersion(), UnfinishedKind)
	}
	for _, k := range s.Keys {
		if err := k.check(); err != nil {
			return m, fmt.Errorf("names %q, which is no key", k)
		}
	}
	return m, nil
}

// encodeHead and encodeLine are a journal's lines, each ended by a newline.
func encodeHead(id, app, scope string) ([]byte, error) {
	var h journalHead
	h.APIVersion, h.Kind = APIVersion(), JournalKind
	h.Metadata.ID, h.Metadata.App, h.Metadata.PID, h.Metadata.Host = id, app, os.Getpid(), hostname()
	h.Spec.Scope, h.Spec.Mark = scope, id
	b, err := json.Marshal(h)
	return append(b, '\n'), err
}

func encodeLine(n int, l line) ([]byte, error) {
	if err := l.Key.check(); err != nil {
		return nil, err
	}
	if l.From != "" {
		if err := l.From.check(); err != nil {
			return nil, err
		}
	}
	var jl journalLine
	jl.APIVersion, jl.Kind, jl.Spec = APIVersion(), JournalChangeKind, l
	jl.Metadata.ID = n
	b, err := json.Marshal(jl)
	return append(b, '\n'), err
}

// parseJournal reads a journal's lines: a last line cut short was never acted on and is left out; any other line that cannot be read, or an id out of order, stops it with nothing put back (G-CODE-6).
func parseJournal(raw []byte) (journalHead, []line, error) {
	var head journalHead
	var out []line
	scan := bufio.NewScanner(bytes.NewReader(raw))
	scan.Buffer(make([]byte, 0, 1<<20), 1<<31-1)
	complete := len(raw) > 0 && raw[len(raw)-1] == '\n'
	var lines [][]byte
	for scan.Scan() {
		lines = append(lines, append([]byte{}, scan.Bytes()...))
	}
	if err := scan.Err(); err != nil {
		return head, nil, err
	}
	for i, b := range lines {
		last := i == len(lines)-1
		if i == 0 {
			err := json.Unmarshal(b, &head)
			if err == nil && (head.APIVersion != APIVersion() || head.Kind != JournalKind || !journalID.MatchString(head.Metadata.ID) || head.Metadata.PID < 1 || head.Metadata.Host == "" || head.Spec.Scope == "" || !markID.MatchString(head.Spec.Mark)) {
				err = fmt.Errorf("is not a %s %s", APIVersion(), JournalKind)
			}
			if err != nil {
				return head, nil, fmt.Errorf("its first line %v", errText(err))
			}
			continue
		}
		var jl journalLine
		err := json.Unmarshal(b, &jl)
		if err == nil && (jl.APIVersion != APIVersion() || jl.Kind != JournalChangeKind || jl.Spec.Key.check() != nil || jl.Spec.From != "" && jl.Spec.From.check() != nil) {
			err = fmt.Errorf("is not a %s %s", APIVersion(), JournalChangeKind)
		}
		if err == nil && jl.Metadata.ID != len(out)+1 {
			err = fmt.Errorf("has the id %d where %d comes", jl.Metadata.ID, len(out)+1)
		}
		if err != nil {
			if last && !complete {
				break
			}
			return head, nil, fmt.Errorf("its line %d %v", i+1, errText(err))
		}
		out = append(out, jl.Spec)
	}
	if len(lines) == 0 {
		return head, nil, errors.New("is empty")
	}
	return head, out, nil
}

func errText(err error) string {
	var se *json.SyntaxError
	if errors.As(err, &se) {
		return "is not JSON"
	}
	return err.Error()
}
