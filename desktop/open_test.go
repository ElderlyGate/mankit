package desktop

import (
	"errors"
	"strings"
	"testing"
)

func lookPath(found ...string) func(string) (string, error) {
	return func(name string) (string, error) {
		for _, f := range found {
			if f == name {
				return "/usr/bin/" + name, nil
			}
		}
		return "", errors.New("not found")
	}
}

func TestOpenCommandPerPlatform(t *testing.T) {
	cases := []struct {
		name string
		p    platform
		want string
	}{
		{"macOS uses open", platform{GOOS: "darwin", LookPath: lookPath()}, "open U"},
		{"Windows uses rundll32", platform{GOOS: "windows", LookPath: lookPath()}, "rundll32 url.dll,FileProtocolHandler U"},
		{"WSL prefers wslview", platform{GOOS: "linux", WSL: true, LookPath: lookPath("wslview")}, "/usr/bin/wslview U"},
		{"WSL without wslview uses rundll32.exe", platform{GOOS: "linux", WSL: true, LookPath: lookPath()}, "rundll32.exe url.dll,FileProtocolHandler U"},
		{"Linux uses xdg-open", platform{GOOS: "linux", LookPath: lookPath("wslview")}, "xdg-open U"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := strings.Join(openCommand(c.p, "U"), " "); got != c.want {
				t.Errorf("got %q want %q", got, c.want)
			}
		})
	}
}
