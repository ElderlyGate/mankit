// Package asking holds the name question store unfinished remove asks, so a test of the store stands in for it the way a person types the name.
package asking

import (
	"github.com/spf13/cobra"

	"github.com/tugbayatilla/mankit/v4/ask"
)

// Name asks for the thing's name before a clear, whatever style the app's config picks for delete: a clear may lose a cut-short change, so it is a change to the system itself (G-STACK-6), and no remembered answer silences it.
var Name = func(c *cobra.Command, a ask.Action) error {
	_, err := ask.Change(c, a, ask.Name)
	return err
}
