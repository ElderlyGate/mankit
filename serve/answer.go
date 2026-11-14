// Package serve is the server every app shares: the guard, the contract, the answers, asking before a change, and serve --detach with servers, servers stop and servers link.
package serve

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/ElderlyGate/contracts"

	"github.com/tugbayatilla/mankit/v3/home"
)

// App is what an app tells serve about itself.
type App struct {
	ID        string   // the product ID: source.app, apiVersion, the cookie name
	Command   string   // the CLI's name, such as e-designer; empty is ID
	Folder    string   // its folder under the org's config and data folders, where an app's folders are named apart from its ID until the rename; empty is ID
	Version   string   // the app's version
	Contracts []string // the contract versions it answers, oldest first; the last is the default
	Addr      string   // the address serve binds by default, such as 127.0.0.1:7480
	// EnvelopeV2From is the first of Contracts answered in envelope/v2 (source, Problems); empty means every contract stays on envelope/v1.
	// A contract an app has released keeps its shape (CT-RULE-1), so an app moves to envelope/v2 with its next contract version.
	EnvelopeV2From string
}

// The types of a Problem, a few general words a program decides by, never by detail (problem/v1, G-STACK-15).
const (
	TypeRefused             = "refused"
	TypeFailed              = "failed"
	TypeNotFound            = "not_found"
	TypeUnsupportedContract = "unsupported_contract"
	TypeUnread              = "unread"
	TypeUndeclared          = "undeclared"
	TypeOld                 = "old"
)

// The reasons serve gives with a type; problem/v1 says what each means.
const (
	ReasonToken            = "token"
	ReasonHost             = "host"
	ReasonOrigin           = "origin"
	ReasonNotJSON          = "not_json"
	ReasonUnconfirmed      = "unconfirmed"
	ReasonConfigUnreadable = "config_unreadable"
	ReasonCoreNeedsValues  = "core_needs_values"
)

// Source says where an answer comes from; contracts envelope/v2 describes each field and why it exists, and envelope/v1 names it ran.
type Source struct {
	Name     string `json:"name"`
	App      string `json:"app"`
	Version  string `json:"version"`
	Contract string `json:"contract"`
}

// Envelope is every answer, from the CLI and from HTTP alike: envelope/v2, or envelope/v1 for a contract before App.EnvelopeV2From.
type Envelope struct {
	APIVersion string    `json:"apiVersion"`
	Kind       string    `json:"kind"`
	Source     Source    `json:"source"`
	Data       any       `json:"data"`
	Errors     []Problem `json:"errors"`

	v1        bool     // written as envelope/v1, with ran and error/v1 items
	supported []string // the contracts an error/v1 unsupported_contract lists
}

// Problem is one thing that went wrong (contracts problem/v1); Fail makes one, and an answer fills in its apiVersion and kind.
type Problem struct {
	APIVersion string  `json:"apiVersion"`
	Kind       string  `json:"kind"`
	Type       string  `json:"type"`
	Title      string  `json:"title,omitempty"`
	Detail     string  `json:"detail"`
	Reason     string  `json:"reason,omitempty"`
	At         []Place `json:"at,omitempty"`
}

// Place is where a Problem points (problem/v1); a field it does not know is left empty.
type Place struct {
	File     string `json:"file,omitempty"`
	Document int    `json:"document,omitempty"`
	Line     int    `json:"line,omitempty"`
	Path     string `json:"path,omitempty"`
}

func (p *Problem) Error() string { return p.Detail }

// Fail is a Problem of typ, with reason when a program needs more than the type (empty for none), for an answer.
func Fail(typ, reason, detail string) *Problem {
	return &Problem{Type: typ, Reason: reason, Detail: detail}
}

// The apiVersion and kind every Problem carries (problem/v1).
const (
	problemAPIVersion = "elderlygate/contracts/problem/v1"
	problemKind       = "Problem"
)

// APIVersion is the apiVersion of the app's answers in contract: <org>/<id>/app/<contract>.
func (a App) APIVersion(contract string) string { return home.Org + "/" + a.ID + "/app/" + contract }

