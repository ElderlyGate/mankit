// Package helpout gives a cobra app the shared help layout (H-1) for `<app> help` and `<app> --help`, and for a usage mistake.
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
)

// Info is what an app says about itself; the commands and flags come from cobra.
type Info struct {
	What  string   // one line: what the app is
	Start []string // how to start it and where to run it
	More  string   // the repository URL
}

// Apply makes root's help and usage the H-1 layout; a verb's own help stays cobra's.
func Apply(root *cobra.Command, info Info) {
	verbHelp, verbUsage := root.HelpFunc(), root.UsageFunc()
	root.SetHelpFunc(func(c *cobra.Command, args []string) {
		if c != root {
			verbHelp(c, args)
			return
		}
		write(c.OutOrStdout(), root, info)
	})
	root.SetUsageFunc(func(c *cobra.Command) error {
		if c != root {
			return verbUsage(c)
		}
		write(c.OutOrStderr(), root, info)
		return nil
	})
}

// mistake is a usage mistake on cmd: the arguments do not fit it.
type mistake struct {
	cmd *cobra.Command
	err error
}

func (m *mistake) Error() string { return m.err.Error() }

// Execute runs root: 0 when it went well, 2 after printing a usage mistake and its usage to stderr, 1 and the run-time error untouched otherwise.
func Execute(ctx context.Context, root *cobra.Command) (code int, err error) {
	root.SilenceErrors, root.SilenceUsage = true, true
	root.InitDefaultHelpCmd()
	root.SetFlagErrorFunc(func(c *cobra.Command, err error) error { return &mistake{c, err} })
	wrapArgs(root)
	if help, _, err := root.Find([]string{"help"}); err == nil && help != root {
		rejectUnknownTopics(root, help)
	}
	cmd, err := root.ExecuteContextC(ctx)
	var m *mistake
	switch {
	case err == nil:
		return 0, nil
	case errors.As(err, &m):
		cmd = m.cmd
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

// write prints the layout: what, Start, Commands, Flags, More; an empty section is left out.
func write(w io.Writer, root *cobra.Command, info Info) {
	if info.What == "" {
		fmt.Fprintln(w, root.Name())
	} else {
		fmt.Fprintf(w, "%s: %s\n", root.Name(), info.What)
	}
	if len(info.Start) > 0 {
		fmt.Fprint(w, "\nStart:\n")
		for _, line := range info.Start {
			fmt.Fprintf(w, "  %s\n", line)
		}
	}
	var verbs []*cobra.Command
	width := 0
	for _, c := range root.Commands() {
		if c.IsAvailableCommand() || c.Name() == "help" {
			verbs = append(verbs, c)
			width = max(width, runewidth.StringWidth(c.Name()))
		}
	}
	if len(verbs) > 0 {
		fmt.Fprint(w, "\nCommands:\n")
		for _, c := range verbs {
			fmt.Fprintf(w, "  %s   %s\n", runewidth.FillRight(c.Name(), width), c.Short)
		}
	}
	own := pflag.NewFlagSet(root.Name(), pflag.ContinueOnError)
	root.LocalFlags().VisitAll(func(f *pflag.Flag) {
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
