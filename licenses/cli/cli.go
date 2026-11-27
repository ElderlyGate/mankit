// Package cli is the thing license every app mounts at its root: `license show`, `license add <file>` and `license remove`, above licenses, so licenses imports neither serve nor ask.
package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/tugbayatilla/mankit/v4/ask"
	"github.com/tugbayatilla/mankit/v4/features"
	"github.com/tugbayatilla/mankit/v4/licenses"
	"github.com/tugbayatilla/mankit/v4/problem"
	"github.com/tugbayatilla/mankit/v4/serve"
)

// now is the day the Checker reads by; nil is time.Now, and only a test hands in its own.
var now func() time.Time

// line is the product line the Checker reads by; 0 is the build stamp (build.Line()), and only a test hands in its own.
var line int

// Checker is the one way an app builds its license Checker, so its ID, title and folder come from a alone and its line from the build stamp; trust is licenses.Trusted() in every app.
func Checker(a serve.App, trust licenses.Trust) (*licenses.Checker, error) {
	return licenses.New(licenses.Config{ID: a.ID, Title: a.Name(), Line: line, Folder: a.Folder, Trust: trust, Now: now})
}

// Command is the thing license of app a, printed through serve's Printer so every app answers alike: c reads its licenses, list holds its features closed by c's Closer, and add and remove ask with confirm.
func Command(a serve.App, c *licenses.Checker, list *features.List, confirm func(*cobra.Command, ask.Action) error) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "license",
		Short: "The license and trial that open this app's enterprise features",
		Long: "A license names whom it is for and the enterprise features it opens; one license file is read by every app.\n" +
			"A trial is built into the app and starts by itself.\n" +
			"`license show` lists them and every feature with where it is unlocked from;\n" +
			"`license add <file>` keeps the license, `license remove` takes it away, and the trial stays.",
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
		Short: "Check a license file and keep it as the license every app reads",
		Args:  cobra.ExactArgs(1),
	}
	add.Flags().Bool("org", false, "the license is kept for every app")
	_ = add.Flags().MarkDeprecated("org", "license add keeps the one license every app reads")
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
		t, err := c.Check(raw, true)
		if err != nil {
			return failAt(err, args[0])
		}
		found, err := c.Read()
		if err != nil {
			return err
		}
		what := fmt.Sprintf("keep the license for %s%s, read by every app, in %s%s", t.For, runs(t), t.File, replacing(found, t.File))
		if err := confirm(cc, ask.Action{Type: ask.Settings, What: what, Name: "license"}); err != nil {
			return err
		}
		if _, err := c.Add(cc.Context(), raw, true); err != nil {
			return err
		}
		return answer(cc, addOut, c, list)
	}
	remove := &cobra.Command{
		Use:   "remove",
		Short: "Remove the license every app reads; the trial stays",
		Args:  cobra.NoArgs,
	}
	removeOut := a.Printer(remove)
	ask.Flag(remove)
	remove.RunE = func(cc *cobra.Command, _ []string) error {
		if err := removeOut.Check(); err != nil {
			return err
		}
		shown, err := c.Removing()
		if err != nil {
			return err
		}
		files := make([]string, len(shown))
		for i, r := range shown {
			files[i] = r.File
		}
		what := "remove the license file " + strings.Join(files, " and ") + "; the trial stays"
		if err := confirm(cc, ask.Action{Type: ask.Settings, What: what, Name: "license"}); err != nil {
			return err
		}
		if err := c.Remove(cc.Context(), shown); err != nil {
			return err
		}
		return answer(cc, removeOut, c, list)
	}
	cmd.AddCommand(show, add, remove)
	return cmd
}

// runs is how long t runs, as the question says it: until its last day, or on with none.
func runs(t licenses.Taken) string {
	if t.LastDay.IsZero() {
		return ", which never ends"
	}
	return " until " + t.LastDay.Format("2006-01-02")
}

// replacing names what add replaces at path, so the question says it (G-STACK-13).
func replacing(f licenses.Found, path string) string {
	for _, t := range f.Taken {
		if t.File == path {
			return fmt.Sprintf(", replacing the license for %s%s", t.For, runs(t))
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
				until, left := "–", "–"
				if l.Ends() != "" {
					until, left = l.Ends(), strconv.Itoa(l.DaysLeft)
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", l.Kind, l.For, until, left, l.KeyID)
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
