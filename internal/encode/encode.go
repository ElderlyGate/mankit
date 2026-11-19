// Package encode is the one form mankit writes JSON and YAML in, indented by two spaces: a file the store writes, a config.yaml, and what -o json and -o yaml print (helpout.Encode).
package encode

import (
	"bytes"
	"encoding/json"

	"gopkg.in/yaml.v3"
)

// JSON is v indented by two spaces, ending in a newline.
func JSON(v any) ([]byte, error) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

// YAML is v as a config file holds it: indented by two spaces.
func YAML(v any) ([]byte, error) {
	var b bytes.Buffer
	enc := yaml.NewEncoder(&b)
	enc.SetIndent(2)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}
