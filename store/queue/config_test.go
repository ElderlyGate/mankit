package queue_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ElderlyGate/contracts"
	"github.com/spf13/cobra"

	"go.naywee.com/mankit/v5/ask"
	"go.naywee.com/mankit/v5/helpout"
	"go.naywee.com/mankit/v5/helpout/helptest"
	"go.naywee.com/mankit/v5/problem"
	"go.naywee.com/mankit/v5/serve"
	"go.naywee.com/mankit/v5/settings"
	"go.naywee.com/mankit/v5/store/queue"
)

// configRun runs `queue` words, the thing an app mounts at its root, against the config.yaml at path.
func configRun(t *testing.T, path string, words ...string) (string, error) {
	t.Helper()
	app := serve.App{ID: "designer", Version: "5.0.0", Contracts: []serve.Contract{{Version: "v1"}}}
	root := &cobra.Command{Use: "e-designer", SilenceUsage: true, SilenceErrors: true}
	asked := func(c *cobra.Command, a ask.Action) error {
		if f := c.Flags().Lookup("yes"); f == nil || f.Value.String() != "true" {
			t.Fatalf("%v asked without --yes", words)
		}
		return nil
	}
	root.AddCommand(queue.Command(app, func() string { return path }, asked))
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetArgs(append([]string{"queue"}, words...))
	err := root.Execute()
	return out.String(), err
}

func TestQueueShowsBothSettingsAndTheirDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	out, err := configRun(t, path, "show", "-o", "json")
	if err != nil {
		t.Fatal(err)
	}
	if err := contracts.Validate("mankit/v1/queue-settings.schema.json", []byte(out)); err != nil {
		t.Fatalf("not a QueueSettings answer: %v\n%s", err, out)
	}
	if !strings.Contains(out, `"value": "30s"`) || !strings.Contains(out, `"value": "2m0s"`) || strings.Contains(out, `"set": true`) {
		t.Fatalf("not the defaults:\n%s", out)
	}
	table, err := configRun(t, path, "show")
	if err != nil || !strings.Contains(table, "queue.wait") || !strings.Contains(table, "queue.limit") {
		t.Fatalf("table %q, %v", table, err)
	}
}

func TestQueueSetsAWaitAndPutsTheDefaultBack(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("# mine\ntheme: dark\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := configRun(t, path, "set", "wait", "45s", "--yes"); err != nil {
		t.Fatal(err)
	}
	wait, limit, err := queue.Waits(path)
	if err != nil || wait != "45s" || limit != "" {
		t.Fatalf("waits %q %q, %v", wait, limit, err)
	}
	if raw, _ := os.ReadFile(path); !strings.Contains(string(raw), "# mine") || !strings.Contains(string(raw), "theme: dark") {
		t.Fatalf("the rest of the file was not kept:\n%s", raw)
	}
	out, err := configRun(t, path, "show", "-o", "json")
	if err != nil || !strings.Contains(out, `"value": "45s"`) {
		t.Fatalf("%s, %v", out, err)
	}
	if _, err := configRun(t, path, "unset", "wait", "--yes"); err != nil {
		t.Fatal(err)
	}
	if wait, _, _ := queue.Waits(path); wait != "" {
		t.Fatalf("wait %q after unset", wait)
	}
}

// A value the queue would refuse is never written, and is refused with ADR-S21's reasons: no duration, nothing, or one under a second or not in whole seconds.
func TestQueueRefusesWhatTheQueueWouldRefuse(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	for value, reason := range map[string]string{"1ms": "out_of_range", "1500ms": "out_of_range", "soon": "wrong_type", "": "empty"} {
		_, err := configRun(t, path, "set", "limit", value, "--yes")
		var p *problem.Problem
		if !errors.As(err, &p) || p.Type != problem.TypeRefused || p.Reason != reason || !strings.Contains(p.Detail, "queue.limit") {
			t.Fatalf("%q: got %v, want refused %s", value, err, reason)
		}
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("a refused value was written")
	}
}

// A value is written as d.String() gives it, the form the contract's pattern takes.
func TestQueueWritesTheCanonicalForm(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if _, err := configRun(t, path, "set", "wait", "+45s", "--yes"); err != nil {
		t.Fatal(err)
	}
	if _, err := configRun(t, path, "set", "limit", "120s", "--yes"); err != nil {
		t.Fatal(err)
	}
	if wait, limit, err := queue.Waits(path); err != nil || wait != "45s" || limit != "2m0s" {
		t.Fatalf("waits %q %q, %v", wait, limit, err)
	}
	out, err := configRun(t, path, "show", "-o", "json")
	if err != nil {
		t.Fatal(err)
	}
	if err := contracts.Validate("mankit/v1/queue-settings.schema.json", []byte(out)); err != nil {
		t.Fatalf("not a QueueSettings answer: %v\n%s", err, out)
	}
}

// A value edited by hand that the queue would refuse is reported, never passed on.
func TestAHandEditedValueTheQueueRefusesIsReported(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("queue.wait: 1500ms\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, words := range [][]string{{"show", "-o", "json"}, {"show"}} {
		_, err := configRun(t, path, words...)
		var p *problem.Problem
		if !errors.As(err, &p) || p.Type != problem.TypeFailed || p.Reason != serve.ReasonConfigUnreadable || !strings.Contains(p.Detail, "queue.wait") {
			t.Fatalf("%v: got %v", words, err)
		}
	}
	if _, _, err := queue.Waits(path); err == nil {
		t.Fatal("Waits passed on a value the queue refuses")
	}
}

