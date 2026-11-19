package helpout

import (
	"encoding/json"
	"fmt"
	"io"

	"gopkg.in/yaml.v3"
)

// Encode writes v to w as -o json or -o yaml prints it: indented by two, the one form every app's output has; another format is an error naming it.
func Encode(w io.Writer, format string, v any) error {
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
	}
	return fmt.Errorf("helpout: cannot encode as %q (want json or yaml)", format)
}
