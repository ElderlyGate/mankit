// Package cli is the thing license every app mounts at its root: `license show`, `license list`, `license add <file>`, `license use <id>` and `license remove <id>`, above licenses, so licenses imports neither serve nor ask.
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

// Command is the thing license of app a, printed through serve's Printer so every app answers alike: c reads its licenses, list holds its features closed by c's Closer, and add, use and remove ask with confirm.
func Command(a serve.App, c *licenses.Checker, list *features.List, confirm func(*cobra.Command, ask.Action) error) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "license",
		Short: "The licenses and the trial that open this app's enterprise features",
		Long: "A license names whom it is for and the enterprise features it opens. Org licenses are shared by every app; an app keeps its own too.\n" +
			"One license is active in each app: the one chosen here, else the newest. The trial is a rule built into the app.\n" +
			"`license show` (or `license list`) lists them, marks the active one and shows every feature with where it is unlocked from;\n" +
			"`license add <file> [--org]` keeps a license and makes it active here, `license use <id>` makes one active here, `license remove <id>` takes one away.",
	}
	view := func(use, short string) *cobra.Command {
		v := &cobra.Command{Use: use, Short: short, Args: cobra.NoArgs}
		out := a.Printer(v)
		v.RunE = func(cc *cobra.Command, _ []string) error {
			if err := out.Check(); err != nil {
				return err
			}
			return answer(cc, out, c, list)
		}
		return v
	}
	add := &cobra.Command{
		Use:   "add <file>",
		Short: "Check a license file, keep it and make it the active license of this app",
		Args:  cobra.ExactArgs(1),
	}
	add.Flags().Bool("org", false, "keep it in the org's licenses, shared by every app it names")
	addOut := a.Printer(add)
	ask.Flag(add)
	add.RunE = func(cc *cobra.Command, args []string) error {
		if err := addOut.Check(); err != nil {
			return err
		}
		org, _ := cc.Flags().GetBool("org")
		raw, err := os.ReadFile(args[0])
		if err != nil {
			return problem.FailAt(problem.TypeRefused, licenses.ReasonUnreadable, args[0], fmt.Sprintf("%s cannot be read: %v", args[0], err))
		}
		action, err := adding(c, raw, org)
		if err != nil {
			return failAt(err, args[0])
		}
		if err := confirm(cc, action); err != nil {
			return err
		}
		if _, err := c.Add(cc.Context(), raw, org); err != nil {
			return err
		}
		return answer(cc, addOut, c, list)
	}
	use := &cobra.Command{
		Use:   "use <id>",
		Short: "Make the license id the active license of this app",
		Args:  cobra.ExactArgs(1),
	}
	useOut := a.Printer(use)
	ask.Flag(use)
	use.RunE = func(cc *cobra.Command, args []string) error {
		if err := useOut.Check(); err != nil {
			return err
		}
		action, err := using(c, args[0])
		if err != nil {
			return err
		}
		if err := confirm(cc, action); err != nil {
			return err
		}
		if _, err := c.Use(cc.Context(), args[0]); err != nil {
			return err
		}
		return answer(cc, useOut, c, list)
	}
	remove := &cobra.Command{
		Use:   "remove <id>",
		Short: "Remove the license id; the trial stays",
		Args:  cobra.ExactArgs(1),
	}
	removeOut := a.Printer(remove)
	ask.Flag(remove)
	remove.RunE = func(cc *cobra.Command, args []string) error {
		if err := removeOut.Check(); err != nil {
			return err
		}
		shown, action, err := removing(c, args[0])
		if err != nil {
			return err
		}
		if err := confirm(cc, action); err != nil {
			return err
		}
		if err := c.Remove(cc.Context(), shown); err != nil {
			return err
		}
		return answer(cc, removeOut, c, list)
	}
	cmd.AddCommand(view("show", "Show the licenses, the active one, the trial and where each feature is unlocked from"),
		view("list", "List the licenses, the active one, the trial and where each feature is unlocked from, as show does"), add, use, remove)
	return cmd
}