// A change the user does not confirm writes nothing.
func TestQueueWritesNothingWithoutAYes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	app := serve.App{ID: "designer", Version: "5.0.0", Contracts: []serve.Contract{{Version: "v1"}}}
	root := &cobra.Command{Use: "e-designer", SilenceUsage: true, SilenceErrors: true}
	root.AddCommand(queue.Command(app, func() string { return path }, func(*cobra.Command, ask.Action) error {
		return problem.Fail(problem.TypeRefused, serve.ReasonUnconfirmed, "nothing was changed")
	}))
	root.SetOut(&bytes.Buffer{})
	root.SetArgs([]string{"queue", "set", "wait", "45s"})
	if err := root.Execute(); err == nil {
		t.Fatal("a change without its yes was made")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("a change without its yes was written")
	}
}

func TestTheWaitAndLimitComeFromTheAppsSettingsRows(t *testing.T) {
	wait, limit, err := queue.Settings("45s", "5m")
	if err != nil || wait != 45*time.Second || limit != 5*time.Minute {
		t.Fatalf("got wait %s limit %s, %v; want 45s and 5m", wait, limit, err)
	}
	if wait, limit, err := queue.Settings("", ""); err != nil || wait != 0 || limit != 0 {
		t.Fatalf("empty rows gave waits %s %s, %v; want zero, the store's defaults", wait, limit, err)
	}
	for _, k := range []settings.Key{queue.WaitKey, queue.LimitKey} {
		if k.Help == "" || !strings.HasPrefix(k.Name, "queue.") {
			t.Errorf("%+v needs a queue. name and a purpose line", k)
		}
	}
	if queue.WaitKey.Default != queue.DefaultWait.String() || queue.LimitKey.Default != queue.DefaultLimit.String() {
		t.Errorf("the rows' defaults %s, %s are not the queue's", queue.WaitKey.Default, queue.LimitKey.Default)
	}
}

func TestAWaitOrLimitThatIsNoDurationOrTooShortIsRefusedNamingTheRow(t *testing.T) {
	for _, c := range []struct{ wait, limit, row string }{
		{"soon", "", "queue.wait"},
		{"", "-1m", "queue.limit"},
		{"1ms", "", "queue.wait"},
		{"", "0s", "queue.limit"},
	} {
		_, _, err := queue.Settings(c.wait, c.limit)
		var p *problem.Problem
		if !errors.As(err, &p) || p.Reason != serve.ReasonConfigUnreadable || !strings.Contains(err.Error(), c.row) {
			t.Errorf("%q %q: got %v, want %s naming %s", c.wait, c.limit, err, serve.ReasonConfigUnreadable, c.row)
		}
	}
}

// default is no duration: unset puts the default back, and set says so.
func TestQueueSetRefusesDefaultAndNamesUnset(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	_, err := configRun(t, path, "set", "wait", "default", "--yes")
	var p *problem.Problem
	if !errors.As(err, &p) || p.Type != problem.TypeRefused || p.Reason != "wrong_type" || !strings.Contains(p.Detail, "queue unset wait") {
		t.Fatalf("got %v", err)
	}
	if _, err := configRun(t, path, "set", "nap", "45s", "--yes"); !errors.As(err, &p) || p.Reason != "unknown_key" {
		t.Fatalf("an unknown setting: %v", err)
	}
}

// The old forms under config queue still run, for one release, as their full forms do (G-STACK-5).
func TestTheOldConfigQueueFormsRunTheirFullForms(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	app := serve.App{ID: "designer", Version: "5.0.0", Contracts: []serve.Contract{{Version: "v1"}}}
	helptest.Check(t, func() *cobra.Command {
		root := &cobra.Command{Use: "e-designer"}
		root.AddCommand(&cobra.Command{Use: "config", Short: "The settings"}, queue.Command(app, func() string { return path }, nil))
		helpout.Apply(root, helpout.Info{What: "a test app"})
		return root
	})
}

// config queue wait default, the old way back to the default, still runs queue unset wait for one release.
func TestTheOldConfigQueueDefaultUnsets(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	app := serve.App{ID: "designer", Version: "5.0.0", Contracts: []serve.Contract{{Version: "v1"}}}
	run := func(args ...string) (string, int, error) {
		root := &cobra.Command{Use: "e-designer"}
		root.AddCommand(&cobra.Command{Use: "config", Short: "The settings"}, queue.Command(app, func() string { return path }, func(*cobra.Command, ask.Action) error { return nil }))
		helpout.Apply(root, helpout.Info{What: "a test app"})
		var out, errOut bytes.Buffer
		root.SetOut(&out)
		root.SetErr(&errOut)
		root.SetArgs(args)
		code, err := helpout.Execute(context.Background(), root)
		return errOut.String(), code, err
	}
	if _, code, err := run("config", "queue", "wait", "45s", "--yes"); code != 0 || err != nil {
		t.Fatalf("config queue wait 45s: %d %v", code, err)
	}
	if wait, _, _ := queue.Waits(path); wait != "45s" {
		t.Fatalf("wait %q after the old set", wait)
	}
	said, code, err := run("config", "queue", "wait", "default", "--yes")
	if code != 0 || err != nil || !strings.Contains(said, "config queue wait default is now queue unset wait") {
		t.Fatalf("config queue wait default: %d %v, stderr %q", code, err, said)
	}
	if wait, _, _ := queue.Waits(path); wait != "" {
		t.Fatalf("wait %q after the old default", wait)
	}
}
