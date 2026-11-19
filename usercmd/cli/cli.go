// Package cli is `<app> command`: where the config file with the commands
// is, how to get the starter one back, and whether it loads.
package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/tugbayatilla/mankit/v4/helpout"
	"github.com/tugbayatilla/mankit/v4/usercmd"
)

// Command builds the thing `<app> command`, each verb with its old form under commands for one release (G-STACK-5). starter is the file the app ships; taken
// is the app's own keys per view, to report the ones a user command loses.
func Command(app string, starter []byte, spec usercmd.Spec, taken func() map[string][]string) *cobra.Command {
	return CommandAt(app, func() string { return usercmd.Path(app) }, starter, spec, taken)
}

// CommandAt is Command for the config.yaml that path names, asked each time a command runs.
func CommandAt(app string, path func() string, starter []byte, spec usercmd.Spec, taken func() map[string][]string) *cobra.Command {
	root := &cobra.Command{
		Use:   "command",
		Short: "Your own palette commands: where the file is, create it, check it",
		Long: fmt.Sprintf(`The dashboard's palette also lists the commands in
%s,
the one file of %s you edit (it also holds theme: and background:).
They are read at start; a key the dashboard already uses is refused and
the command stays in the palette. An old commands.yaml is moved in.`, path(), app),
		Args: cobra.NoArgs,
		PersistentPreRun: func(cmd *cobra.Command, _ []string) {
			if did, err := usercmd.Migrate(path()); err == nil && did != "" {
				fmt.Fprintln(cmd.ErrOrStderr(), did)
			}
		},
	}
	helpout.Old(root, "commands")
	root.AddCommand(helpout.Old(&cobra.Command{
		Use:   "path",
		Short: "Print where the config file with the commands is",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			fmt.Fprintln(cmd.OutOrStdout(), path())
			if _, err := os.Stat(path()); errors.Is(err, os.ErrNotExist) {
				fmt.Fprintf(cmd.ErrOrStderr(), "it does not exist; `%s command create` creates it\n", app)
			}
			return nil
		},
	}, "commands path"))
	var force bool
	create := &cobra.Command{
		Use:   "create",
		Short: "Write the starter config file when it is missing (--force replaces yours, kept as .bak)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			p := path()
			wrote, backup, err := usercmd.Init(p, starter, force)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			switch {
			case !wrote:
				fmt.Fprintf(out, "kept your %s (--force replaces it with the starter one)\n", p)
			case backup != "":
				fmt.Fprintf(out, "wrote the starter %s; yours is in %s\n", p, backup)
			default:
				fmt.Fprintf(out, "wrote the starter %s\n", p)
			}
			return nil
		},
	}
	create.Flags().BoolVar(&force, "force", false, "replace an existing file, keeping it as config.yaml.bak")
	root.AddCommand(helpout.Old(create, "commands init"))
	root.AddCommand(helpout.Old(&cobra.Command{
		Use:   "check",
		Short: "Check the commands in the config file and list what they add",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return check(cmd.OutOrStdout(), app, path(), spec, taken())
		},
	}, "commands check"))
	root.AddCommand(helpout.Old(&cobra.Command{
		Use:   "edit",
		Short: "Open the config file in $VISUAL or $EDITOR, writing the starter one first if it is missing",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			p := path()
			if _, _, err := usercmd.Init(p, starter, false); err != nil {
				return err
			}
			ed := usercmd.Editor(p)
			ed.Stdin, ed.Stdout, ed.Stderr = os.Stdin, os.Stdout, os.Stderr
			if err := ed.Run(); err != nil {
				return err
			}
			return check(cmd.OutOrStdout(), app, p, spec, taken())
		},
	}, "commands edit"))
	return root
}

func check(w io.Writer, app, path string, spec usercmd.Spec, taken map[string][]string) error {
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("%s does not exist; `%s command create` creates it", path, app)
	}
	cmds, err := usercmd.Load(path, spec)
	if err != nil {
		return err
	}
	cmds, clashes := usercmd.FreeKeys(cmds, taken)
	fmt.Fprintf(w, "%s: %d commands\n", path, len(cmds))
	for _, c := range cmds {
		key := c.Key
		if key == "" {
			key = "-"
		}
		var flags []string
		for _, f := range []struct {
			on   bool
			name string
		}{{c.Ask != "", "asks"}, {c.Confirm, "confirms"}, {c.Terminal, "terminal"}} {
			if f.on {
				flags = append(flags, f.name)
			}
		}
		fmt.Fprintf(w, "  %-8s %-10s %-22s %s", key, c.View, c.ID, c.Label)
		if len(flags) > 0 {
			fmt.Fprintf(w, "  (%s)", strings.Join(flags, ", "))
		}
		fmt.Fprintln(w)
	}
	if len(clashes) > 0 {
		fmt.Fprintln(w, usercmd.ClashMessage(clashes))
	}
	return nil
}
