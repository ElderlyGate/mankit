package helpout

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

// The old forms of renamed commands (G-STACK-5): each still runs its full form for one release and says the new name on stderr, so stdout stays the same.
// This file, and the package helptest that checks it, go at mankit's next major, once no app has an old form left.

// oldKey is the annotation a command's old forms are kept in, one a line, each the words after the app's name.
const oldKey = "helpout.old"

// Old marks cmd's old forms: the words that ran it before it was renamed, after the app's name, such as "servers stop" for server stop. It gives back cmd.
func Old(cmd *cobra.Command, words ...string) *cobra.Command {
	if cmd.Annotations == nil {
		cmd.Annotations = map[string]string{}
	}
	all := append(oldOf(cmd), words...)
	cmd.Annotations[oldKey] = strings.Join(all, "\n")
	return cmd
}

// OldForm is one old form and the full form it runs, each as the words after the app's name.
type OldForm struct {
	Words string `json:"words"`
	Now   string `json:"now"`
}

// OldForms are every old form under root, in the order the commands are.
func OldForms(root *cobra.Command) []OldForm {
	var out []OldForm
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		for _, w := range oldOf(c) {
			out = append(out, OldForm{Words: w, Now: words(root, c)})
		}
		for _, sub := range c.Commands() {
			walk(sub)
		}
	}
	walk(root)
	return out
}

func oldOf(c *cobra.Command) []string {
	if s := c.Annotations[oldKey]; s != "" {
		return strings.Split(s, "\n")
	}
	return nil
}

// words is c's path after root's name.
func words(root, c *cobra.Command) string {
	return strings.TrimPrefix(strings.TrimPrefix(c.CommandPath(), root.CommandPath()), " ")
}

// rerun is an old form that ran: Execute runs args, its full form, instead.
type rerun struct {
	old, now string
	args     []string
}

func (r *rerun) Error() string { return r.old + " is now " + r.now }

// addOld makes each old form a hidden command that hands its words, with the rest of the line, to its full form; an old form that is a command of its own is the app's mistake.
func addOld(root *cobra.Command) error {
	for _, o := range OldForms(root) {
		parent := root
		steps := strings.Fields(o.Words)
		for i, step := range steps {
			next := child(parent, step)
			last := i == len(steps)-1
			switch {
			case next != nil && !isOld(next) && last:
				return fmt.Errorf("helpout: the old form %q is a command of its own", o.Words)
			case next == nil:
				next = &cobra.Command{Use: step, Hidden: true, Annotations: map[string]string{oldNodeKey: "1"}}
				// The full form runs its own hooks when it runs; the old words run none.
				next.PersistentPreRunE = func(*cobra.Command, []string) error { return nil }
				parent.AddCommand(next)
			}
			if last {
				o := o
				next.DisableFlagParsing, next.Args = true, cobra.ArbitraryArgs
				next.RunE = func(_ *cobra.Command, args []string) error {
					return &rerun{old: o.Words, now: o.Now, args: append(strings.Fields(o.Now), args...)}
				}
			}
			parent = next
		}
	}
	return nil
}

// oldNodeKey marks a hidden command addOld made.
const oldNodeKey = "helpout.oldnode"

func isOld(c *cobra.Command) bool { return c.Annotations[oldNodeKey] != "" }

func child(c *cobra.Command, name string) *cobra.Command {
	for _, sub := range c.Commands() {
		if sub.Name() == name {
			return sub
		}
	}
	return nil
}
