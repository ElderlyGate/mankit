// Package cli is `<app> get config` and `<app> set config <key> <value>`, to see and change an app's config.yaml.
package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/tugbayatilla/mankit/settings"
)

// AddTo is AddToE that never fails (keeps an existing `config`, drops a taken -o); call it after the app defines its flags.
func AddTo(root *cobra.Command, keys []settings.Key, path func() string) {
	_ = add(root, keys, path, false)
}

// AddToE adds `get config` and `set config` under root's get and set (made when missing), after the app's flags; a clash adds nothing.
func AddToE(root *cobra.Command, keys []settings.Key, path func() string) error {
	return add(root, keys, path, true)
}

// add puts the config commands under get and set; strict refuses clashes, else it works around them.
func add(root *cobra.Command, keys []settings.Key, path func() string, strict bool) error {
	verbs := []struct {
		name, short string
		build       func([]settings.Key, func() string, string) *cobra.Command
	}{{"get", "Show a setting", getConfig}, {"set", "Change a setting", setConfig}}
	shorthands := make([]string, len(verbs))
	for i, v := range verbs {
		parent := child(root, v.name)
		if parent != nil && child(parent, "config") != nil {
			if strict {
				return fmt.Errorf("%s %s config already exists", root.Name(), v.name)
			}
			shorthands[i] = "-" // kept as it is
			continue
		}
		if parent == nil {
			parent = root
		}
		shorthands[i] = "o"
		if inheritsO(parent) {
			if strict {
				return fmt.Errorf("%s %s: -o is already another flag", root.Name(), v.name)
			}
			shorthands[i] = ""
		}
	}
	for i, v := range verbs {
		if shorthands[i] == "-" {
			continue
		}
		parent := child(root, v.name)
		if parent == nil {
			parent = &cobra.Command{Use: v.name, Short: v.short, Args: cobra.NoArgs}
			root.AddCommand(parent)
		}
		parent.AddCommand(v.build(keys, path, shorthands[i]))
	}
	return nil
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

// inheritsO reports whether c or a parent has a persistent -o that is not --output.
func inheritsO(c *cobra.Command) bool {
	for ; c != nil; c = c.Parent() {
		if f := c.PersistentFlags().ShorthandLookup("o"); f != nil && f.Name != "output" {
			return true
		}
	}
	return false
}

// getConfig is `get config`; short is the -o shorthand, "" for none.
func getConfig(keys []settings.Key, path func() string, short string) *cobra.Command {
	var format string
	c := &cobra.Command{
		Use:   "config",
		Short: "Show every key of config.yaml, its value and whether it is set or the default",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			p := path()
			rows, err := settings.Load(p, keys)
			if err != nil {
				return err
			}
			return write(cmd.OutOrStdout(), format, p, rows)
		},
	}
	c.Flags().StringVarP(&format, "output", short, "table", "output format: table, json or yaml")
	return c
}

// setConfig is `set config <key> <value>`; short is the -o shorthand, "" for none.
func setConfig(keys []settings.Key, path func() string, short string) *cobra.Command {
	var format string
	c := &cobra.Command{
		Use:   "config <key> <value>",
		Short: `Change a key of config.yaml; "" puts it back to its default`,
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !slices.Contains(formats, format) {
				return errFormat(format)
			}
			p := path()
			if err := settings.Set(p, keys, args[0], args[1]); err != nil {
				return err
			}
			rows, err := settings.Load(p, keys)
			if err != nil {
				return err
			}
			for _, r := range rows {
				if r.Key == args[0] {
					return write(cmd.OutOrStdout(), format, p, []settings.Row{r})
				}
			}
			return nil
		},
	}
	c.Flags().StringVarP(&format, "output", short, "table", "output format: table, json or yaml")
	return c
}

// write prints rows as an aligned table under the file's path, or as JSON or YAML.
func write(w io.Writer, format, path string, rows []settings.Row) error {
	switch format {
	case "json":
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(rows)
	case "yaml":
		enc := yaml.NewEncoder(w)
		enc.SetIndent(2)
		if err := enc.Encode(rows); err != nil {
			return err
		}
		return enc.Close()
	case "table":
		fmt.Fprintf(w, "file: %s\n", path)
		tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "KEY\tVALUE\tSOURCE\tHELP")
		for _, r := range rows {
			source := "default"
			if r.Set {
				source = "set"
			}
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", r.Key, r.Value, source, r.Help)
		}
		return tw.Flush()
	}
	return errFormat(format)
}

// formats are the -o values.
var formats = []string{"table", "json", "yaml"}

// errFormat refuses an unknown -o value.
func errFormat(format string) error {
	return fmt.Errorf("unknown output %q (want table, json or yaml)", format)
}
