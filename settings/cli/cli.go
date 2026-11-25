// Package cli is `<app> config list` and `<app> config set <key> <value>`, to see and change an app's config.yaml.
package cli

import (
	"fmt"
	"io"
	"slices"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"go.naywee.com/mankit/v5/helpout"
	"go.naywee.com/mankit/v5/settings"
)

// AddTo is AddToE that never fails (keeps an existing `config list` or `config set`, drops a taken -o); call it after the app defines its flags.
func AddTo(root *cobra.Command, keys []settings.Key, path func() string) {
	_ = add(root, keys, path, false)
}

// AddToE adds `config list` and `config set` under root's config (made when missing), after the app's flags; a clash adds nothing.
func AddToE(root *cobra.Command, keys []settings.Key, path func() string) error {
	return add(root, keys, path, true)
}

// add puts list and set under the thing config, each with its old form for one release (G-STACK-5); strict refuses clashes, else it works around them.
func add(root *cobra.Command, keys []settings.Key, path func() string, strict bool) error {
	verbs := []struct {
		name, old string
		build     func([]settings.Key, func() string, string) *cobra.Command
	}{{"list", "get config", listConfig}, {"set", "set config", setConfig}}
	config := child(root, "config")
	parent := config
	if parent == nil {
		parent = root
	}
	shorthand := "o"
	if inheritsO(parent) {
		if strict {
			return fmt.Errorf("%s config: -o is already another flag", root.Name())
		}
		shorthand = ""
	}
	if config != nil {
		for _, v := range verbs {
			if child(config, v.name) != nil && strict {
				return fmt.Errorf("%s config %s already exists", root.Name(), v.name)
			}
		}
	} else {
		config = &cobra.Command{Use: "config", Short: "The settings in config.yaml"}
		root.AddCommand(config)
	}
	for _, v := range verbs {
		if child(config, v.name) != nil {
			continue // kept as it is
		}
		config.AddCommand(helpout.Old(v.build(keys, path, shorthand), v.old))
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

// listConfig is `config list`; short is the -o shorthand, "" for none.
func listConfig(keys []settings.Key, path func() string, short string) *cobra.Command {
	var format string
	c := &cobra.Command{
		Use:   "list",
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

// setConfig is `config set <key> <value>`; short is the -o shorthand, "" for none.
func setConfig(keys []settings.Key, path func() string, short string) *cobra.Command {
	var format string
	c := &cobra.Command{
		Use:   "set <key> <value>",
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
			k, _ := settings.Find(keys, args[0])
			rows, err := settings.Load(p, []settings.Key{k})
			if err != nil {
				return err
			}
			return write(cmd.OutOrStdout(), format, p, rows)
		},
	}
	c.Flags().StringVarP(&format, "output", short, "table", "output format: table, json or yaml")
	return c
}

// write prints rows as an aligned table under the file's path, or as JSON or YAML.
func write(w io.Writer, format, path string, rows []settings.Row) error {
	switch format {
	case "json", "yaml":
		return helpout.Encode(w, format, rows)
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
