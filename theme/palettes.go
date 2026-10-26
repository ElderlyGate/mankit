package theme

// Palette is a named theme in a dark-terminal and a light-terminal variant.
type Palette struct {
	Name        string
	Description string
	Dark, Light Colors
}

// Palettes are the themes every app offers, default first.
var Palettes = []Palette{DefaultPalette, Nord, Gruvbox, Solarized, HighContrast}

// DefaultPalette is the hubman design: green-slate neutrals, a sea-green accent.
var DefaultPalette = Palette{
	Name:        "default",
	Description: "green-slate, the hubman design",
	Dark: Colors{
		Text: "#e3e8e5", Accent: "#7cc4ad", Dim: "#7f8e87", Border: "#4b5b54",
		Good: "#7ed39b", Warn: "#f0c86a", Bad: "#f08a7a", Info: "#73d0c9", Blue: "#7fb2ff",
		Selection: "#2a3a34", Hit: "#f0c86a", CurHit: "#7cc4ad",
	},
	Light: Colors{
		Text: "#1d2421", Accent: "#2f6f5e", Dim: "#5d6862", Border: "#b9c1bb",
		Good: "#2e7d4f", Warn: "#9a6a00", Bad: "#b3412f", Info: "#1f6f8b", Blue: "#2f5fb3",
		Selection: "#e1ede8", Hit: "#f6e3a8", CurHit: "#b9dccf",
	},
}

// Nord is cool blue-grey with frost accents.
var Nord = Palette{
	Name:        "nord",
	Description: "cool blue-grey, low contrast",
	Dark: Colors{
		Text: "#eceff4", Accent: "#88c0d0", Dim: "#7b88a1", Border: "#4c566a",
		Good: "#a3be8c", Warn: "#ebcb8b", Bad: "#bf616a", Info: "#8fbcbb", Blue: "#81a1c1",
		Selection: "#3b4252", Hit: "#ebcb8b", CurHit: "#88c0d0",
	},
	Light: Colors{
		Text: "#2e3440", Accent: "#5e81ac", Dim: "#616e88", Border: "#c2c9d6",
		Good: "#4f7a3a", Warn: "#946c1c", Bad: "#a5414b", Info: "#3b7f8c", Blue: "#4c6a94",
		Selection: "#e5e9f0", Hit: "#f2e2b8", CurHit: "#c5dce6",
	},
}

// Gruvbox is warm amber, orange and olive on brown-grey.
var Gruvbox = Palette{
	Name:        "gruvbox",
	Description: "warm amber and olive, retro",
	Dark: Colors{
		Text: "#ebdbb2", Accent: "#fe8019", Dim: "#928374", Border: "#504945",
		Good: "#b8bb26", Warn: "#fabd2f", Bad: "#fb4934", Info: "#8ec07c", Blue: "#83a598",
		Selection: "#3c3836", Hit: "#fabd2f", CurHit: "#fe8019",
	},
	Light: Colors{
		Text: "#3c3836", Accent: "#af3a03", Dim: "#7c6f64", Border: "#bdae93",
		Good: "#79740e", Warn: "#b57614", Bad: "#9d0006", Info: "#427b58", Blue: "#076678",
		Selection: "#ebdbb2", Hit: "#f9e2a5", CurHit: "#f5c49b",
	},
}

// Solarized is the balanced blue, cyan and yellow on teal-tinted neutrals.
var Solarized = Palette{
	Name:        "solarized",
	Description: "balanced blue, cyan and yellow",
	Dark: Colors{
		Text: "#eee8d5", Accent: "#268bd2", Dim: "#839496", Border: "#586e75",
		Good: "#859900", Warn: "#b58900", Bad: "#dc322f", Info: "#2aa198", Blue: "#6c71c4",
		Selection: "#073642", Hit: "#b58900", CurHit: "#2aa198",
	},
	Light: Colors{
		Text: "#073642", Accent: "#268bd2", Dim: "#657b83", Border: "#d3cbb7",
		Good: "#6c7d00", Warn: "#9c7500", Bad: "#dc322f", Info: "#1f8a82", Blue: "#6c71c4",
		Selection: "#eee8d5", Hit: "#f5e3a3", CurHit: "#bfe3df",
	},
}

// HighContrast is strong, saturated colours for bright rooms or weaker eyesight.
var HighContrast = Palette{
	Name:        "high-contrast",
	Description: "strong, saturated colours",
	Dark: Colors{
		Text: "#ffffff", Accent: "#00e5ff", Dim: "#c8c8c8", Border: "#a0a0a0",
		Good: "#00ff7f", Warn: "#ffd700", Bad: "#ff5555", Info: "#00e5ff", Blue: "#6cb6ff",
		Selection: "#0047ab", Hit: "#ffd700", CurHit: "#00e5ff",
	},
	Light: Colors{
		Text: "#000000", Accent: "#0033cc", Dim: "#333333", Border: "#555555",
		Good: "#006400", Warn: "#7a4f00", Bad: "#b00000", Info: "#005f87", Blue: "#0033cc",
		Selection: "#ffe066", Hit: "#ffd700", CurHit: "#7fdbff",
	},
}

// PaletteNamed returns the palette called name.
func PaletteNamed(name string) (Palette, bool) {
	for _, p := range Palettes {
		if p.Name == name {
			return p, true
		}
	}
	return Palette{}, false
}

// Names lists every theme name --theme and config.yaml accept.
func Names() []string {
	var out []string
	for _, p := range Palettes {
		out = append(out, p.Name)
	}
	return append(out, "mono")
}