// adding is the question license add asks before it keeps the license raw holds, from the CLI and over HTTP alike, or the Problem why raw cannot be kept.
func adding(c *licenses.Checker, raw []byte, org bool) (ask.Action, error) {
	t, err := c.Check(raw, org)
	if err != nil {
		return ask.Action{}, err
	}
	found, err := c.Read()
	if err != nil {
		return ask.Action{}, err
	}
	where := "as this app's own"
	if org {
		where = "shared by every app it names"
	}
	what := fmt.Sprintf("keep the license %s for %s%s%s in %s, %s%s, and make it the active license of this app", t.ID, t.For, runs(t), idle(t), t.File, where, replacing(found, t.File))
	return ask.Action{Type: ask.Settings, What: what, Name: "license"}, nil
}

// using is the question license use asks before it makes the license id active, from the CLI and over HTTP alike, or the Problem why it cannot.
func using(c *licenses.Checker, id string) (ask.Action, error) {
	found, err := c.Read()
	if err != nil {
		return ask.Action{}, err
	}
	t, err := found.Find(id)
	if err != nil {
		return ask.Action{}, err
	}
	what := fmt.Sprintf("make the license %s for %s%s%s the active license of this app", t.ID, t.For, runs(t), idle(t))
	if a := found.Active(); a != nil && a.ID != id {
		what += ", in place of " + a.ID
	}
	return ask.Action{Type: ask.Settings, What: what, Name: "license"}, nil
}

// removing is what license remove takes away for the license id and the question it asks first, naming each file's place, from the CLI and over HTTP alike; with nothing to remove it is a Problem not_found.
func removing(c *licenses.Checker, id string) ([]licenses.Removal, ask.Action, error) {
	shown, err := c.Removing(id)
	if err != nil {
		return nil, ask.Action{}, err
	}
	files := make([]string, len(shown))
	for i, r := range shown {
		files[i] = "the file " + r.File + " of this app"
		if r.Place == licenses.PlaceOrg {
			files[i] = "the file " + r.File + " in the org's licenses, shared by every app it names"
		}
	}
	what := fmt.Sprintf("remove the license %s, %s; the trial stays", id, strings.Join(files, " and "))
	return shown, ask.Action{Type: ask.Settings, What: what, Name: "license"}, nil
}

// runs is how long t runs, as the question says it: until its last day, or on with none.
func runs(t licenses.Taken) string {
	if t.LastDay.IsZero() {
		return ", which never ends"
	}
	return " until " + t.LastDay.Format("2006-01-02")
}

// idle says why t opens nothing here, as the question says it, the clause closed: it has ended, it has not started, or it does not run in this build; empty while it runs.
func idle(t licenses.Taken) string {
	switch {
	case t.Ended:
		return ", which has ended,"
	case t.Early:
		return ", which starts on " + t.Issued.Format("2006-01-02") + ","
	case !t.Running():
		return ", which opens nothing in this build,"
	}
	return ""
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
			fmt.Fprintln(tw, "ACTIVE\tID\tPLACE\tFOR\tISSUED\tUNTIL\tDAYS LEFT\tKEY")
			for _, l := range s.Licenses {
				active, until, left := "", "–", "–"
				if l.Active {
					active = "*"
				}
				if l.Ends() != "" {
					until, left = l.Ends(), strconv.Itoa(l.DaysLeft)
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n", active, l.ID, l.Place, l.For, l.Issued, until, left, l.KeyID)
			}
			fmt.Fprintln(tw)
		}
		if s.Trial != nil {
			fmt.Fprintf(tw, "TRIAL\tUNTIL\tDAYS LEFT\tENDED\n\t%s\t%d\t%t\n\n", s.Trial.Until, s.Trial.DaysLeft, s.Trial.Ended)
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
