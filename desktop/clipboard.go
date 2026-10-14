package desktop

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/aymanbagabas/go-osc52/v2"
)

// osc52Out is the terminal OSC 52 is written to.
var osc52Out io.Writer = os.Stderr

// clipboardBackends are tried in order after OSC 52.
var clipboardBackends = []func(string) error{wslClip, systemClipboard}

// Copy sends s over OSC 52, which reaches the local clipboard through SSH
// and tmux, and to the system clipboard where one is available.
func Copy(s string) error {
	oscErr := writeOSC52(osc52Out, s, os.Getenv("TMUX") != "")
	for _, backend := range clipboardBackends {
		if backend(s) == nil {
			return nil
		}
	}
	return oscErr
}

func writeOSC52(w io.Writer, s string, tmux bool) error {
	seq := osc52.New(s)
	if tmux {
		seq = seq.Tmux()
	}
	_, err := seq.WriteTo(w)
	return err
}

var errNotWSL = errors.New("not WSL")

func wslClip(s string) error {
	if !IsWSL() {
		return errNotWSL
	}
	c := exec.Command("clip.exe")
	c.Stdin = strings.NewReader(s)
	return c.Run()
}
