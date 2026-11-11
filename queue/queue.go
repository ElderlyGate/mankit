// Package queue is the change queue every app shares (ADR-S14): every change to one served thing waits its turn in one queue,
// kept in <served folder>/<app folder>/.queue/, so CLI runs, servers, page clicks and other apps see the same line.
// The queue holds tickets, never commands: each process runs its own change, and nothing in the folder is ever executed.
package queue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/tugbayatilla/mankit/home"
	"github.com/tugbayatilla/mankit/serve"
)

// The codes of the errors the queue gives; a program decides by the code, never by the message (G-STACK-15).
const (
	CodeTimeout     = "queue_timeout"  // the change waited longer than Queue.Wait for its turn and did not run
	CodeNotReadBack = "not_read_back"  // the change ran, but what it wrote could not be read back
	CodeLost        = "ticket_lost"    // another process found the ticket stale and moved it to bad/ while it waited
	CodeUnusable    = "queue_unusable" // the queue's folder cannot be made, read or written
)

// The waits an app gets when it sets none.
const (
	DefaultWait  = 30 * time.Second
	DefaultStale = 30 * time.Second
)

// Kind is the kind of a ticket, and ListKind the kind of the answer <app> queue gives (contracts mankit/v1).
const (
	Kind     = "Ticket"
	ListKind = "QueueList"
)

const (
	folderName = ".queue"
	badName    = "bad"
	lockName   = "lock"
	turnName   = "turn"
	ignoreName = ".gitignore"
	poll       = 20 * time.Millisecond
)

// APIVersion is the ticket's apiVersion; two app versions share a queue at once, so it never changes (ADR-S15).
func APIVersion() string { return home.Org + "/mankit/ticket/v1" }

// Queue is the queue of one served thing, as one app sees it.
type Queue struct {
	Folder  string        // the served thing's app folder, <served folder>/<app folder>; the queue is its .queue
	App     string        // the product ID of the app that waits
	Version string        // the app's version, written on its tickets
	Wait    time.Duration // how long a change waits for its turn; zero is DefaultWait
	Stale   time.Duration // a ticket with no sign of life for this long is stale; zero is DefaultStale
	Log     io.Writer     // where a ticket moved to bad/ is said; nil is stderr
}

// Ticket is one change waiting its turn, as written in the queue's folder (contracts mankit/v1 ticket).
type Ticket struct {
	APIVersion string   `json:"apiVersion"`
	Kind       string   `json:"kind"`
	Metadata   Metadata `json:"metadata"`
	Spec       Spec     `json:"spec"`
}

// Metadata is the ticket's fixed header: never renamed or removed (ADR-S15).
type Metadata struct {
	ID   string `json:"id"` // its place in the line and its file name; a lower one goes first
	App  string `json:"app"`
	PID  int    `json:"pid"`
	Host string `json:"host"`
}

// Spec is what the ticket says about its change; a newer app may add fields.
type Spec struct {
	Change   string    `json:"change"`
	JoinedAt time.Time `json:"joinedAt"`
	Version  string    `json:"version,omitempty"`
}

// Listing is what <app> queue shows: the tickets in the order they go, and the files moved to bad/.
type Listing struct {
	Tickets []Place `json:"tickets"`
	Bad     []Bad   `json:"bad"`
}

// Place is a ticket in the line, and whether its change runs now.
type Place struct {
	Ticket Ticket `json:"ticket"`
	Turn   bool   `json:"turn"`
}

// Bad is a file moved to .queue/bad/, by its name there.
type Bad struct {
	File string `json:"file"`
}

// Change runs write once every change queued before it on this thing is done, then reads it back; the change is done only once readBack is.
// An error from write is given back as it is; the queue's own errors carry its codes.
func (q Queue) Change(ctx context.Context, change string, write, readBack func() error) error {
	t, err := q.join(change)
	if err != nil {
		return err
	}
	defer t.leave()
	if err := t.await(ctx); err != nil {
		return err
	}
	if err := write(); err != nil {
		return err
	}
	if err := readBack(); err != nil {
		return serve.Fail(CodeNotReadBack, fmt.Sprintf("%s was written but could not be read back: %v", change, err))
	}
	return nil
}

// List is the queue now; stale and unreadable tickets it meets are moved to bad/ first, as every reader of the queue does.
func (q Queue) List() (Listing, error) {
	out := Listing{Tickets: []Place{}, Bad: []Bad{}}
	if _, err := os.Stat(q.dir()); errors.Is(err, os.ErrNotExist) {
		return out, nil
	}
	err := q.locked(func() error {
		tickets, err := q.sweep()
		if err != nil {
			return err
		}
		busy, err := q.turnTaken()
		if err != nil {
			return err
		}
		for i, t := range tickets {
			out.Tickets = append(out.Tickets, Place{Ticket: t, Turn: i == 0 && busy})
		}
		bad, err := os.ReadDir(filepath.Join(q.dir(), badName))
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return unusable("cannot read %s: %v", filepath.Join(q.dir(), badName), err)
		}
		for _, e := range bad {
			out.Bad = append(out.Bad, Bad{File: e.Name()})
		}
		return nil
	})
	return out, err
}

