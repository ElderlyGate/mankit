// Package helpout gives a cobra app the shared help layout (H-1) for `<app> help` and `<app> --help`, for `<app> <thing> help` at every level, and for a usage mistake.
package helpout

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/mattn/go-runewidth"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/tugbayatilla/mankit/v4/ask"
)

// Info is what an app says about itself; the commands and flags come from cobra.
type Info struct {
	What  string   // one line: what the app is
	Start []string // how to start it and where to run it
	More  string   // the repository URL
	// Answer, when set, gives c the app's -o and --contract flags and prints c's answer as the app's other commands do, so help -o json is the app's envelope: an app passes func(c *cobra.Command) helpout.Answerer { return app.Printer(c) }.
	Answer func(c *cobra.Command) Answerer
}

// Apply makes the help and usage of root and of every thing under it, a command with commands of its own, the H-1 layout, and gives help -o json and yaml; a verb's own help stays cobra's.
func Apply(root *cobra.Command, info Info) {
	answerHelp(root, info)
	verbHelp, verbUsage := root.HelpFunc(), root.UsageFunc()
	root.SetHelpFunc(func(c *cobra.Command, args []string) {
		if !laidOut(root, c) {
			verbHelp(c, args)
			return
		}
		write(c.OutOrStdout(), root, c, info)
	})
	root.SetUsageFunc(func(c *cobra.Command) error {
		if !laidOut(root, c) {
			return verbUsage(c)
		}
		write(c.OutOrStderr(), root, c, info)
		return nil
	})
}

// laidOut says whether c's help is the layout: root's, and every thing's.
func laidOut(root, c *cobra.Command) bool { return c == root || c.HasAvailableSubCommands() }

// mistake is a usage mistake on cmd: the arguments do not fit it.
type mistake struct {
	cmd *cobra.Command
	err error
}

func (m *mistake) Error() string { return m.err.Error() }

// Printed is a failure the command has already given in full, in its answer: Execute exits 1 and nothing more is printed, so an -o json answer stays one envelope.
type Printed struct{ Err error }

func (p Printed) Error() string { return p.Err.Error() }
func (p Printed) Unwrap() error { return p.Err }

// Execute runs root: 0 when it went well, 2 after printing a usage mistake and its usage to stderr, 3 and the error for an ask refusal, 1 and no error for a failure already Printed, 1 and the run-time error untouched otherwise.
func Execute(ctx context.Context, root *cobra.Command) (code int, err error) {
	root.SilenceErrors, root.SilenceUsage = true, true
	root.InitDefaultHelpCmd()
	addThingHelp(root)
	if err := addOld(root); err != nil {
		return 1, err
	}
	root.SetFlagErrorFunc(func(c *cobra.Command, err error) error { return &mistake{c, err} })
	wrapArgs(root)
	if help, _, err := root.Find([]string{"help"}); err == nil && help != root {
		rejectUnknownTopics(root, help)
	}
	cmd, err := root.ExecuteContextC(ctx)
	// An old form says its new name on stderr, then its full form runs, once.
	var old *rerun
	if errors.As(err, &old) {
		fmt.Fprintf(root.ErrOrStderr(), "%s: %s\n", root.Name(), old.Error())
		root.SetArgs(old.args)
		cmd, err = root.ExecuteContextC(ctx)
	}
	var m *mistake
	switch {
	case err == nil:
		return 0, nil
	case errors.As(err, &m):
		cmd = m.cmd
	case errors.Is(err, ask.ErrRefused):
		return 3, err
	case errors.As(err, new(Printed)):
		return 1, nil
	case cmd != root || !root.TraverseChildren || !isFlagError(err):
		return 1, err
	}
	w := cmd.ErrOrStderr()
	fmt.Fprintln(w, "error:", err)
	fmt.Fprint(w, cmd.UsageString())
	return 2, nil
}

