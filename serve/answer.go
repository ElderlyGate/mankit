// Package serve is the server every app shares: the guard, the contract, the answers, asking before a change, and serve --detach with servers, servers stop and servers link.
package serve

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/tugbayatilla/mankit/home"
)

// App is what an app tells serve about itself.
type App struct {
	ID        string   // the product ID: ran.app, apiVersion, the cookie and header names, the data folder
	Command   string   // the CLI's name, such as e-designer; empty is ID
	Version   string   // the app's version
	Contracts []string // the contract versions it answers, oldest first; the last is the default
	Addr      string   // the address serve binds by default, such as 127.0.0.1:7480
}

// The codes of the errors serve answers; a program decides by the code, never by the message (G-STACK-15).
const (
	CodeHost                = "host_refused"
	CodeOrigin              = "origin_refused"
	CodeToken               = "token_refused"
	CodeNotJSON             = "not_json"
	CodeUnsupportedContract = "unsupported_contract"
	CodeUnconfirmed         = "unconfirmed"
	CodeNotFound            = "not_found"
	CodeConfig              = "config_unreadable"
	CodeFailed              = "failed"
	CodeCoreNeedsValues     = "core_needs_values"
)

// Ran says where an answer came from: the thing, the app, its version and the contract (ADR-S08).
type Ran struct {
	Name     string `json:"name"`
	App      string `json:"app"`
	Version  string `json:"version"`
	Contract string `json:"contract"`
}

// Envelope is every answer, from the CLI and from HTTP alike (contracts envelope/v1).
type Envelope struct {
	APIVersion string  `json:"apiVersion"`
	Kind       string  `json:"kind"`
	Ran        Ran     `json:"ran"`
	Data       any     `json:"data"`
	Errors     []Error `json:"errors"`
}

// Error is one error of an answer (contracts error/v1); Fail makes one, and an answer fills in its apiVersion and kind.
type Error struct {
	APIVersion string   `json:"apiVersion"`
	Kind       string   `json:"kind"`
	Code       string   `json:"code"`
	Message    string   `json:"message"`
	Supported  []string `json:"supported,omitempty"`
}

func (e *Error) Error() string { return e.Message }

// Fail is an error with its code, for an answer.
func Fail(code, message string) *Error { return &Error{Code: code, Message: message} }

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
	err := Fail(CodeUnsupportedContract, fmt.Sprintf("%s does not answer contract %s; it answers %s", a.command(), asked, strings.Join(a.Contracts, ", ")))
	err.Supported = slices.Clone(a.Contracts)
	return "", err
}

// Answer is a good answer of kind about the thing name (empty is the app's ID), in contract.
func (a App) Answer(name, contract, kind string, data any) Envelope {
	if name == "" {
		name = a.ID
	}
	return Envelope{
		APIVersion: a.APIVersion(contract),
		Kind:       kind,
		Ran:        Ran{Name: name, App: a.ID, Version: a.Version, Contract: contract},
		Data:       data,
		Errors:     []Error{},
	}
}

// Failure is the answer that carries err; an error that is not an *Error gets CodeFailed.
func (a App) Failure(name, contract string, err error) Envelope {
	e := Fail(CodeFailed, err.Error())
	if errors.As(err, &e) {
		copied := *e
		e = &copied
	}
	e.APIVersion, e.Kind = a.APIVersion(contract), "Error"
	env := a.Answer(name, contract, "Error", nil)
	env.Errors = []Error{*e}
	return env
}

// Encode writes e as indented JSON, the same bytes from the CLI and from HTTP.
func Encode(w io.Writer, e Envelope) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(e)
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
