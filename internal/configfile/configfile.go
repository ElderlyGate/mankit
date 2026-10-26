// Package configfile is where an app's config.yaml is, shared by theme, usercmd and settings.
package configfile

import (
	"os"
	"path/filepath"
)

// Path is <dir>/<app>/config.yaml; an empty dir is the user config dir.
func Path(app, dir string) (string, error) {
	if dir == "" {
		d, err := os.UserConfigDir()
		if err != nil {
			return "", err
		}
		dir = d
	}
	return filepath.Join(dir, app, "config.yaml"), nil
}
