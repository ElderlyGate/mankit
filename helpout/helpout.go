// Package helpout gives a cobra app the shared help layout (H-1) for `<app> help` and `<app> --help`.
package helpout

import (
	"fmt"
	"io"

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
