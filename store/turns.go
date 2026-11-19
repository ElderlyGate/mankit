package store

import (
	"context"
	"errors"
	"slices"
	"sync/atomic"
)

// turns are the turns one change holds, each a ticket in a line's change queue (ADR-S14), taken the folder's first and given up last first; a line two keys share is taken once.
type turns struct {
	s     *Scope
	ctx   context.Context
	what  string
	held  []*held
	lines map[string]bool
	root  bool
}

func (s *Scope) turns(ctx context.Context, what string) *turns {
	return &turns{s: s, ctx: ctx, what: what, lines: map[string]bool{}}
}

// held is one turn: the queue's change runs until the store lets go of it, and says when its limit passed.
type held struct {
	line    string
	dir     string
	release chan error
	done    chan error
	over    atomic.Bool
}

// take holds thing's turn, the folder's own when thing is empty, waiting for it as the queue allows.
func (t *turns) take(thing Key) error {
	line := t.s.line(thing)
	if thing == "" {
		t.root = true
	}
	if t.lines[line] {
		return nil
	}
	q := t.s.queueFor(line)
	h := &held{line: line, dir: q.Dir(), release: make(chan error, 1), done: make(chan error, 1)}
	started := make(chan struct{})
	go func() {
		h.done <- q.Change(t.ctx, t.what, func(running context.Context) error {
			close(started)
			select {
			case err := <-h.release:
				return err
			case <-running.Done():
				// The turn is kept until the store has stopped, since its writes may still be under way; it sees over and stops.
				h.over.Store(true)
				return <-h.release
			}
		}, func(context.Context) error { return nil })
	}()
	select {
	case <-started:
	case err := <-h.done:
		return err
	}
	t.lines[line] = true
	t.held = append(t.held, h)
	return nil
}

// holds says whether thing's turn is held, the folder's own when thing is empty.
func (t *turns) holds(thing Key) bool {
	if thing == "" {
		return t.root
	}
	return t.lines[t.s.line(thing)]
}

// dirs are the .queue folders of every turn held, where a change's marks go.
func (t *turns) dirs() []string {
	var out []string
	for _, h := range t.held {
		if !slices.Contains(out, h.dir) {
			out = append(out, h.dir)
		}
	}
	return out
}

// over says whether a turn's limit has passed, so the change stops before its next write.
func (t *turns) over() bool {
	for _, h := range t.held {
		if h.over.Load() {
			return true
		}
	}
	return false
}

// give lets go of every turn, last first, with the change's error; the queue's own answer, such as change_too_long, wins over it.
func (t *turns) give(err error) error {
	var queueErr error
	for i := len(t.held) - 1; i >= 0; i-- {
		h := t.held[i]
		h.release <- err
		if got := <-h.done; got != nil && !errors.Is(got, err) && queueErr == nil {
			queueErr = got
		}
	}
	t.held = nil
	if queueErr != nil && !errors.Is(err, errCrashed) {
		return queueErr
	}
	return err
}
