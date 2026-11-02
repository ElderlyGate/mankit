// Package ask is how a CLI asks before it changes anything: y/N in a terminal, refused without --yes elsewhere.
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

// Change asks before cmd makes the change what: nil with --yes or a yes in the terminal, an ErrRefused otherwise.
func Change(cmd *cobra.Command, what string) error {
	if f := cmd.Flag(yesFlag); f != nil && f.Value.String() == "true" {
		return nil
	}
	in, out := cmd.InOrStdin(), cmd.ErrOrStderr()
	if !isTerminal(in, out) {
		return fmt.Errorf("%w: %s; pass --yes to make the change", ErrRefused, what)
	}
	fmt.Fprintf(out, "%s\nProceed? [y/N] ", what)
	answer, err := bufio.NewReader(in).ReadString('\n')
	if err != nil {
		fmt.Fprintln(out)
		answer = ""
	}
	if a := strings.ToLower(strings.TrimSpace(answer)); a == "y" || a == "yes" {
		return nil
	}
	return fmt.Errorf("%w: %s", ErrRefused, what)
}
