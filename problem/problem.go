// Package problem is a Problem, one thing that went wrong (contracts problem/v1), with the types a program decides by; it sits below everything that makes one, so the storage port and the server say what went wrong the same way.
package problem

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

// The apiVersion and kind every Problem carries (problem/v1), filled in where one is answered or logged.
const (
	APIVersion = "elderlygate/contracts/problem/v1"
	Kind       = "Problem"
)

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
// problem/v1 requires a detail, so serve's Failure writes the type and reason as the detail when it is empty.
func Fail(typ, reason, detail string) *Problem {
	return &Problem{Type: typ, Reason: reason, Detail: detail}
}

// FailAt is Fail pointing at file, a key or a path, when it names one.
func FailAt(typ, reason, file, detail string) *Problem {
	p := Fail(typ, reason, detail)
	if file != "" {
		p.At = []Place{{File: file}}
	}
	return p
}
