// Package ask is how a CLI asks before it changes anything, in the style the user picked for that type of change; with no terminal it refuses without --yes.
package ask

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// ErrRefused is wrapped by every refusal, so a caller can tell a refusal from a failure.
var ErrRefused = errors.New("refused")

// yesFlag is the flag that makes a change without asking.
const yesFlag = "yes"

// Action is one change: its type, what it will change, and the name a name question asks to type.
type Action struct {
	Type Type
	What string
	Name string
}

// Answer is a yes; Remember means the user asked to stop asking for this type: the caller saves never for it in the product's own config.
type Answer struct {
	Remember bool
}

// isTerminal reports whether the question can be asked: stdin and stderr are both a terminal.
var isTerminal = func(in io.Reader, out io.Writer) bool {
	return terminal(in) && terminal(out)
}

func terminal(stream any) bool {
	f, ok := stream.(*os.File)
	return ok && term.IsTerminal(int(f.Fd()))
}

// Flag adds --yes to cmd, the one way to make a change without being asked.
func Flag(cmd *cobra.Command) {
	cmd.Flags().Bool(yesFlag, false, "make the change without asking")
}

// Change asks before cmd makes a in style (empty or unknown: the type's default, name without a Name: yes-no); a no is an ErrRefused.
func Change(cmd *cobra.Command, a Action, style Style) (Answer, error) {
	if f := cmd.Flag(yesFlag); f != nil && f.Value.String() == "true" {
		return Answer{}, nil
	}
	in, out := cmd.InOrStdin(), cmd.ErrOrStderr()
	if !isTerminal(in, out) {
		return Answer{}, fmt.Errorf("%w: %s; pass --yes to make the change", ErrRefused, a.What)
	}
	style = a.style(style)
	if style == Never {
		return Answer{}, nil
	}
	fmt.Fprintf(out, "%s\n%s", a.What, a.question(style))
	reply, err := bufio.NewReader(in).ReadString('\n')
	if err != nil {
		fmt.Fprintln(out)
	} else if answer, yes := a.answer(style, strings.TrimSpace(reply)); yes {
		return answer, nil
	}
	return Answer{}, fmt.Errorf("%w: %s", ErrRefused, a.What)
}

// style is the style the question takes: the type's default for an empty or unknown one, yes-no for name when there is no name to type.
func (a Action) style(s Style) Style {
	if _, err := ParseStyle(string(s)); err != nil {
		s = Default(a.Type)
	}
	if s == Name && a.Name == "" {
		return YesNo
	}
	return s
}

func (a Action) question(s Style) string {
	if s == Name {
		return fmt.Sprintf("Type %s to go ahead (add ! to stop asking for %s): ", a.Name, a.Type)
	}
	return "Proceed? [y/N/a] "
}

// answer reads reply: whether it says yes, and whether it asks to stop asking.
func (a Action) answer(s Style, reply string) (Answer, bool) {
	if s == Name {
		return Answer{Remember: reply == a.Name+"!"}, reply == a.Name || reply == a.Name+"!"
	}
	switch strings.ToLower(reply) {
	case "y", "yes":
		return Answer{}, true
	case "a", "always":
		return Answer{Remember: true}, true
	}
	return Answer{}, false
}
