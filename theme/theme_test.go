package theme

import "testing"

func TestNamedFallsBack(t *testing.T) {
	themes := map[string]Base{"default": Default, "mono": Mono}
	t.Setenv("NO_COLOR", "")
	if got, ok := Named(themes, "nope"); ok || got.Name != "default" {
		t.Fatalf("unknown name: %q %v", got.Name, ok)
	}
	t.Setenv("NO_COLOR", "1")
	if got, _ := Named(themes, "default"); got.Name != "mono" {
		t.Fatalf("NO_COLOR picks mono: %q", got.Name)
	}
}
