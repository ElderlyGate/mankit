// Package cli is `<app> commands`: where the commands file is, how to get
// the starter one back, and whether it loads.
package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/tugbayatilla/mankit/usercmd"
)

// Command builds `<app> commands`. starter is the file the app ships; taken
// is the app's own keys per view, to report the ones a user command loses.
func Command(app string, starter []byte, spec usercmd.Spec, taken func() map[string][]string) *cobra.Command {
	path := usercmd.Path(app)
	root := &cobra.Command{
		Use:   "commands",
		Short: "Your own palette commands: where the file is, recreate it, check it",
		Long: fmt.Sprintf(`The dashboard's palette also lists the commands in
%s.
They are read at start; a key the dashboard already uses is refused and
the command stays in the palette.`, path),
		Args: cobra.NoArgs,
	}
	root.AddCommand(&cobra.Command{
		Use:   "path",
		Short: "Print where the commands file is",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			fmt.Fprintln(cmd.OutOrStdout(), path)
			if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
				fmt.Fprintf(cmd.ErrOrStderr(), "it does not exist; `%s commands init` creates it\n", app)
			}
			return nil
		},
	})
	var force bool
	initCmd := &cobra.Command{
		Use:   "init",
		Short: "Write the starter commands file when it is missing (--force replaces yours, kept as .bak)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			wrote, backup, err := usercmd.Init(path, starter, force)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			switch {
			case !wrote:
				fmt.Fprintf(out, "kept your %s (--force replaces it with the starter one)\n", path)
			case backup != "":
				fmt.Fprintf(out, "wrote the starter %s; yours is in %s\n", path, backup)
			default:
				fmt.Fprintf(out, "wrote the starter %s\n", path)
			}
			return nil
		},
	}
	initCmd.Flags().BoolVar(&force, "force", false, "replace an existing file, keeping it as commands.yaml.bak")
	root.AddCommand(initCmd)
	root.AddCommand(&cobra.Command{
		Use:   "check",
		Short: "Check the commands file and list what it adds",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return check(cmd.OutOrStdout(), app, path, spec, taken())
		},
	})
	root.AddCommand(&cobra.Command{
		Use:   "edit",
		Short: "Open the commands file in $VISUAL or $EDITOR, writing the starter one first if it is missing",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if _, _, err := usercmd.Init(path, starter, false); err != nil {
				return err
			}
			ed := usercmd.Editor(path)
			ed.Stdin, ed.Stdout, ed.Stderr = os.Stdin, os.Stdout, os.Stderr
			if err := ed.Run(); err != nil {
				return err
			}
			return check(cmd.OutOrStdout(), app, path, spec, taken())
		},
	})
	return root
}

func check(w io.Writer, app, path string, spec usercmd.Spec, taken map[string][]string) error {
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("%s does not exist; `%s commands init` creates it", path, app)
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
