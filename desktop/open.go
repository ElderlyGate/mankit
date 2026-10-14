// Package desktop reaches the user's desktop: the browser and the clipboard, including from WSL.
package desktop

import (
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// Platform is what the open and copy strategies depend on.
type Platform struct {
	GOOS     string
	WSL      bool
	LookPath func(string) (string, error)
}

func Current() Platform {
	return Platform{GOOS: runtime.GOOS, WSL: IsWSL(), LookPath: exec.LookPath}
}

func IsWSL() bool {
	b, err := os.ReadFile("/proc/version")
	return err == nil && strings.Contains(strings.ToLower(string(b)), "microsoft")
}

type opener struct {
	applies func(Platform) bool
	argv    func(p Platform, url string) []string
}

// openers are tried in order; the last always applies.
var openers = []opener{
	{func(p Platform) bool { return p.GOOS == "darwin" }, func(_ Platform, u string) []string { return []string{"open", u} }},
	{func(p Platform) bool { return p.GOOS == "windows" }, func(_ Platform, u string) []string {
		return []string{"rundll32", "url.dll,FileProtocolHandler", u}
	}},
	{func(p Platform) bool { return p.WSL && has(p, "wslview") }, func(p Platform, u string) []string {
		path, _ := p.LookPath("wslview")
		return []string{path, u}
	}},
	{func(p Platform) bool { return p.WSL }, func(_ Platform, u string) []string {
		return []string{"rundll32.exe", "url.dll,FileProtocolHandler", u}
	}},
	{func(Platform) bool { return true }, func(_ Platform, u string) []string { return []string{"xdg-open", u} }},
}

func has(p Platform, name string) bool {
	_, err := p.LookPath(name)
	return err == nil
}

// OpenCommand is the command that opens url on p.
func OpenCommand(p Platform, url string) []string {
	for _, o := range openers {
		if o.applies(p) {
			return o.argv(p, url)
		}
	}
	return nil
}

// Open opens url in the default browser without waiting for it.
func Open(url string) error {
	argv := OpenCommand(Current(), url)
	return exec.Command(argv[0], argv[1:]...).Start()
}
