package theme

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

func TestPick(t *testing.T) {
	darkTerminal = func() bool { return true }
	defer func() { darkTerminal = lipgloss.HasDarkBackground }()
	for _, c := range []struct {
		name, bg, wantName, wantText string
		mono                         bool
	}{
		{"", "", "default", DefaultPalette.Dark.Text, false},
		{"nord", "auto", "nord", Nord.Dark.Text, false},
		{"gruvbox", "light", "gruvbox", Gruvbox.Light.Text, false},
		{"solarized", "dark", "solarized", Solarized.Dark.Text, false},
		{"high-contrast", "", "high-contrast", HighContrast.Dark.Text, false},
		{"light", "", "default", DefaultPalette.Light.Text, false},
		{"mono", "", "mono", "", true},
	} {
		ch, err := Pick(c.name, c.bg)
		if err != nil || ch.Base.Name != c.wantName || ch.Colors.Text != c.wantText || ch.Mono != c.mono {
			t.Errorf("Pick(%q, %q) = %+v %v", c.name, c.bg, ch.Colors, err)
		}
	}
	if _, err := Pick("neon", ""); err == nil {
		t.Error("unknown theme accepted")
	}
	if _, err := Pick("nord", "sepia"); err == nil {
		t.Error("unknown background accepted")
	}
}

func TestEveryPaletteIsComplete(t *testing.T) {
	for _, p := range Palettes {
		for _, c := range []Colors{p.Dark, p.Light} {
			for _, v := range []string{c.Text, c.Accent, c.Dim, c.Border, c.Good, c.Warn, c.Bad, c.Info, c.Blue, c.Selection, c.Hit, c.CurHit} {
				if len(v) != 7 || v[0] != '#' {
					t.Errorf("%s: colour %q", p.Name, v)
				}
			}
		}
	}
}

func TestResolveOrder(t *testing.T) {
	darkTerminal = func() bool { return true }
	defer func() { darkTerminal = lipgloss.HasDarkBackground }()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("NO_COLOR", "")
	if ch, _ := Resolve("app", "", false); ch.Base.Name != "default" {
		t.Fatalf("no config: %s", ch.Base.Name)
	}
	os.MkdirAll(filepath.Join(dir, "app"), 0o755)
	os.WriteFile(filepath.Join(dir, "app", "config.yaml"), []byte("theme: gruvbox\nbackground: light\nother: kept\n"), 0o644)
	if ch, _ := Resolve("app", "", false); ch.Base.Name != "gruvbox" || ch.Colors != Gruvbox.Light {
		t.Fatalf("config: %+v", ch.Base.Name)
	}
	if ch, _ := Resolve("app", "nord", true); ch.Base.Name != "nord" || ch.Colors != Nord.Light {
		t.Fatalf("--theme wins, background from config: %s", ch.Base.Name)
	}
	t.Setenv("NO_COLOR", "1")
	if ch, _ := Resolve("app", "", false); !ch.Mono {
		t.Fatal("NO_COLOR beats the config")
	}
	if ch, _ := Resolve("app", "nord", true); ch.Mono {
		t.Fatal("an explicit --theme beats NO_COLOR")
	}
	os.WriteFile(filepath.Join(dir, "app", "config.yaml"), []byte("theme: [\n"), 0o644)
	if _, err := Resolve("app", "", false); err == nil {
		t.Fatal("broken config accepted")
	}
}
