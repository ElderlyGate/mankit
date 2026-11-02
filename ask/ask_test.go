package ask

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// run executes a command that asks before the change "delete card 7"; it returns stderr, whether the change ran, and the error.
func run(t *testing.T, onTerminal bool, stdin string, args ...string) (string, bool, error) {
	t.Helper()
	was := isTerminal
	isTerminal = func(io.Reader, io.Writer) bool { return onTerminal }
	t.Cleanup(func() { isTerminal = was })
	changed := false
	cmd := &cobra.Command{Use: "delete", RunE: func(c *cobra.Command, _ []string) error {
		if err := Change(c, "delete card 7"); err != nil {
			return err
		}
		changed = true
		return nil
	}}
	Flag(cmd)
	var errOut bytes.Buffer
	cmd.SetIn(strings.NewReader(stdin))
	cmd.SetErr(&errOut)
	cmd.SetOut(io.Discard)
	cmd.SetArgs(args)
	cmd.SilenceErrors, cmd.SilenceUsage = true, true
	err := cmd.Execute()
	return errOut.String(), changed, err
}

func TestChange(t *testing.T) {
	const asked = "delete card 7\nProceed? [y/N] "
	tests := []struct {
		name       string
		onTerminal bool
		stdin      string
		args       []string
		wantChange bool
		wantErr    string
		wantStderr string
	}{
		{"--yes in a terminal", true, "", []string{"--yes"}, true, "", ""},
		{"--yes with no terminal", false, "", []string{"--yes"}, true, "", ""},
		{"y", true, "y\n", nil, true, "", asked},
		{"Y", true, "Y\n", nil, true, "", asked},
		{"yes", true, "yes\n", nil, true, "", asked},
		{"YES with spaces", true, "  YES \n", nil, true, "", asked},
		{"n", true, "n\n", nil, false, "refused: delete card 7", asked},
		{"empty line", true, "\n", nil, false, "refused: delete card 7", asked},
		{"other word", true, "yep\n", nil, false, "refused: delete card 7", asked},
		{"EOF", true, "", nil, false, "refused: delete card 7", asked + "\n"},
		{"y then EOF", true, "y", nil, false, "refused: delete card 7", asked + "\n"},
		{"no terminal", false, "y\n", nil, false, "refused: delete card 7; pass --yes to make the change", ""},
		{"--yes=false with no terminal", false, "", []string{"--yes=false"}, false, "refused: delete card 7; pass --yes to make the change", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stderr, changed, err := run(t, tt.onTerminal, tt.stdin, tt.args...)
			if changed != tt.wantChange {
				t.Errorf("changed = %v, want %v", changed, tt.wantChange)
			}
			if stderr != tt.wantStderr {
				t.Errorf("stderr = %q, want %q", stderr, tt.wantStderr)
			}
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("err = %v, want nil", err)
				}
				return
			}
			if err == nil || err.Error() != tt.wantErr {
				t.Fatalf("err = %v, want %q", err, tt.wantErr)
			}
			if !errors.Is(err, ErrRefused) {
				t.Errorf("err is not ErrRefused: %v", err)
			}
		})
	}
}

func TestFlagHelp(t *testing.T) {
	cmd := &cobra.Command{Use: "x"}
	Flag(cmd)
	f := cmd.Flags().Lookup("yes")
	if f == nil || f.Usage != "make the change without asking" || f.Shorthand != "" {
		t.Fatalf("--yes = %+v, want the one help text and no shorthand", f)
	}
}
