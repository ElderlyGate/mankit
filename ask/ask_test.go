package ask

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

var (
	deleteCard  = Action{Type: Delete, What: "delete card 20261102-ab12", Name: "20261102-ab12"}
	deleteBoard = Action{Type: Delete, What: deleteCard.What, Name: deleteCard.Name, System: true}
	moveCard    = Action{Type: Move, What: "move card 7 to review"}
)

const (
	yesNo     = "Proceed? [y/N/a] "
	typeName  = "Type 20261102-ab12 to go ahead (add ! to stop asking for delete): "
	refusedNo = "refused: delete card 20261102-ab12"
	refusedMv = "refused: move card 7 to review"
	noTermDel = refusedNo + "; pass --yes to make the change"
)

// run executes a command that asks before action in style; it returns stderr, whether the change ran, the answer and the error.
func run(t *testing.T, onTerminal bool, action Action, style Style, stdin string, args ...string) (string, bool, Answer, error) {
	t.Helper()
	was := isTerminal
	isTerminal = func(io.Reader, io.Writer) bool { return onTerminal }
	t.Cleanup(func() { isTerminal = was })
	var changed bool
	var answer Answer
	cmd := &cobra.Command{Use: "change", RunE: func(c *cobra.Command, _ []string) error {
		var err error
		if answer, err = Change(c, action, style); err != nil {
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
	return errOut.String(), changed, answer, err
}

func TestChange(t *testing.T) {
	askDel := deleteCard.What + "\n" + yesNo
	askName := deleteCard.What + "\n" + typeName
	askMove := moveCard.What + "\n" + yesNo
	tests := []struct {
		name         string
		onTerminal   bool
		action       Action
		style        Style
		stdin        string
		args         []string
		wantStderr   string
		wantRemember bool
		wantErr      string
	}{
		{"--yes, yes-no, terminal", true, deleteCard, YesNo, "", []string{"--yes"}, "", false, ""},
		{"--yes, name, terminal", true, deleteCard, Name, "", []string{"--yes"}, "", false, ""},
		{"--yes, name, no terminal", false, deleteCard, Name, "", []string{"--yes"}, "", false, ""},
		{"no terminal, yes-no", false, deleteCard, YesNo, "y\n", nil, "", false, noTermDel},
		{"no terminal, name", false, deleteCard, Name, "20261102-ab12\n", nil, "", false, noTermDel},
		{"no terminal, never", false, deleteCard, Never, "", nil, "", false, noTermDel},
		{"no terminal, --yes=false", false, deleteCard, Never, "", []string{"--yes=false"}, "", false, noTermDel},
		{"never", true, deleteCard, Never, "", nil, "", false, ""},

		{"yes-no y", true, deleteCard, YesNo, "y\n", nil, askDel, false, ""},
		{"yes-no Y", true, deleteCard, YesNo, "Y\n", nil, askDel, false, ""},
		{"yes-no yes", true, deleteCard, YesNo, "yes\n", nil, askDel, false, ""},
		{"yes-no YES with spaces", true, deleteCard, YesNo, "  YES \n", nil, askDel, false, ""},
		{"yes-no a", true, deleteCard, YesNo, "a\n", nil, askDel, true, ""},
		{"yes-no A", true, deleteCard, YesNo, "A\n", nil, askDel, true, ""},
		{"yes-no always", true, deleteCard, YesNo, "always\n", nil, askDel, true, ""},
		{"yes-no n", true, deleteCard, YesNo, "n\n", nil, askDel, false, refusedNo},
		{"yes-no empty line", true, deleteCard, YesNo, "\n", nil, askDel, false, refusedNo},
		{"yes-no other word", true, deleteCard, YesNo, "yep\n", nil, askDel, false, refusedNo},
		{"yes-no EOF", true, deleteCard, YesNo, "", nil, askDel + "\n", false, refusedNo},
		{"yes-no y then EOF", true, deleteCard, YesNo, "y", nil, askDel + "\n", false, refusedNo},

		{"name typed", true, deleteCard, Name, "20261102-ab12\n", nil, askName, false, ""},
		{"name typed with spaces", true, deleteCard, Name, " 20261102-ab12 \n", nil, askName, false, ""},
		{"name with !", true, deleteCard, Name, "20261102-ab12!\n", nil, askName, true, ""},
		{"name wrong", true, deleteCard, Name, "20261102-ab13\n", nil, askName, false, refusedNo},
		{"name other case", true, Action{Type: Delete, What: deleteCard.What, Name: "Roadmap"}, Name, "roadmap\n", nil,
			deleteCard.What + "\nType Roadmap to go ahead (add ! to stop asking for delete): ", false, refusedNo},
		{"name y", true, deleteCard, Name, "y\n", nil, askName, false, refusedNo},
		{"name a", true, deleteCard, Name, "a\n", nil, askName, false, refusedNo},
		{"name EOF", true, deleteCard, Name, "", nil, askName + "\n", false, refusedNo},

		{"no style, delete asks yes-no", true, deleteCard, "", "y\n", nil, askDel, false, ""},
		{"no style, move asks yes-no", true, moveCard, "", "y\n", nil, askMove, false, ""},
		{"no style, a system change asks the name", true, deleteBoard, "", "20261102-ab12\n", nil, askName, false, ""},
		{"a system change in yes-no asks yes-no", true, deleteBoard, YesNo, "y\n", nil, askDel, false, ""},
		{"a system change in never still asks the name", true, deleteBoard, Never, "20261102-ab12\n", nil, askName, false, ""},
		{"a system change in never, --yes", true, deleteBoard, Never, "", []string{"--yes"}, "", false, ""},
		{"a system change in never, no terminal", false, deleteBoard, Never, "", nil, "", false, noTermDel},
		{"unknown style is the default", true, deleteBoard, "always", "20261102-ab12\n", nil, askName, false, ""},
		{"name with no Name asks yes-no", true, moveCard, Name, "n\n", nil, askMove, false, refusedMv},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stderr, changed, answer, err := run(t, tt.onTerminal, tt.action, tt.style, tt.stdin, tt.args...)
			if stderr != tt.wantStderr {
				t.Errorf("stderr = %q, want %q", stderr, tt.wantStderr)
			}
			if tt.wantErr == "" {
				if err != nil || !changed || answer.Remember != tt.wantRemember {
					t.Fatalf("changed %v, remember %v, err %v; want a yes, remember %v", changed, answer.Remember, err, tt.wantRemember)
				}
				return
			}
			if changed || err == nil || err.Error() != tt.wantErr || !errors.Is(err, ErrRefused) {
				t.Fatalf("changed %v, err %v; want ErrRefused %q", changed, err, tt.wantErr)
			}
		})
	}
}

func TestChangeWithoutTheYesFlagIsAnErrorOfTheApp(t *testing.T) {
	cmd := &cobra.Command{Use: "change"}
	_, err := Change(cmd, deleteCard, YesNo)
	if err == nil || errors.Is(err, ErrRefused) || !strings.Contains(err.Error(), "ask.Flag") {
		t.Fatalf("err = %v, want an error naming ask.Flag, not a refusal", err)
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
