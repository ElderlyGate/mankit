package theme

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"gopkg.in/yaml.v3"
)

// Config is the colour part of an app's config.yaml:
//
//	theme: nord          # default, nord, gruvbox, solarized, high-contrast, mono
//	background: auto     # auto, dark or light: which variant of the theme
type Config struct {
	Theme      string `yaml:"theme"`
	Background string `yaml:"background"`
}

// ConfigPath is <config dir>/<app>/config.yaml.
func ConfigPath(app string) (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, app, "config.yaml"), nil
}

// LoadConfig reads the colour settings from path; a missing file is fine.
func LoadConfig(path string) (Config, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Config{}, nil
	}
	if err != nil {
		return Config{}, err
	}
	var c Config
	if err := yaml.Unmarshal(b, &c); err != nil {
		return Config{}, fmt.Errorf("%s: %w", path, err)
	}
	return c, nil
}

// Choice is a resolved theme: the shared styles and the colours an app
// builds its own from. Colors is empty when Mono is set.
type Choice struct {
	Base   Base
	Colors Colors
	Mono   bool
}

// Pick turns a theme name and a background (auto, dark, light or "") into
// a Choice. "light" is kept as the default theme on a light background.
func Pick(name, background string) (Choice, error) {
	if name == "" {
		name = "default"
	}
	if name == "light" {
		name, background = "default", "light"
	}
	if name == "mono" {
		return Choice{Base: Mono, Mono: true}, nil
	}
	p, ok := PaletteNamed(name)
	if !ok {
		return Choice{}, fmt.Errorf("unknown theme %q (have: %s)", name, strings.Join(Names(), ", "))
	}
	dark := true
	switch background {
	case "", "auto":
		dark = darkTerminal()
	case "dark":
	case "light":
		dark = false
	default:
		return Choice{}, fmt.Errorf("unknown background %q (want auto, dark or light)", background)
	}
	c := p.Dark
	if !dark {
		c = p.Light
	}
	return Choice{Base: NewBase(p.Name, c, p.Light.Text), Colors: c}, nil
}

// darkTerminal asks the terminal for its background; tests replace it.
var darkTerminal = lipgloss.HasDarkBackground

// Resolve picks an app's theme: --theme when given, else mono under
// NO_COLOR, else theme and background from the app's config.yaml, else
// default. flag is the --theme value, set whether it was given.
func Resolve(app, flag string, set bool) (Choice, error) {
	var cfg Config
	if path, err := ConfigPath(app); err == nil {
		if cfg, err = LoadConfig(path); err != nil {
			return Choice{}, err
		}
	}
	switch {
	case set:
		return Pick(flag, cfg.Background)
	case os.Getenv("NO_COLOR") != "":
		return Pick("mono", "")
	}
	return Pick(cfg.Theme, cfg.Background)
}
