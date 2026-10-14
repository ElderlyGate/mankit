//go:build !darwin && !windows

package desktop

import (
	"errors"
	"os"
	"os/exec"
	"strings"
)

// unixClipboards are tried when copying, not at startup: atotto/clipboard
// searches PATH for all of them in init, which under WSL walks every Windows
// directory on /mnt/c and delays the first frame by over a second.
var unixClipboards = [][]string{
	{"wl-copy"},
	{"xclip", "-in", "-selection", "clipboard"},
	{"xsel", "--input", "--clipboard"},
	{"termux-clipboard-set"},
}

// unixPastes mirror unixClipboards, with Windows' clipboard last for WSL.
var unixPastes = [][]string{
	{"wl-paste", "--no-newline"},
	{"xclip", "-out", "-selection", "clipboard"},
	{"xsel", "--output", "--clipboard"},
	{"termux-clipboard-get"},
	{"powershell.exe", "-NoProfile", "-Command", "Get-Clipboard"},
}

var errNoClipboard = errors.New("no clipboard utility found (xsel, xclip, wl-copy or termux-clipboard-set)")

func systemClipboard(s string) error {
	for _, argv := range unixClipboards {
		if argv[0] == "wl-copy" && os.Getenv("WAYLAND_DISPLAY") == "" {
			continue
		}
		c := exec.Command(argv[0], argv[1:]...)
		c.Stdin = strings.NewReader(s)
		if err := c.Run(); err == nil {
			return nil
		}
	}
	return errNoClipboard
}

// Paste reads the system clipboard.
func Paste() (string, error) {
	for _, argv := range unixPastes {
		if argv[0] == "wl-paste" && os.Getenv("WAYLAND_DISPLAY") == "" {
			continue
		}
		if out, err := exec.Command(argv[0], argv[1:]...).Output(); err == nil {
			if argv[0] == "powershell.exe" {
				return strings.TrimSuffix(strings.ReplaceAll(string(out), "\r\n", "\n"), "\n"), nil
			}
			return string(out), nil
		}
	}
	return "", errNoClipboard
}
