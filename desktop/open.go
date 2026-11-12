// Package desktop reaches the user's desktop: the browser and the clipboard, including from WSL.
package desktop

import (
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// platform is what the open and copy strategies depend on.
type platform struct {
	GOOS     string
	WSL      bool
	LookPath func(string) (string, error)
}

func current() platform {
	return platform{GOOS: runtime.GOOS, WSL: IsWSL(), LookPath: exec.LookPath}
}

func IsWSL() bool {
	b, err := os.ReadFile("/proc/version")
	return err == nil && strings.Contains(strings.ToLower(string(b)), "microsoft")
}

type opener struct {
	applies func(platform) bool
	argv    func(p platform, url string) []string
}

// openers are tried in order; the last always applies.
var openers = []opener{
	{func(p platform) bool { return p.GOOS == "darwin" }, func(_ platform, u string) []string { return []string{"open", u} }},
	{func(p platform) bool { return p.GOOS == "windows" }, func(_ platform, u string) []string {
		return []string{"rundll32", "url.dll,FileProtocolHandler", u}
	}},
	{func(p platform) bool { return p.WSL && has(p, "wslview") }, func(p platform, u string) []string {
		path, _ := p.LookPath("wslview")
		return []string{path, u}
	}},
	{func(p platform) bool { return p.WSL }, func(_ platform, u string) []string {
		return []string{"rundll32.exe", "url.dll,FileProtocolHandler", u}
	}},
	{func(platform) bool { return true }, func(_ platform, u string) []string { return []string{"xdg-open", u} }},
}

func has(p platform, name string) bool {
	_, err := p.LookPath(name)
	return err == nil
}

// openCommand is the command that opens url on p.
func openCommand(p platform, url string) []string {
	for _, o := range openers {
		if o.applies(p) {
			return o.argv(p, url)
		}
	}
	return nil
}

// Open opens url in the default browser without waiting for it.
func Open(url string) error {
	argv := openCommand(current(), url)
	return exec.Command(argv[0], argv[1:]...).Start()
}
