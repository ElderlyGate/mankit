package helpout

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
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

// execute runs root through Execute with args; it returns stdout, stderr, the code and the error.
func execute(root *cobra.Command, args ...string) (string, string, int, error) {
	var out, errOut bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errOut)
	root.SetArgs(args)
	code, err := Execute(context.Background(), root)
	return out.String(), errOut.String(), code, err
}

// usageOf is cobra's usage of the verb at path in a root without the layout.
func usageOf(path ...string) string {
	c, _, _ := sample(false).Find(path)
	c.InitDefaultHelpFlag()
	return c.UsageString()
}

// noArgsRoot is sample with a root that runs and takes no arguments, as gitman and hubman have it.
func noArgsRoot() *cobra.Command {
	root := sample(true)
	root.Args = cobra.NoArgs
	root.RunE = func(*cobra.Command, []string) error { return nil }
	return root
}

// traversing is sample with a root that runs and parses flags before the verb, as dcman has it.
func traversing() *cobra.Command {
	root := sample(true)
	root.TraverseChildren = true
	root.RunE = func(*cobra.Command, []string) error { return errors.New("the dashboard ran") }
	return root
}

func TestARootMistakeIsTheErrorThenTheLayout(t *testing.T) {
	for _, c := range []struct {
		root func() *cobra.Command
		args []string
		msg  string
	}{
		{func() *cobra.Command { return sample(true) }, []string{"--nope"}, "unknown flag: --nope"},
		{func() *cobra.Command { return sample(true) }, []string{"-x"}, "unknown shorthand flag: 'x' in -x"},
		{func() *cobra.Command { return sample(true) }, []string{"nope"}, `unknown command "nope" for "demo"`},
		{func() *cobra.Command { return sample(true) }, []string{"help", "nope"}, `unknown help topic "nope"`},
		{func() *cobra.Command { return sample(true) }, []string{"help", "up", "nope"}, `unknown help topic "up nope"`},
		{noArgsRoot, []string{"nope"}, `unknown command "nope" for "demo"`},
		{noArgsRoot, []string{"help", "nope"}, `unknown help topic "nope"`},
		{traversing, []string{"nope"}, `unknown command "nope" for "demo"`},
		{traversing, []string{"--nope", "status"}, "unknown flag: --nope"},
	} {
		out, errOut, code, err := execute(c.root(), c.args...)
		if want := "error: " + c.msg + "\n" + golden; errOut != want || out != "" || code != 2 || err != nil {
			t.Errorf("%v: code %d, err %v, stdout %q, stderr:\n%s\nwant:\n%s", c.args, code, err, out, errOut, want)
		}
	}
}

func TestAVerbMistakeIsTheErrorThenItsUsage(t *testing.T) {
	root := sample(true)
	up, _, _ := root.Find([]string{"up"})
	up.Args = cobra.NoArgs
	for _, c := range []struct {
		args []string
		msg  string
	}{
		{[]string{"up", "--nope"}, "unknown flag: --nope"},
		{[]string{"up", "now"}, `unknown command "now" for "demo up"`},
	} {
		out, errOut, code, err := execute(root, c.args...)
		if want := "error: " + c.msg + "\n" + usageOf("up"); errOut != want || out != "" || code != 2 || err != nil {
			t.Errorf("%v: code %d, err %v, stdout %q, stderr:\n%s\nwant:\n%s", c.args, code, err, out, errOut, want)
		}
	}
}

func TestAVerbWithItsOwnPersistentPreRunStillShowsItsUsageOnlyOnAMistake(t *testing.T) {
	root := sample(true)
	group := &cobra.Command{Use: "group", Short: "A group", PersistentPreRun: func(*cobra.Command, []string) {}}
	boom := errors.New("boom")
	group.AddCommand(&cobra.Command{Use: "fail", Short: "Fail", RunE: func(*cobra.Command, []string) error { return boom }})
	root.AddCommand(group)
	out, errOut, code, err := execute(root, "group", "fail")
	if out != "" || errOut != "" || code != 1 || err != boom {
		t.Errorf("run-time: code %d, err %v, stdout %q, stderr %q", code, err, out, errOut)
	}
	fail, _, _ := root.Find([]string{"group", "fail"})
	out, errOut, code, _ = execute(root, "group", "fail", "--nope")
	if want := "error: unknown flag: --nope\n" + fail.UsageString(); errOut != want || out != "" || code != 2 {
		t.Errorf("mistake: code %d, stdout %q, stderr:\n%s", code, out, errOut)
	}
}

func TestARunTimeErrorIsReturnedUntouched(t *testing.T) {
	root := sample(true)
	boom := errors.New("boom")
	up, _, _ := root.Find([]string{"up"})
	up.Run, up.RunE = nil, func(*cobra.Command, []string) error { return boom }
	out, errOut, code, err := execute(root, "up")
	if out != "" || errOut != "" || code != 1 || err != boom {
		t.Errorf("code %d, err %v, stdout %q, stderr %q", code, err, out, errOut)
	}
}

