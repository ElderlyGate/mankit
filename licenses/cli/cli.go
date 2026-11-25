// Package cli is the thing license every app mounts at its root: `license show` and `license add [--org] <file>`, above licenses, so licenses imports neither serve nor ask.
package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"go.naywee.com/mankit/v5/ask"
	"go.naywee.com/mankit/v5/features"
	"go.naywee.com/mankit/v5/licenses"
	"go.naywee.com/mankit/v5/problem"
	"go.naywee.com/mankit/v5/serve"
)

// now is the day the Checker reads by; nil is time.Now, and only a test hands in its own.
var now func() time.Time

// Checker is the one way an app builds its license Checker, so its ID, title and folder come from a alone; trust is licenses.Trusted() in every app.
func Checker(a serve.App, trust licenses.Trust) (*licenses.Checker, error) {
	return licenses.New(licenses.Config{ID: a.ID, Title: a.Name(), Folder: a.Folder, Trust: trust, Now: now})
}

// Command is the thing license of app a, printed through serve's Printer so every app answers alike: c reads its licenses, list holds its features closed by c's Closer, and add asks with confirm.
func Command(a serve.App, c *licenses.Checker, list *features.List, confirm func(*cobra.Command, ask.Action) error) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "license",
		Short: "The licenses that open this app's enterprise features",
		Long: "A license names whom it is for, the enterprise features it opens and its last day.\n" +
			"The org license is read by every app, a product license or trial key by this app alone.\n" +
			"`license show` lists them and every feature with where it is unlocked from;\n" +
			"`license add <file>` keeps one for this app, `license add --org <file>` for every app.",
	}
	show := &cobra.Command{
		Use:   "show",
		Short: "Show the licenses and where each feature is unlocked from",
		Args:  cobra.NoArgs,
	}
	showOut := a.Printer(show)
	show.RunE = func(cc *cobra.Command, _ []string) error {
		if err := showOut.Check(); err != nil {
			return err
		}
		return answer(cc, showOut, c, list)
	}
	add := &cobra.Command{
		Use:   "add <file>",
		Short: "Check a license file and keep it for this app, or with --org for every app",
		Args:  cobra.ExactArgs(1),
	}
	org := add.Flags().Bool("org", false, "keep it as the org license, which every app reads")
	addOut := a.Printer(add)
	ask.Flag(add)
	add.RunE = func(cc *cobra.Command, args []string) error {
		if err := addOut.Check(); err != nil {
			return err
		}
		raw, err := os.ReadFile(args[0])
		if err != nil {
			return problem.FailAt(problem.TypeRefused, licenses.ReasonUnreadable, args[0], fmt.Sprintf("%s cannot be read: %v", args[0], err))
		}
		t, err := c.Check(raw, *org)
		if err != nil {
			return failAt(err, args[0])
		}
		found, err := c.Read()
		if err != nil {
			return err
		}
		what := fmt.Sprintf("keep the %s license for %s until %s as %s, in %s%s", t.Kind, t.For, t.LastDay.Format("2006-01-02"), whose(*org, a.ID), t.File, replacing(found, t.File))
		if err := confirm(cc, ask.Action{Type: ask.Settings, What: what, Name: "license"}); err != nil {
			return err
		}
		if _, err := c.Add(cc.Context(), raw, *org); err != nil {
			return err
		}
		return answer(cc, addOut, c, list)
	}
	cmd.AddCommand(show, add)
	return cmd
}

func whose(org bool, app string) string {
	if org {
		return "the org's, for every app"
	}
	return app + "'s own"
}

// replacing names what add replaces at path, so the question says it (G-STACK-13).
func replacing(f licenses.Found, path string) string {
	for _, t := range f.Taken {
		if t.File == path {
			return fmt.Sprintf(", replacing the %s license for %s until %s", t.Kind, t.For, t.LastDay.Format("2006-01-02"))
		}
	}
	for _, r := range f.Refused {
		if r.File == path {
			return fmt.Sprintf(", replacing the file there, which is refused (%s)", r.Reason)
		}
	}
	return ""
}

// failAt is err pointing at the file it is about.
func failAt(err error, file string) error {
	var p *problem.Problem
	if errors.As(err, &p) {
		return problem.FailAt(p.Type, p.Reason, file, file+": "+p.Detail)
	}
	return err
}

// answer prints the license show answer: the envelope with -o json or yaml, else its sentence and its tables.
func answer(cc *cobra.Command, out serve.Printer, c *licenses.Checker, list *features.List) error {
	s, err := c.Show(list)
	if err != nil {
		return err
	}
	return out.Print(cc.OutOrStdout(), "", licenses.ShowKind, s, func(w io.Writer) {
		tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		if s.Sentence != "" {
			fmt.Fprintf(tw, "%s\n\n", s.Sentence)
		}
		fmt.Fprintf(tw, "STATE\t%s\nRELEASE\t%t\n\n", s.State, s.Build.Release)
		if len(s.Licenses) > 0 {
			fmt.Fprintln(tw, "KIND\tFOR\tUNTIL\tDAYS LEFT\tKEY")
			for _, l := range s.Licenses {
				fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t%s\n", l.Kind, l.For, ends(l), l.DaysLeft, l.KeyID)
			}
			fmt.Fprintln(tw)
		}
		fmt.Fprintln(tw, "FEATURE\tTIER\tON\tFROM\tUNTIL")
		for _, f := range s.Features {
			until := f.Until
			if until == "" {
				until = "–"
			}
			fmt.Fprintf(tw, "%s\t%s\t%t\t%s\t%s\n", f.ID, f.Tier, f.On, f.From, until)
		}
		if len(s.Refused) > 0 {
			fmt.Fprintln(tw, "\nREFUSED\tREASON")
			for _, r := range s.Refused {
				fmt.Fprintf(tw, "%s\t%s\n", r.File, r.Reason)
			}
		}
		_ = tw.Flush()
	})
}

// ends is the day a row's UNTIL column shows: a release-trial's trial end once started, else its offer end, as contracts mankit/v1 license-show says; every other kind's until.
func ends(l licenses.ShownFile) string {
	switch {
	case l.Kind != licenses.ReleaseTrial:
		return l.Until
	case l.TrialEnds != "":
		return l.TrialEnds
	}
	return l.OfferEnds
}
