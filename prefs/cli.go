package prefs

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/tugbayatilla/mankit/ask"
)

// confirmKey starts a key of `set config` that sets a type's style.
const confirmKey = ask.Section + "."

// Options are what AddTo needs from the app.
type Options struct {
	App    string
	Config func() string // the app's config.yaml
	Types  []ask.Type    // the types the app's changes use, in the order they are shown
	Asker  Asker
	Extra  func() []Location // places the app keeps besides its config and data; nil for none
}

// AddTo adds `get locations` and `get confirm`, and makes mankit's `set config` (added first) ask and take `confirm.<type>`.
func AddTo(root *cobra.Command, o Options) error {
	get, set := child(root, "get"), child(root, "set")
	if get == nil || set == nil || child(set, "config") == nil {
		return errors.New("prefs: add mankit's settings verbs first")
	}
	get.AddCommand(locationsCmd(o), confirmCmd(o))
	wrapSetConfig(child(set, "config"), o)
	return nil
}

func locationsCmd(o Options) *cobra.Command {
	var format string
	c := &cobra.Command{
		Use:   "locations",
		Short: "Show where " + o.App + " keeps things: its config and its data",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			var extra []Location
			if o.Extra != nil {
				extra = o.Extra()
			}
			locs, err := Locations(o.App, o.Config(), extra...)
			if err != nil {
				return err
			}
			return printOut(cmd.OutOrStdout(), format, locs, func(tw io.Writer) {
				fmt.Fprintln(tw, "WHAT\tPATH\tNOTE")
				for _, l := range locs {
					fmt.Fprintf(tw, "%s\t%s\t%s\n", l.Label, l.Path, l.Note)
				}
			})
		},
	}
	outputFlag(c, &format)
	return c
}

func confirmCmd(o Options) *cobra.Command {
	var format string
	c := &cobra.Command{
		Use:   "confirm",
		Short: "Show how each type of change asks, and whether the style is " + o.App + "'s or the default",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			path := o.Config()
			rows, err := Rows(path, o.Types)
			if err != nil {
				return err
			}
			return printOut(cmd.OutOrStdout(), format, rows, func(tw io.Writer) {
				fmt.Fprintf(tw, "file: %s\n", path)
				fmt.Fprintln(tw, "TYPE\tSTYLE\tSOURCE")
				for _, r := range rows {
					fmt.Fprintf(tw, "%s\t%s\t%s\n", r.Type, r.Style, r.Source)
				}
			})
		},
	}
	outputFlag(c, &format)
	return c
}

// wrapSetConfig makes set ask before it writes, and handle confirm.<type> itself in the app's file.
func wrapSetConfig(set *cobra.Command, o Options) {
	if set.Flag("yes") == nil {
		set.Flags().BoolP("yes", "y", false, "make the change without asking")
	}
	set.Use = "config <key> <value>"
	set.Long = `Change a key of config.yaml; "" puts it back to its default.
confirm.<type> <style> sets how a type of change asks: name, yes-no or never;
default removes it, so the type asks in its default way again.
Types: ` + typeNames(o.Types) + `.`
	write := set.RunE
	set.RunE = func(cmd *cobra.Command, args []string) error {
		key, value := args[0], args[1]
		t, isConfirm := strings.CutPrefix(key, confirmKey)
		if isConfirm {
			if err := checkConfirm(o.Types, t, value); err != nil {
				return err
			}
		}
		what := fmt.Sprintf("set %s to %s in %s", key, value, o.Config())
		if err := o.Asker.Ask(cmd, ask.Action{Type: ask.Settings, What: what}); err != nil {
			return err
		}
		if !isConfirm {
			return write(cmd, args)
		}
		style := ask.Style(value)
		if value == UseDefault {
			style = ""
		}
		if err := ask.Set(o.Config(), ask.Type(t), style); err != nil {
			return err
		}
		rows, err := Rows(o.Config(), []ask.Type{ask.Type(t)})
		if err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "%s: %s (%s)\n", key, rows[0].Style, rows[0].Source)
		return nil
	}
}

// checkConfirm refuses a type the app does not use or a style that is not one.
func checkConfirm(types []ask.Type, t, value string) error {
	known := false
	for _, k := range types {
		known = known || string(k) == t
	}
	if !known {
		return fmt.Errorf("unknown type %q: use %s", t, typeNames(types))
	}
	if value == UseDefault {
		return nil
	}
	if _, err := ask.ParseStyle(value); err != nil {
		return fmt.Errorf("%w, or %s", err, UseDefault)
	}
	return nil
}

func typeNames(types []ask.Type) string {
	names := make([]string, len(types))
	for i, t := range types {
		names[i] = string(t)
	}
	return strings.Join(names, ", ")
}

func outputFlag(c *cobra.Command, format *string) {
	c.Flags().StringVarP(format, "output", "o", "table", "output format: table, json or yaml")
}

// printOut writes v as JSON or YAML, or as the table rows draws.
func printOut(w io.Writer, format string, v any, rows func(io.Writer)) error {
	switch format {
	case "json":
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(v)
	case "yaml":
		enc := yaml.NewEncoder(w)
		enc.SetIndent(2)
		if err := enc.Encode(v); err != nil {
			return err
		}
		return enc.Close()
	case "table":
		tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		rows(tw)
		return tw.Flush()
	}
	return fmt.Errorf("unknown output %q (want table, json or yaml)", format)
}

// child is c's command named name, nil when it has none.
func child(c *cobra.Command, name string) *cobra.Command {
	for _, sub := range c.Commands() {
		if sub.Name() == name {
			return sub
		}
	}
	return nil
}
