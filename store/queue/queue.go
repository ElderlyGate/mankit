// Package queue, the store's, shows the change queue (ADR-S14) and sets its waits: the tickets waiting in a line, the rows queue.wait and queue.limit, and the thing <app> queue.
// The turns themselves are the store's own (ADR-S22): a change takes one through store.Scope.Change, never here.
package queue

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.naywee.com/mankit/v5/problem"
	"go.naywee.com/mankit/v5/serve"
	"go.naywee.com/mankit/v5/settings"
	engine "go.naywee.com/mankit/v5/store/internal/queue"
)

// The reasons of the queue's Problems, each with the type failed; a program decides by type and reason, never by the detail (G-STACK-15).
const (
	ReasonTimeout            = engine.ReasonTimeout            // the change waited longer than its wait for its turn and did not run
	ReasonNotReadBack        = engine.ReasonNotReadBack        // the change ran, but what it wrote could not be read back
	ReasonLost               = engine.ReasonLost               // another process took the waiting ticket for stale and moved it to bad/
	ReasonUnusable           = engine.ReasonUnusable           // the queue's folder cannot be made, read or written
	ReasonBackendUnsupported = engine.ReasonBackendUnsupported // .queue/backend names a backend this build does not run, or cannot be read
	ReasonTooLong            = engine.ReasonTooLong            // the app's own write held the turn past its limit
)

// The waits an app gets when it sets none.
const (
	DefaultWait  = engine.DefaultWait
	DefaultLimit = engine.DefaultLimit
)

// Kind is the kind of a ticket, ListKind the kind of the answer <app> queue gives, and BackendKind the kind of .queue/backend (contracts mankit/v2).
const (
	Kind        = engine.Kind
	ListKind    = engine.ListKind
	BackendKind = engine.BackendKind
)

// The backends a marker may name; this build runs files alone.
const (
	BackendFiles       = engine.BackendFiles
	BackendLockService = engine.BackendLockService
)

// APIVersion is the apiVersion of the queue's files, a ticket or the backend marker.
func APIVersion() string { return engine.APIVersion() }

// The forms List gives (contracts mankit/v2 queue-list); their home is the queue itself.
type (
	Listing  = engine.Listing
	Ticket   = engine.Ticket
	Metadata = engine.Metadata
	Spec     = engine.Spec
	Place    = engine.Place
	Bad      = engine.Bad
)

// List is what <app> queue shows of the line whose app folder is folder, as app at version sees it: the tickets in order, which one's turn it is, and what was set aside.
func List(ctx context.Context, folder, app, version string) (Listing, error) {
	return engine.Queue{Folder: folder, App: app, Version: version}.List(ctx)
}

// minSetting is the least a user may set queue.wait or queue.limit to, so a typo such as 1ms does not fail every change.
const minSetting = time.Second

// WaitKey and LimitKey are the rows every app adds to its settings, so users set the queue's waits the same way in each.
var (
	WaitKey  = settings.Key{Name: "queue.wait", Default: DefaultWait.String(), Help: "how long a change waits for its turn before it gives up"}
	LimitKey = settings.Key{Name: "queue.limit", Default: DefaultLimit.String(), Help: "how long a change may hold its turn before it is stopped"}
)

// Duration is a value of queue.wait or queue.limit as the queue takes it: a duration of at least a second in whole seconds, the one rule contracts mankit/v2 queue-settings states; its d.String() (45s, 2m0s) is the form written and shown.
// What it refuses is said with ADR-S21's reasons: wrong_type for no duration, out_of_range for one under a second or not in whole seconds, empty for nothing.
func Duration(key settings.Key, value string) (time.Duration, error) {
	if value == "" {
		return 0, problem.Fail(problem.TypeRefused, "empty", fmt.Sprintf("%s needs a duration of at least %s in whole seconds, such as %s", key.Name, minSetting, key.Default))
	}
	d, err := time.ParseDuration(value)
	if err != nil {
		return 0, problem.Fail(problem.TypeRefused, "wrong_type", fmt.Sprintf("%s is %q, which is no duration; it takes one of at least %s in whole seconds, such as %s", key.Name, value, minSetting, key.Default))
	}
	if d < minSetting || d%time.Second != 0 {
		return 0, problem.Fail(problem.TypeRefused, "out_of_range", fmt.Sprintf("%s is %q; it takes a duration of at least %s in whole seconds, such as %s", key.Name, value, minSetting, key.Default))
	}
	return d, nil
}

// Settings are the wait and limit the rows queue.wait and queue.limit say, zero where a row is empty, as store.App takes them; a value Duration refuses is the app's config that cannot be read, with serve's config code.
func Settings(wait, limit string) (waitFor, limitTo time.Duration, err error) {
	for _, row := range []struct {
		key   settings.Key
		value string
		to    *time.Duration
	}{{WaitKey, wait, &waitFor}, {LimitKey, limit, &limitTo}} {
		if row.value == "" {
			continue
		}
		d, err := Duration(row.key, row.value)
		var p *problem.Problem
		if errors.As(err, &p) {
			return 0, 0, problem.Fail(problem.TypeFailed, serve.ReasonConfigUnreadable, p.Detail)
		}
		*row.to = d
	}
	return waitFor, limitTo, nil
}