func (q Queue) dir() string { return filepath.Join(q.Folder, folderName) }

func (q Queue) wait() time.Duration { return or(q.Wait, DefaultWait) }

func (q Queue) stale() time.Duration { return or(q.Stale, DefaultStale) }

func or(d, fallback time.Duration) time.Duration {
	if d > 0 {
		return d
	}
	return fallback
}

func (q Queue) log() io.Writer {
	if q.Log != nil {
		return q.Log
	}
	return os.Stderr
}

func unusable(format string, args ...any) error {
	return serve.Fail(CodeUnusable, "the change queue "+fmt.Sprintf(format, args...))
}

// ticket is this process's place in the line while its change waits and runs.
type ticket struct {
	q    Queue
	id   string
	path string
	turn *os.File
	stop chan struct{}
	done sync.WaitGroup
}

// join writes a ticket at the end of the line and keeps it alive until it leaves.
func (q Queue) join(change string) (*ticket, error) {
	if err := q.prepare(); err != nil {
		return nil, err
	}
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "unknown"
	}
	t := &ticket{q: q, stop: make(chan struct{})}
	err = q.locked(func() error {
		tickets, err := q.sweep()
		if err != nil {
			return err
		}
		t.id = nextID(tickets, time.Now())
		t.path = filepath.Join(q.dir(), t.id+".json")
		data, err := json.MarshalIndent(Ticket{
			APIVersion: APIVersion(),
			Kind:       Kind,
			Metadata:   Metadata{ID: t.id, App: q.App, PID: os.Getpid(), Host: host},
			Spec:       Spec{Change: change, JoinedAt: time.Now().UTC(), Version: q.Version},
		}, "", "  ")
		if err != nil {
			return err
		}
		if err := writeWhole(q.dir(), t.path, append(data, '\n')); err != nil {
			return unusable("cannot write a ticket in %s: %v", q.dir(), err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	t.done.Add(1)
	go t.alive()
	return t, nil
}

// nextID is after every ticket in the line, and close to now so a person reading the folder sees the order.
func nextID(tickets []Ticket, now time.Time) string {
	next := now.UnixNano()
	for _, t := range tickets {
		if n, err := strconv.ParseInt(t.Metadata.ID, 10, 64); err == nil && n >= next {
			next = n + 1
		}
	}
	return fmt.Sprintf("%020d", next)
}

// alive touches the ticket while it waits and runs, so no other process takes it for the ticket of one that died.
func (t *ticket) alive() {
	defer t.done.Done()
	tick := time.NewTicker(t.q.stale() / 4)
	defer tick.Stop()
	for {
		select {
		case <-t.stop:
			return
		case now := <-tick.C:
			_ = os.Chtimes(t.path, now, now)
		}
	}
}

// await returns once the ticket is first in the line and holds the turn, or gives up when the wait is over.
func (t *ticket) await(ctx context.Context) error {
	deadline := time.NewTimer(t.q.wait())
	defer deadline.Stop()
	for {
		ready, err := t.try()
		if err != nil || ready {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return serve.Fail(CodeTimeout, fmt.Sprintf("the change waited %s for its turn in %s and did not run", t.q.wait(), t.q.dir()))
		case <-time.After(poll):
		}
	}
}

// try takes the turn when the ticket is first; the turn's lock also keeps out a process whose ticket was taken for stale while it still runs.
func (t *ticket) try() (bool, error) {
	ready := false
	err := t.q.locked(func() error {
		tickets, err := t.q.sweep()
		if err != nil {
			return err
		}
		if !holds(tickets, t.id) {
			return serve.Fail(CodeLost, fmt.Sprintf("the ticket %s was taken for stale and moved to %s; the change did not run", t.path, filepath.Join(t.q.dir(), badName)))
		}
		if tickets[0].Metadata.ID != t.id {
			return nil
		}
		f, err := os.OpenFile(filepath.Join(t.q.dir(), turnName), os.O_CREATE|os.O_RDWR, 0o600)
		if err != nil {
			return unusable("cannot open %s: %v", filepath.Join(t.q.dir(), turnName), err)
		}
		got, err := lockFile(f, false)
		if err != nil || !got {
			f.Close()
			return err
		}
		t.turn, ready = f, true
		return nil
	})
	return ready, err
}

func holds(tickets []Ticket, id string) bool {
	for _, t := range tickets {
		if t.Metadata.ID == id {
			return true
		}
	}
	return false
}

// leave gives the turn up and takes the ticket out of the line, whether the change ran or not.
func (t *ticket) leave() {
	close(t.stop)
	t.done.Wait()
	if t.turn != nil {
		_ = unlockFile(t.turn)
		t.turn.Close()
	}
	_ = t.q.locked(func() error {
		err := os.Remove(t.path)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	})
}

// prepare makes the queue's folder only this user may enter, and keeps it out of git without touching the served folder's own files.
func (q Queue) prepare() error {
	dir := q.dir()
	for _, d := range []string{dir, filepath.Join(dir, badName)} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return unusable("cannot make %s: %v", d, err)
		}
		if err := os.Chmod(d, 0o700); err != nil {
			return unusable("cannot keep %s to this user: %v", d, err)
		}
	}
	// Under the folder's lock, so no other process meets the temporary file and takes it for a bad ticket.
	return q.locked(func() error {
		ignore := filepath.Join(dir, ignoreName)
		if _, err := os.Stat(ignore); errors.Is(err, os.ErrNotExist) {
			if err := writeWhole(dir, ignore, []byte("*\n")); err != nil {
				return unusable("cannot write %s: %v", ignore, err)
			}
		}
		return nil
	})
}

// locked runs fn while this process alone may change the queue's folder; fn is short, so others wait for it without a deadline.
func (q Queue) locked(fn func() error) error {
	path := filepath.Join(q.dir(), lockName)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return unusable("cannot open %s: %v", path, err)
	}
	defer f.Close()
	if _, err := lockFile(f, true); err != nil {
		return unusable("cannot lock %s: %v", path, err)
	}
	defer unlockFile(f)
	return fn()
}

