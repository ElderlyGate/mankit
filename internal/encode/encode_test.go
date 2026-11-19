package encode

import (
	"testing"
)

func TestJSONIsIndentedByTwoWithANewline(t *testing.T) {
	b, err := JSON(map[string]any{"a": []int{1}})
	if err != nil {
		t.Fatal(err)
	}
	if want := "{\n  \"a\": [\n    1\n  ]\n}\n"; string(b) != want {
		t.Errorf("JSON = %q, want %q", b, want)
	}
	if _, err := JSON(func() {}); err == nil {
		t.Error("a func was encoded")
	}
}

func TestYAMLIsIndentedByTwo(t *testing.T) {
	b, err := YAML(map[string]any{"confirm": map[string]string{"delete": "name"}})
	if err != nil {
		t.Fatal(err)
	}
	if want := "confirm:\n  delete: name\n"; string(b) != want {
		t.Errorf("YAML = %q, want %q", b, want)
	}
}
