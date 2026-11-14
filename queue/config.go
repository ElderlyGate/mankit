package queue

import (
	"errors"
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/tugbayatilla/mankit/v3/ask"
	"github.com/tugbayatilla/mankit/v3/serve"
	"github.com/tugbayatilla/mankit/v3/settings"
)

// SettingsKind is the kind of the answer <app> config queue gives (contracts mankit/v1 queue-settings).
const SettingsKind = "QueueSettings"

// Setting is one of the queue's two settings as the app takes it now; contracts mankit/v1 queue-settings describes each field.
type Setting struct {
	Value   string `json:"value"`
	Default string `json:"default"`
	Set     bool   `json:"set"`
}

// SettingsData is the data of <app> config queue: how long a change waits for its turn, and how long it may hold it.
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

// ConfigCommand is `config queue`, which every app with a change queue mounts under its own config, as it does config confirm:
// it shows queue.wait and queue.limit, and `wait <duration|default>` and `limit <duration|default>` set one in the app's config.yaml at path after confirm asks.
// It lives here, beside the rule Settings checks, and prints through serve's Printer, so every app shows and answers it the same way.
func ConfigCommand(a serve.App, path func() string, confirm func(*cobra.Command, ask.Action) error) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "queue",
		Short: "Show how long a change waits for its turn and may hold it",
		Long: "Every change to a served thing waits its turn in that thing's change queue.\n" +
			"queue.wait is how long it waits before it gives up, and queue.limit how long it\n" +
			"may hold its turn; each is at least a second. `config queue wait 45s` and\n" +
			"`config queue limit 3m` set one, `default` puts it back.",
		Args: cobra.NoArgs,
	}
	out := a.Printer(cmd)
	cmd.RunE = func(c *cobra.Command, args []string) error {
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
	cmd.AddCommand(setCommand(a, WaitKey, "wait", path, confirm), setCommand(a, LimitKey, "limit", path, confirm))
	return cmd
}

// setCommand sets one row, checked by Settings, so a value the queue would refuse is never written.
func setCommand(a serve.App, key settings.Key, word string, path func() string, confirm func(*cobra.Command, ask.Action) error) *cobra.Command {
	cmd := &cobra.Command{
		Use:   word + " <duration|default>",
		Short: "Set " + key.Name + ": " + key.Help,
		Args:  cobra.ExactArgs(1),
	}
	out := a.Printer(cmd)
	ask.Flag(cmd)
	cmd.RunE = func(c *cobra.Command, args []string) error {
		if err := out.Check(); err != nil {
			return err
		}
		// Written as d.String() gives it, so what the queue takes is what the contract's pattern takes.
		value := ""
		if args[0] != "default" {
			d, err := Duration(key, args[0])
			if err != nil {
				return err
			}
			value = d.String()
		}
		what := fmt.Sprintf("set %s to %s in %s's config", key.Name, args[0], a.ID)
		if err := confirm(c, ask.Action{Type: ask.Settings, Name: key.Name, What: what}); err != nil {
			return err
		}
		if err := settings.Set(path(), keys(), key.Name, value); err != nil {
			return serve.Fail(serve.TypeFailed, serve.ReasonConfigUnreadable, fmt.Sprintf("%s could not be written: %v", key.Name, err))
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
		return SettingsData{}, serve.Fail(serve.TypeFailed, serve.ReasonConfigUnreadable, err.Error())
	}
	var data SettingsData
	for _, r := range rows {
		key := WaitKey
		if r.Key == LimitKey.Name {
			key = LimitKey
		}
		d, err := Duration(key, r.Value)
		var p *serve.Problem
		if errors.As(err, &p) {
			return SettingsData{}, serve.Fail(serve.TypeFailed, serve.ReasonConfigUnreadable, p.Detail)
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
