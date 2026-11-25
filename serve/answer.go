// Package serve is the server every app shares: the guard, the contract, the answers, asking before a change, and serve --detach with server list, server stop and server link.
package serve

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strings"

	"go.naywee.com/mankit/v5/home"
	"go.naywee.com/mankit/v5/problem"
)

// App is what an app tells serve about itself.
type App struct {
	ID        string     // the product ID: source.app, apiVersion, the cookie name
	Command   string     // the CLI's name, such as e-designer; empty is ID
	Title     string     // the app's name people read, such as Designer; empty is ID
	Folder    string     // its folder under the org's config and data folders, where an app's folders are named apart from its ID until the rename; empty is ID
	Version   string     // the app's version
	Contracts []Contract // the contract versions it answers, each with its envelope, oldest first; the last is the default
	Addr      string     // the address serve binds by default, such as 127.0.0.1:7480
}

// Contract is one contract version an app answers, and the envelope its answers come in.
// Envelope is "v2" (source, Problems), the one envelope serve writes; empty is "v2". An older envelope is refused when serve starts.
// When envelope/v3 comes, it is a new Envelope value, never a new field.
type Contract struct {
	Version  string
	Envelope string
}

// contractVersion is the form of a contract version (contracts envelope/v2 source.contract).
var contractVersion = regexp.MustCompile(`^v[0-9]+$`)

// envelope is the one envelope serve writes (contracts envelope/v2).
const envelope = "v2"

// versions are the contract versions the app answers, oldest first.
func (a App) versions() []string {
	out := make([]string, len(a.Contracts))
	for i, c := range a.Contracts {
		out[i] = c.Version
	}
	return out
}

// The reasons serve gives with a type; problem/v1 says what each means.
const (
	ReasonToken            = "token"
	ReasonHost             = "host"
	ReasonOrigin           = "origin"
	ReasonNotJSON          = "not_json"
	ReasonUnconfirmed      = "unconfirmed"
	ReasonConfigUnreadable = "config_unreadable"
	ReasonCoreNeedsValues  = "core_needs_values"
	ReasonTooLarge         = "too_large"
	// servers --check: no server is listed, or one listed does not answer.
	ReasonNoneListed   = "none_listed"
	ReasonNotAnswering = "not_answering"
)

// Source says where an answer comes from; contracts envelope/v2 describes each field and why it exists.
type Source struct {
	Name     string `json:"name"`
	App      string `json:"app"`
	Version  string `json:"version"`
	Contract string `json:"contract"`
}

// Envelope is every answer, from the CLI and from HTTP alike, in contracts envelope/v2.
type Envelope struct {
	APIVersion string            `json:"apiVersion"`
	Kind       string            `json:"kind"`
	Source     Source            `json:"source"`
	Data       any               `json:"data"`
	Errors     []problem.Problem `json:"errors"`
}

// APIVersion is the apiVersion of the app's answers in contract: <org>/<id>/app/<contract>.
func (a App) APIVersion(contract string) string { return home.Org + "/" + a.ID + "/app/" + contract }

// Contract is the contract to answer when a reader asks for asked (--contract or Accept-Contract): the default when asked is empty, else asked if the app answers it.
func (a App) Contract(asked string) (string, error) {
	if asked == "" {
		return a.Contracts[len(a.Contracts)-1].Version, nil
	}
	if slices.Contains(a.versions(), asked) {
		return asked, nil
	}
	return "", problem.Fail(problem.TypeUnsupportedContract, "", fmt.Sprintf("%s does not answer contract %s; it answers %s", a.command(), asked, strings.Join(a.versions(), ", ")))
}

// Answer is a good answer of kind about the thing name (empty is the app's ID), in contract.
func (a App) Answer(name, contract, kind string, data any) Envelope {
	if name == "" {
		name = a.ID
	}
	return Envelope{
		APIVersion: a.APIVersion(contract),
		Kind:       kind,
		Source:     Source{Name: name, App: a.ID, Version: a.Version, Contract: contract},
		Data:       data,
		Errors:     []problem.Problem{},
	}
}

// Failure is the answer that carries err; an error that is not a *Problem is a Problem of type failed.
// The answer's kind is Error: it says the answer failed, and each Problem in errors says why, so the two words never name one thing.
func (a App) Failure(name, contract string, err error) Envelope {
	env := a.Answer(name, contract, "Error", nil)
	env.Errors = []problem.Problem{problemOf(err)}
	return env
}

// problemOf is err as the Problem an answer carries: its own when it is a *Problem, else one of type failed, filled in as problem/v1 asks.
func problemOf(err error) problem.Problem {
	p := problem.Fail(problem.TypeFailed, "", err.Error())
	if errors.As(err, &p) {
		copied := *p
		p = &copied
	}
	p.APIVersion, p.Kind = problem.APIVersion, problem.Kind
	if p.Detail == "" {
		p.Detail = strings.TrimSuffix(p.Type+": "+p.Reason, ": ")
	}
	// A place must say at least one thing (problem/v1), so an empty one is dropped.
	p.At = slices.DeleteFunc(slices.Clone(p.At), func(at problem.Place) bool { return at == (problem.Place{}) })
	if len(p.At) == 0 {
		p.At = nil
	}
	return *p
}

// Encode writes e as indented JSON, the same bytes from the CLI and from HTTP.
func Encode(w io.Writer, e Envelope) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(e)
}

func (a App) folder() string {
	if a.Folder != "" {
		return a.Folder
	}
	return a.ID
}

// Name is the app's name people read in its texts: its Title, else its ID.
func (a App) Name() string {
	if a.Title != "" {
		return a.Title
	}
	return a.ID
}

func (a App) command() string {
	if a.Command != "" {
		return a.Command
	}
	return a.ID
}

// check refuses an App serve cannot answer for.
func (a App) check() error {
	if a.ID == "" || a.Version == "" || len(a.Contracts) == 0 {
		return errors.New("serve: an App needs its ID, its Version and at least one contract")
	}
	seen := map[string]bool{}
	for _, c := range a.Contracts {
		switch {
		case !contractVersion.MatchString(c.Version):
			return fmt.Errorf("serve: contract %q is not vN", c.Version)
		case c.Envelope != "" && c.Envelope != envelope:
			return fmt.Errorf("serve: contract %s names the envelope %q; serve writes only envelope/%s, so leave Envelope empty or set it to %q", c.Version, c.Envelope, envelope, envelope)
		case seen[c.Version]:
			return fmt.Errorf("serve: contract %s is given twice", c.Version)
		}
		seen[c.Version] = true
	}
	return nil
}
