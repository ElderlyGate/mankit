package helpout

import (
	"bytes"
	"testing"

	"github.com/spf13/cobra"
)

var info = Info{
	What:  "a demo dashboard",
	Start: []string{"demo up", "run it inside a project folder"},
	More:  "https://github.com/example/demo",
}

// sample is a root with two verbs, a hidden and a deprecated one, and flags; layout applies H-1 to it.
func sample(layout bool) *cobra.Command {
	noop := func(*cobra.Command, []string) {}
	root := &cobra.Command{Use: "demo"}
	root.AddCommand(
		&cobra.Command{Use: "status", Short: "Show the state", Run: noop},
		&cobra.Command{Use: "up", Short: "Start the service", Long: "Start the service and wait.", Run: noop},
		&cobra.Command{Use: "secret", Short: "Not for people", Hidden: true, Run: noop},
		&cobra.Command{Use: "old", Short: "The old way", Deprecated: "use up", Run: noop},
	)
	root.Flags().StringP("theme", "t", "", "the colours")
	root.PersistentFlags().Bool("debug", false, "say more")
	root.Flags().Bool("trace", false, "for us")
	root.Flags().MarkHidden("trace")
	root.CompletionOptions.DisableDefaultCmd = true
	if layout {
		Apply(root, info)
	}
	return root
}

// run executes root with args; it returns stdout, stderr and the error.
func run(root *cobra.Command, args ...string) (string, string, error) {
	var out, errOut bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errOut)
	root.SetArgs(args)
	err := root.Execute()
	return out.String(), errOut.String(), err
}

const golden = `demo: a demo dashboard

Start:
  demo up
  run it inside a project folder

Commands:
  help     Help about any command
  status   Show the state
  up       Start the service

Flags:
      --debug          say more
  -t, --theme string   the colours

More: https://github.com/example/demo
`

func TestRootHelpIsTheLayout(t *testing.T) {
	for _, args := range [][]string{{"help"}, {"--help"}, {"-h"}, {}} {
		out, errOut, err := run(sample(true), args...)
		if out != golden || errOut != "" || err != nil {
			t.Errorf("%v:\n%s\nwant:\n%s\nstderr %q, err %v", args, out, golden, errOut, err)
		}
	}
}

func TestApplyTwiceIsTheSame(t *testing.T) {
	root := sample(true)
	Apply(root, info)
	if out, _, _ := run(root, "help"); out != golden {
		t.Errorf("got:\n%s", out)
	}
	got, _, _ := run(root, "help", "up")
	want, _, _ := run(sample(false), "help", "up")
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestVerbHelpStaysCobras(t *testing.T) {
	for _, args := range [][]string{{"help", "up"}, {"up", "--help"}} {
		got, gotErr, _ := run(sample(true), args...)
		want, wantErr, _ := run(sample(false), args...)
		if got != want || gotErr != wantErr {
			t.Errorf("%v:\n%s\nwant:\n%s", args, got, want)
		}
	}
}

// cobra sends usage to Out when one is set, so stdout and stderr are read together here.
func TestUsageOnAMistakeIsTheLayout(t *testing.T) {
	out, errOut, _ := run(sample(true), "help", "nope")
	if got := errOut + out; got != "Unknown help topic [`nope`]\n"+golden {
		t.Errorf("help nope:\n%s", got)
	}
	out, errOut, err := run(sample(true), "--nope")
	if got := errOut + out; err == nil || got != "Error: unknown flag: --nope\n"+golden+"\n" {
		t.Errorf("--nope:\n%s", got)
	}
}

func TestLeavesOutWhatIsEmpty(t *testing.T) {
	root := &cobra.Command{Use: "bare"}
	root.AddCommand(&cobra.Command{Use: "go", Short: "Go", Run: func(*cobra.Command, []string) {}})
	root.CompletionOptions.DisableDefaultCmd = true
	Apply(root, Info{})
	out, _, _ := run(root, "help")
	if want := "bare\n\nCommands:\n  go     Go\n  help   Help about any command\n"; out != want {
		t.Errorf("got:\n%q\nwant:\n%q", out, want)
	}
}

func TestBareRootStillRunsTheApp(t *testing.T) {
	root := sample(false)
	ran := false
	root.RunE = func(*cobra.Command, []string) error { ran = true; return nil }
	Apply(root, info)
	out, errOut, err := run(root)
	if !ran || out != "" || errOut != "" || err != nil {
		t.Errorf("ran %v, stdout %q, stderr %q, err %v", ran, out, errOut, err)
	}
}
