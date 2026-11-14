package prefs

import (
	"github.com/tugbayatilla/mankit/v3/home"
)

// Location is one place the app keeps things.
type Location struct {
	Label string `json:"label" yaml:"label"`
	Path  string `json:"path" yaml:"path"`
	Note  string `json:"note,omitempty" yaml:"note,omitempty"`
}

// Locations is where app keeps things: its config file, its data folder, then extra.
func Locations(app, config string, extra ...Location) ([]Location, error) {
	data, err := home.ProductData(app)
	if err != nil {
		return nil, err
	}
	out := []Location{
		{Label: "This app's config", Path: config},
		{Label: "This app's data", Path: data},
	}
	return append(out, extra...), nil
}