func TestExecuteKeepsHelpAndTheBareRoot(t *testing.T) {
	for _, args := range [][]string{{"help"}, {"--help"}, {"-h"}, {}} {
		out, errOut, code, err := execute(sample(true), args...)
		if out != golden || errOut != "" || code != 0 || err != nil {
			t.Errorf("%v: code %d, err %v, stdout:\n%s\nstderr %q", args, code, err, out, errOut)
		}
	}
	for _, args := range [][]string{{"help", "up"}, {"up", "--help"}} {
		got, _, code, _ := execute(sample(true), args...)
		want, _, _ := run(sample(false), args...)
		if got != want || code != 0 {
			t.Errorf("%v: code %d:\n%s\nwant:\n%s", args, code, got, want)
		}
	}
	root := sample(false)
	ran := false
	root.RunE = func(*cobra.Command, []string) error { ran = true; return nil }
	Apply(root, info)
	if out, errOut, code, err := execute(root); !ran || out != "" || errOut != "" || code != 0 || err != nil {
		t.Errorf("bare root: ran %v, code %d, err %v, stdout %q, stderr %q", ran, code, err, out, errOut)
	}
}

func TestHelpFindsNestedVerbsAndTopics(t *testing.T) {
	root := sample(true)
	design := &cobra.Command{Use: "design", Short: "Designs"}
	design.AddCommand(&cobra.Command{Use: "rename", Short: "Rename a design", Long: "Rename a design in place.", Run: func(*cobra.Command, []string) {}})
	root.AddCommand(design, &cobra.Command{Use: "spec", Short: "The spec format", Long: "The spec is YAML."})
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"help", "design", "rename"}, "Rename a design in place."},
		{[]string{"help", "spec"}, "The spec is YAML."},
	} {
		out, errOut, code, err := execute(root, c.args...)
		if !strings.HasPrefix(out, c.want) || errOut != "" || code != 0 || err != nil {
			t.Errorf("%v: code %d, err %v, stdout:\n%s\nstderr %q", c.args, code, err, out, errOut)
		}
	}
}

func TestHelpStillCompletesVerbs(t *testing.T) {
	out, _, code, err := execute(sample(true), "__complete", "help", "s")
	if !strings.HasPrefix(out, "status\tShow the state\n") || code != 0 || err != nil {
		t.Errorf("code %d, err %v, stdout:\n%s", code, err, out)
	}
}

func TestARunTimeErrorWrappingAFlagErrorIsUntouched(t *testing.T) {
	flags := pflag.NewFlagSet("config", pflag.ContinueOnError)
	flags.SetOutput(io.Discard)
	parse := flags.Parse([]string{"--nope"})
	wrapped := fmt.Errorf("parse config: %w", parse)
	for _, root := range []*cobra.Command{sample(true), traversing()} {
		up, _, _ := root.Find([]string{"up"})
		up.Run, up.RunE = nil, func(*cobra.Command, []string) error { return wrapped }
		root.RunE = func(*cobra.Command, []string) error { return wrapped }
		for _, args := range [][]string{{"up"}, {}} {
			out, errOut, code, err := execute(root, args...)
			if out != "" || errOut != "" || code != 1 || err != wrapped {
				t.Errorf("%v traverse %v: code %d, err %v, stdout %q, stderr %q", args, root.TraverseChildren, code, err, out, errOut)
			}
		}
	}
}

// grouped is sample with a group verb holding `add`, and root's Args and Run as given.
func grouped(args cobra.PositionalArgs, run bool) (*cobra.Command, *cobra.Command) {
	root := sample(true)
	root.Args = args
	if run {
		root.RunE = func(*cobra.Command, []string) error { return nil }
	}
	remote := &cobra.Command{Use: "remote", Short: "The remotes"}
	remote.AddCommand(&cobra.Command{Use: "add", Short: "Add a remote", Run: func(*cobra.Command, []string) {}})
	root.AddCommand(remote)
	return root, remote
}

func TestAnUnknownVerbUnderAGroupIsAMistakeOnTheGroup(t *testing.T) {
	for _, c := range []struct {
		word, msg string
	}{
		{"nope", `unknown command "nope" for "demo remote"`},
		{"ad", "unknown command \"ad\" for \"demo remote\"\n\nDid you mean this?\n\tadd"},
	} {
		root, remote := grouped(nil, false)
		out, errOut, code, err := execute(root, "remote", c.word)
		if want := "error: " + c.msg + "\n" + remote.UsageString(); errOut != want || out != "" || code != 2 || err != nil {
			t.Errorf("%s: code %d, err %v, stdout %q, stderr:\n%s\nwant:\n%s", c.word, code, err, out, errOut, want)
		}
	}
	root, _ := grouped(nil, false)
	out, errOut, code, err := execute(root, "remote")
	if !strings.Contains(out, "add         Add a remote") || errOut != "" || code != 0 || err != nil {
		t.Errorf("bare group: code %d, err %v, stdout:\n%s\nstderr %q", code, err, out, errOut)
	}
}

func TestARootWithArgsButNoRunRefusesAWord(t *testing.T) {
	root, _ := grouped(cobra.NoArgs, false)
	out, errOut, code, err := execute(root, "nope")
	if !strings.HasPrefix(errOut, "error: unknown command \"nope\" for \"demo\"\ndemo: a demo dashboard\n") || out != "" || code != 2 || err != nil {
		t.Errorf("code %d, err %v, stdout %q, stderr:\n%s", code, err, out, errOut)
	}
}

func TestAnUnknownVerbAtTheRootSuggestsOne(t *testing.T) {
	root, _ := grouped(nil, true)
	_, errOut, code, _ := execute(root, "statu")
	if want := "error: unknown command \"statu\" for \"demo\"\n\nDid you mean this?\n\tstatus\ndemo: a demo dashboard\n"; !strings.HasPrefix(errOut, want) || code != 2 {
		t.Errorf("code %d, stderr:\n%s", code, errOut)
	}
}
