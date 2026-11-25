package helpout

import (
	"fmt"
	"io"

	"go.naywee.com/mankit/v5/internal/encode"
)

// Encode writes v to w as -o json or -o yaml prints it: indented by two, the one form every app's output has (internal/encode); another format is an error naming it.
func Encode(w io.Writer, format string, v any) error {
	var b []byte
	var err error
	switch format {
	case "json":
		b, err = encode.JSON(v)
	case "yaml":
		b, err = encode.YAML(v)
	default:
		return fmt.Errorf("helpout: cannot encode as %q (want json or yaml)", format)
	}
	if err != nil {
		return err
	}
	_, err = w.Write(b)
	return err
}