// wrapArgs makes every command's Args error a usage mistake; a command with verbs takes no other word unless its Args says so.
// A command with verbs and no Run gets one that shows its help, so its PersistentPreRun hooks now run first.
func wrapArgs(c *cobra.Command) {
	if c.HasSubCommands() {
		if c.Args == nil {
			c.Args = noVerb
		}
		if !c.Runnable() {
			c.RunE = func(c *cobra.Command, _ []string) error { return c.Help() }
		}
	}
	if check := c.Args; check != nil {
		c.Args = func(cmd *cobra.Command, args []string) error {
			if err := check(cmd, args); err != nil {
				return &mistake{cmd, err}
			}
			return nil
		}
	}
	for _, sub := range c.Commands() {
		wrapArgs(sub)
	}
}

// noVerb refuses any word: it names the first and, as cobra does, the verbs it may have meant.
func noVerb(c *cobra.Command, args []string) error {
	if len(args) == 0 {
		return nil
	}
	msg := fmt.Sprintf("unknown command %q for %q", args[0], c.CommandPath())
	if s := c.SuggestionsFor(args[0]); len(s) > 0 && !c.Root().DisableSuggestions {
		msg += "\n\nDid you mean this?\n\t" + strings.Join(s, "\n\t")
	}
	return errors.New(msg)
}

// addThingHelp gives every thing under root a help verb, so `<app> <thing> help` shows the thing's layout as `<app> help <thing>` does.
func addThingHelp(root *cobra.Command) {
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		for _, sub := range c.Commands() {
			if sub.HasSubCommands() && child(sub, "help") == nil && !isOld(sub) {
				sub.AddCommand(&cobra.Command{
					Use:   "help",
					Short: "Help about " + sub.Name(),
					Args:  cobra.NoArgs,
					RunE:  func(c *cobra.Command, _ []string) error { return c.Parent().Help() },
				})
			}
			walk(sub)
		}
	}
	walk(root)
}

// rejectUnknownTopics makes `help <topic>` a usage mistake on root when no command matches the topic.
func rejectUnknownTopics(root, help *cobra.Command) {
	run, runE := help.Run, help.RunE
	help.RunE = func(c *cobra.Command, args []string) error {
		if _, rest, err := root.Find(args); err != nil || len(rest) > 0 {
			return &mistake{root, fmt.Errorf("unknown help topic %q", strings.Join(args, " "))}
		}
		if runE != nil {
			return runE(c, args)
		}
		run(c, args)
		return nil
	}
}

// isFlagError is pflag's own error, as TraverseChildren returns it for a flag before the verb, bypassing FlagErrorFunc.
func isFlagError(err error) bool {
	switch err.(type) {
	case *pflag.NotExistError, *pflag.ValueRequiredError, *pflag.InvalidValueError, *pflag.InvalidSyntaxError:
		return true
	}
	return false
}

// write prints c's layout: what, Start, Commands, Flags, More; an empty section is left out. A thing's first line is its own short line, and Start is root's alone.
func write(w io.Writer, root, c *cobra.Command, info Info) {
	what := info.What
	if c != root {
		what = c.Short
	}
	if what == "" {
		fmt.Fprintln(w, c.CommandPath())
	} else {
		fmt.Fprintf(w, "%s: %s\n", c.CommandPath(), what)
	}
	if c == root && len(info.Start) > 0 {
		fmt.Fprint(w, "\nStart:\n")
		for _, line := range info.Start {
			fmt.Fprintf(w, "  %s\n", line)
		}
	}
	var verbs []*cobra.Command
	width := 0
	for _, sub := range c.Commands() {
		if sub.IsAvailableCommand() || sub.Name() == "help" {
			verbs = append(verbs, sub)
			width = max(width, runewidth.StringWidth(sub.Name()))
		}
	}
	if len(verbs) > 0 {
		fmt.Fprint(w, "\nCommands:\n")
		for _, sub := range verbs {
			fmt.Fprintf(w, "  %s   %s\n", runewidth.FillRight(sub.Name(), width), sub.Short)
		}
	}
	own := pflag.NewFlagSet(c.Name(), pflag.ContinueOnError)
	c.LocalFlags().VisitAll(func(f *pflag.Flag) {
		if f.Name != "help" {
			own.AddFlag(f)
		}
	})
	if lines := own.FlagUsages(); lines != "" {
		fmt.Fprintf(w, "\nFlags:\n%s", lines)
	}
	if info.More != "" {
		fmt.Fprintf(w, "\nMore: %s\n", info.More)
	}
}