// turnTaken says whether a change runs now; it is asked under the folder's lock, so taking and giving the turn back here holds up no one.
func (q Queue) turnTaken() (bool, error) {
	path := filepath.Join(q.dir(), turnName)
	f, err := os.OpenFile(path, os.O_RDWR, 0o600)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, unusable("cannot open %s: %v", path, err)
	}
	defer f.Close()
	got, err := lockFile(f, false)
	if err != nil {
		return false, unusable("cannot lock %s: %v", path, err)
	}
	if got {
		_ = unlockFile(f)
	}
	return !got, nil
}

// sweep is the line in order; a ticket it cannot read or understand, or one with no sign of life, is moved to bad/ and said (G-CODE-6).
func (q Queue) sweep() ([]Ticket, error) {
	dir := q.dir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, unusable("cannot read %s: %v", dir, err)
	}
	var tickets []Ticket
	for _, e := range entries {
		switch e.Name() {
		case badName, lockName, turnName, ignoreName:
			continue
		}
		path := filepath.Join(dir, e.Name())
		t, why := q.read(path, e)
		if why != "" {
			if err := q.moveBad(path, why); err != nil {
				return nil, err
			}
			continue
		}
		tickets = append(tickets, t)
	}
	sort.Slice(tickets, func(i, j int) bool { return tickets[i].Metadata.ID < tickets[j].Metadata.ID })
	return tickets, nil
}

// read is the ticket at path, or why it is bad.
func (q Queue) read(path string, e os.DirEntry) (Ticket, string) {
	var t Ticket
	if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
		return t, "is not a ticket file"
	}
	info, err := e.Info()
	if err != nil {
		return t, fmt.Sprintf("cannot be read: %v", err)
	}
	if age := time.Since(info.ModTime()); age > q.stale() {
		return t, fmt.Sprintf("is stale: no sign of life for %s", age.Round(time.Second))
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return t, fmt.Sprintf("cannot be read: %v", err)
	}
	if err := json.Unmarshal(data, &t); err != nil {
		return t, fmt.Sprintf("is not JSON: %v", err)
	}
	m := t.Metadata
	if t.APIVersion != APIVersion() || t.Kind != Kind || m.ID+".json" != e.Name() || m.App == "" || m.PID < 1 || m.Host == "" {
		return t, fmt.Sprintf("is not a %s %s whose id is its file name, with an app, a pid and a host", APIVersion(), Kind)
	}
	return t, ""
}

// moveBad sets a bad ticket aside, never over another one already there.
func (q Queue) moveBad(path, why string) error {
	bad := filepath.Join(q.dir(), badName)
	to := filepath.Join(bad, filepath.Base(path))
	for n := 1; ; n++ {
		if _, err := os.Lstat(to); errors.Is(err, os.ErrNotExist) {
			break
		}
		to = filepath.Join(bad, fmt.Sprintf("%s.%d", filepath.Base(path), n))
	}
	if err := os.Rename(path, to); err != nil {
		return unusable("cannot move %s, which %s, to %s: %v", path, why, bad, err)
	}
	fmt.Fprintf(q.log(), "queue: %s %s; moved to %s and skipped\n", path, why, to)
	return nil
}

// writeWhole writes data to path through a temporary file beside it (G-CODE-5).
func writeWhole(dir, path string, data []byte) error {
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
