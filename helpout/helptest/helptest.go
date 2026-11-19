// Package helptest checks an app's old command forms (G-STACK-5): each names a command, none is listed in help, and each gives the same standard output as its full form.
// It goes with helpout's old forms at mankit's next major.
package helptest

import (
	"bytes"
	"context"
	"strings"

	"github.com/spf13/cobra"

	"github.com/tugbayatilla/mankit/v4/helpout"
)

// T is what Check reports to: a *testing.T.
type T interface {
	Helper()
	Errorf(format string, args ...any)
}

// Check runs every check on the app newRoot builds; it builds a fresh root for each run, as each run of the app starts afresh.
// It runs each form with --help only, so no command does anything.
func Check(t T, newRoot func() *cobra.Command) {
	t.Helper()
	root := newRoot()
	for _, o := range helpout.OldForms(root) {
		full, rest, err := root.Find(strings.Fields(o.Now))
		if err != nil || len(rest) > 0 || full == root {
			t.Errorf("the old form %q runs %q, which is no command", o.Words, o.Now)
			continue
		}
		oldOut, oldErr, oldCode := run(newRoot(), append(strings.Fields(o.Words), "--help")...)
		fullOut, _, fullCode := run(newRoot(), append(strings.Fields(o.Now), "--help")...)
		if oldOut != fullOut || oldCode != fullCode {
			t.Errorf("%q --help gave exit %d and\n%s\nbut %q --help gave exit %d and\n%s", o.Words, oldCode, oldOut, o.Now, fullCode, fullOut)
		}
		if !strings.Contains(oldErr, o.Words+" is now "+o.Now) {
			t.Errorf("%q does not say its new name %q on stderr: %q", o.Words, o.Now, oldErr)
		}
	}
	listed(t, newRoot, root, nil)
}

// listed checks that the help of c, and of every thing under it, names no old form.
func listed(t T, newRoot func() *cobra.Command, root *cobra.Command, path []string) {
	t.Helper()
	out, _, _ := run(newRoot(), append(append([]string{}, path...), "--help")...)
	for _, o := range helpout.OldForms(root) {
		steps := strings.Fields(o.Words)
		if len(steps) <= len(path) || strings.Join(steps[:len(path)], " ") != strings.Join(path, " ") {
			continue
		}
		if word := steps[len(path)]; strings.Contains(out, "\n  "+word+" ") && !visible(root, append(path, word)) {
			t.Errorf("the help of %q lists the old form %q:\n%s", strings.Join(append([]string{root.Name()}, path...), " "), o.Words, out)
		}
	}
	c, _, _ := root.Find(path)
	for _, sub := range c.Commands() {
		if sub.HasAvailableSubCommands() {
			listed(t, newRoot, root, append(append([]string{}, path...), sub.Name()))
		}
	}
}

// visible says whether path is a command people see, before any old form is added.
func visible(root *cobra.Command, path []string) bool {
	c, rest, err := root.Find(path)
	return err == nil && len(rest) == 0 && c != root && c.IsAvailableCommand()
}

func run(root *cobra.Command, args ...string) (stdout, stderr string, code int) {
	var out, errOut bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errOut)
	root.SetArgs(args)
	code, err := helpout.Execute(context.Background(), root)
	if err != nil {
		errOut.WriteString(err.Error())
	}
	return out.String(), errOut.String(), code
}