// Contract is the contract to answer when a reader asks for asked (--contract or Accept-Contract): the default when asked is empty, else asked if the app answers it.
func (a App) Contract(asked string) (string, error) {
	if asked == "" {
		return a.Contracts[len(a.Contracts)-1], nil
	}
	if slices.Contains(a.Contracts, asked) {
		return asked, nil
	}
	return "", Fail(TypeUnsupportedContract, "", fmt.Sprintf("%s does not answer contract %s; it answers %s", a.command(), asked, strings.Join(a.Contracts, ", ")))
}

// v1 reports whether contract is answered in envelope/v1: every contract before EnvelopeV2From, or all of them when it is empty.
func (a App) v1(contract string) bool {
	if a.EnvelopeV2From == "" {
		return true
	}
	from, at := slices.Index(a.Contracts, a.EnvelopeV2From), slices.Index(a.Contracts, contract)
	return from < 0 || at < from
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
		Errors:     []Problem{},
		v1:         a.v1(contract),
		supported:  slices.Clone(a.Contracts),
	}
}

// Failure is the answer that carries err; an error that is not a *Problem is a Problem of type failed.
// The answer's kind is Error in both envelopes: it says the answer failed, and each Problem in errors says why, so the two words never name one thing.
func (a App) Failure(name, contract string, err error) Envelope {
	p := Fail(TypeFailed, "", err.Error())
	if errors.As(err, &p) {
		copied := *p
		p = &copied
	}
	p.APIVersion, p.Kind = problemAPIVersion, problemKind
	env := a.Answer(name, contract, "Error", nil)
	env.Errors = []Problem{*p}
	return env
}

// codeOf is the error/v1 code a Problem had before Problems: the owner's words back from type and reason, else the reason, else the type.
func codeOf(p Problem) string {
	switch {
	case p.Type == TypeRefused && p.Reason == ReasonToken:
		return "token_refused"
	case p.Type == TypeRefused && p.Reason == ReasonHost:
		return "host_refused"
	case p.Type == TypeRefused && p.Reason == ReasonOrigin:
		return "origin_refused"
	case p.Reason != "":
		return p.Reason
	}
	return p.Type
}

// errorV1 is a Problem as contracts error/v1 writes it, for a contract still on envelope/v1.
type errorV1 struct {
	APIVersion string   `json:"apiVersion"`
	Kind       string   `json:"kind"`
	Code       string   `json:"code"`
	Message    string   `json:"message"`
	At         *atV1    `json:"at,omitempty"`
	Supported  []string `json:"supported,omitempty"`
}

type atV1 struct {
	File  string `json:"file,omitempty"`
	Field string `json:"field,omitempty"`
}

// MarshalJSON writes envelope/v2, or envelope/v1 for a contract before App.EnvelopeV2From, so an app's released contract keeps its bytes.
func (e Envelope) MarshalJSON() ([]byte, error) {
	if !e.v1 {
		type v2 Envelope
		return json.Marshal(v2(e))
	}
	out := struct {
		APIVersion string    `json:"apiVersion"`
		Kind       string    `json:"kind"`
		Ran        Source    `json:"ran"`
		Data       any       `json:"data"`
		Errors     []errorV1 `json:"errors"`
	}{APIVersion: e.APIVersion, Kind: e.Kind, Ran: e.Source, Data: e.Data, Errors: []errorV1{}}
	for _, p := range e.Errors {
		item := errorV1{APIVersion: e.APIVersion, Kind: "Error", Code: codeOf(p), Message: p.Detail}
		if len(p.At) > 0 && (p.At[0].File != "" || p.At[0].Path != "") {
			item.At = &atV1{File: p.At[0].File, Field: p.At[0].Path}
		}
		if item.Code == TypeUnsupportedContract {
			item.Supported = e.supported
		}
		out.Errors = append(out.Errors, item)
	}
	return json.Marshal(out)
}

// UnmarshalJSON reads envelope/v2, and envelope/v1 through contracts' upgrade step, so a reader such as servers reads an app that has not moved yet.
func (e *Envelope) UnmarshalJSON(b []byte) error {
	var probe struct {
		Ran json.RawMessage `json:"ran"`
	}
	if err := json.Unmarshal(b, &probe); err != nil {
		return err
	}
	if probe.Ran != nil {
		up, err := contracts.UpgradeEnvelope(b)
		if err != nil {
			return err
		}
		b = up
	}
	type v2 Envelope
	var out v2
	if err := json.Unmarshal(b, &out); err != nil {
		return err
	}
	*e = Envelope(out)
	return nil
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
	return nil
}
