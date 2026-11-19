package helptest_test

import (
	"fmt"
	"testing"

	"github.com/spf13/cobra"

	"github.com/tugbayatilla/mankit/v4/helpout"
	"github.com/tugbayatilla/mankit/v4/helpout/helptest"
)

func app() *cobra.Command {
	root := &cobra.Command{Use: "demo"}
	server := &cobra.Command{Use: "server", Short: "The servers"}
	server.AddCommand(
		helpout.Old(&cobra.Command{Use: "list", Short: "List the servers", Run: func(*cobra.Command, []string) {}}, "servers"),
		helpout.Old(&cobra.Command{Use: "stop", Short: "Stop a server", Run: func(*cobra.Command, []string) {}}, "servers stop"),
	)
	// config is a thing an old form passes through, with no verb of its own yet.
	config := &cobra.Command{Use: "config", Short: "The settings"}
	server.AddCommand(helpout.Old(&cobra.Command{Use: "show", Short: "Show the server settings", Run: func(*cobra.Command, []string) {}}, "config server"))
	root.AddCommand(server, config)
	helpout.Apply(root, helpout.Info{What: "a demo"})
	return root
}

func TestAnAppWhoseOldFormsAreRightPasses(t *testing.T) {
	helptest.Check(t, app)
}

func TestABrokenOldFormFails(t *testing.T) {
	for _, c := range []struct {
		name  string
		spoil func(root *cobra.Command)
	}{
		{"an old form of the root itself", func(root *cobra.Command) { helpout.Old(root, "nowhere") }},
		{"an old form listed in help", func(root *cobra.Command) {
			root.AddCommand(&cobra.Command{Use: "servers", Short: "Shown", Run: func(*cobra.Command, []string) {}})
		}},
	} {
		got := &record{}
		helptest.Check(got, func() *cobra.Command { root := app(); c.spoil(root); return root })
		if len(got.errs) == 0 {
			t.Errorf("%s passed", c.name)
		}
	}
}

// record keeps what Check reports.
type record struct{ errs []string }

func (r *record) Helper() {}
func (r *record) Errorf(format string, args ...any) {
	r.errs = append(r.errs, fmt.Sprintf(format, args...))
}
