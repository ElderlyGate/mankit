package queue

import (
	"errors"
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"go.naywee.com/mankit/v5/ask"
	"go.naywee.com/mankit/v5/helpout"
	"go.naywee.com/mankit/v5/problem"
	"go.naywee.com/mankit/v5/serve"
	"go.naywee.com/mankit/v5/settings"
)

// SettingsKind is the kind of the answer <app> queue show, set and unset give (contracts mankit/v2 queue-settings).
const SettingsKind = "QueueSettings"

// Setting is one of the queue's two settings as the app takes it now; contracts mankit/v2 queue-settings describes each field.
type Setting struct {
	Value   string `json:"value"`
	Default string `json:"default"`
	Set     bool   `json:"set"`
}

// SettingsData is the data of <app> queue show: how long a change waits for its turn, and how long it may hold it.
type SettingsData struct {
	Wait  Setting `json:"wait"`
	Limit Setting `json:"limit"`
}

// keys are the queue's rows in an app's config.yaml.
func keys() []settings.Key { return []settings.Key{WaitKey, LimitKey} }

// Waits are queue.wait and queue.limit as the app's config.yaml at path says them, in the form d.String() gives, empty where it says nothing; a hand-edited value the queue would refuse is reported, never passed on.
func Waits(path string) (wait, limit string, err error) {
	data, err := settingsAt(path)
	if err != nil {
		return "", "", err
	}
	if data.Wait.Set {
		wait = data.Wait.Value
	}
	if data.Limit.Set {
		limit = data.Limit.Value
	}
	return wait, limit, nil
}

// Command is the thing queue: `show` shows queue.wait and queue.limit, `set <wait|limit> <duration>` sets one and `unset <wait|limit>` puts it back to its default, in the app's config.yaml at path after confirm asks.
// Each has its old form under config queue for one release (G-STACK-5). An app with a queue of its own adds its verbs, such as list, to the thing.
// It lives here, beside the rule Settings checks, and prints through serve's Printer, so every app shows and answers it the same way.
func Command(a serve.App, path func() string, confirm func(*cobra.Command, ask.Action) error) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "queue",
		Short: "How long a change waits for its turn and may hold it",
		Long: "Every change to a served thing waits its turn in that thing's change queue.\n" +
			"queue.wait is how long it waits before it gives up, and queue.limit how long it\n" +
			"may hold its turn; each is at least a second. `queue set wait 45s` and\n" +
			"`queue set limit 3m` set one, `queue unset wait` puts it back.",
	}
	show := &cobra.Command{
		Use:   "show",
		Short: "Show how long a change waits for its turn and may hold it",
		Args:  cobra.NoArgs,
	}
	out := a.Printer(show)
	show.RunE = func(c *cobra.Command, args []string) error {
		if err := out.Check(); err != nil {
			return err
		}
		data, err := settingsAt(path())
		if err != nil {
			return err
		}
		return out.Print(c.OutOrStdout(), "", SettingsKind, data, func(w io.Writer) {
			tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
			fmt.Fprintln(tw, "SETTING\tVALUE\tDEFAULT\tSET")
			for _, r := range []struct {
				name string
				s    Setting
			}{{WaitKey.Name, data.Wait}, {LimitKey.Name, data.Limit}} {
				fmt.Fprintf(tw, "%s\t%s\t%s\t%t\n", r.name, r.s.Value, r.s.Default, r.s.Set)
			}
			_ = tw.Flush()
		})
	}
	set := setCommand(a, path, confirm, false)
	unset := setCommand(a, path, confirm, true)
	helpout.Old(show, "config queue")
	helpout.OldArgs(set, "config queue wait", "wait")
	helpout.OldArgs(set, "config queue limit", "limit")
	helpout.OldArgs(unset, "config queue wait default", "wait")
	helpout.OldArgs(unset, "config queue limit default", "limit")
	cmd.AddCommand(show, set, unset)
	return cmd
}

// rows are the queue's two rows by the word set and unset take.
var rows = map[string]settings.Key{"wait": WaitKey, "limit": LimitKey}

// setCommand is `set <wait|limit> <duration>`, checked by Settings, so a value the queue would refuse is never written; unset is `unset <wait|limit>`, which puts the default back.
func setCommand(a serve.App, path func() string, confirm func(*cobra.Command, ask.Action) error, unset bool) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "set <wait|limit> <duration>",
		Short: "Set queue.wait or queue.limit: " + WaitKey.Help + ", or " + LimitKey.Help,
		Args:  cobra.ExactArgs(2),
	}
	if unset {
		cmd.Use, cmd.Short, cmd.Args = "unset <wait|limit>", "Put queue.wait or queue.limit back to its default", cobra.ExactArgs(1)
	}
	out := a.Printer(cmd)
	ask.Flag(cmd)
	cmd.RunE = func(c *cobra.Command, args []string) error {
		if err := out.Check(); err != nil {
			return err
		}
		key, ok := rows[args[0]]
		if !ok {
			return problem.Fail(problem.TypeRefused, "unknown_key", fmt.Sprintf("the queue has no setting %q; it has wait and limit", args[0]))
		}
		// Written as d.String() gives it, so what the queue takes is what the contract's pattern takes.
		value, said := "", "its default"
		if !unset {
			if args[1] == "default" {
				return problem.Fail(problem.TypeRefused, "wrong_type", fmt.Sprintf("default is no duration; `%s queue unset %s` puts the default back", a.ID, args[0]))
			}
			d, err := Duration(key, args[1])
			if err != nil {
				return err
			}
			value, said = d.String(), args[1]
		}
		what := fmt.Sprintf("set %s to %s in %s's config", key.Name, said, a.ID)
		if err := confirm(c, ask.Action{Type: ask.Settings, Name: key.Name, What: what}); err != nil {
			return err
		}
		if err := settings.Set(path(), keys(), key.Name, value); err != nil {
			return problem.Fail(problem.TypeFailed, serve.ReasonConfigUnreadable, fmt.Sprintf("%s could not be written: %v", key.Name, err))
		}
		data, err := settingsAt(path())
		if err != nil {
			return err
		}
		now := data.Limit
		if key.Name == WaitKey.Name {
			now = data.Wait
		}
		return out.Print(c.OutOrStdout(), "", SettingsKind, data, func(w io.Writer) {
			fmt.Fprintf(w, "%s is %s\n", key.Name, now.Value)
		})
	}
	return cmd
}

// settingsAt is the queue's two settings as the app's config.yaml at path has them, each in the form d.String() gives; a value the queue would refuse is reported as the config that cannot be read.
func settingsAt(path string) (SettingsData, error) {
	rows, err := settings.Load(path, keys())
	if err != nil {
		return SettingsData{}, problem.Fail(problem.TypeFailed, serve.ReasonConfigUnreadable, err.Error())
	}
	var data SettingsData
	for _, r := range rows {
		key := WaitKey
		if r.Key == LimitKey.Name {
			key = LimitKey
		}
		d, err := Duration(key, r.Value)
		var p *problem.Problem
		if errors.As(err, &p) {
			return SettingsData{}, problem.Fail(problem.TypeFailed, serve.ReasonConfigUnreadable, p.Detail)
		}
		s := Setting{Value: d.String(), Default: r.Default, Set: r.Set}
		switch r.Key {
		case WaitKey.Name:
			data.Wait = s
		case LimitKey.Name:
			data.Limit = s
		}
	}
	return data, nil
}
