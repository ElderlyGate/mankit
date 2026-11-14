package queue_test

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ElderlyGate/contracts"
	"github.com/spf13/cobra"

	"github.com/tugbayatilla/mankit/v3/ask"
	"github.com/tugbayatilla/mankit/v3/queue"
	"github.com/tugbayatilla/mankit/v3/serve"
)

// configRun runs `config queue` words under a config of its own, as an app mounts it, against the config.yaml at path.
func configRun(t *testing.T, path string, words ...string) (string, error) {
	t.Helper()
	app := serve.App{ID: "designer", Version: "5.0.0", Contracts: []serve.Contract{{Version: "v1"}}}
	root := &cobra.Command{Use: "e-designer", SilenceUsage: true, SilenceErrors: true}
	config := &cobra.Command{Use: "config"}
	asked := func(c *cobra.Command, a ask.Action) error {
		if f := c.Flags().Lookup("yes"); f == nil || f.Value.String() != "true" {
			t.Fatalf("%v asked without --yes", words)
		}
		return nil
	}
	config.AddCommand(queue.ConfigCommand(app, func() string { return path }, asked))
	root.AddCommand(config)
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetArgs(append([]string{"config", "queue"}, words...))
	err := root.Execute()
	return out.String(), err
}

func TestConfigQueueShowsBothSettingsAndTheirDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	out, err := configRun(t, path, "-o", "json")
	if err != nil {
		t.Fatal(err)
	}
	if err := contracts.Validate("mankit/v1/queue-settings.schema.json", []byte(out)); err != nil {
		t.Fatalf("not a QueueSettings answer: %v\n%s", err, out)
	}
	if !strings.Contains(out, `"value": "30s"`) || !strings.Contains(out, `"value": "2m0s"`) || strings.Contains(out, `"set": true`) {
		t.Fatalf("not the defaults:\n%s", out)
	}
	table, err := configRun(t, path)
	if err != nil || !strings.Contains(table, "queue.wait") || !strings.Contains(table, "queue.limit") {
		t.Fatalf("table %q, %v", table, err)
	}
}

func TestConfigQueueSetsAWaitAndPutsTheDefaultBack(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("# mine\ntheme: dark\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := configRun(t, path, "wait", "45s", "--yes"); err != nil {
		t.Fatal(err)
	}
	wait, limit, err := queue.Waits(path)
	if err != nil || wait != "45s" || limit != "" {
		t.Fatalf("waits %q %q, %v", wait, limit, err)
	}
	if raw, _ := os.ReadFile(path); !strings.Contains(string(raw), "# mine") || !strings.Contains(string(raw), "theme: dark") {
		t.Fatalf("the rest of the file was not kept:\n%s", raw)
	}
	out, err := configRun(t, path, "-o", "json")
	if err != nil || !strings.Contains(out, `"value": "45s"`) {
		t.Fatalf("%s, %v", out, err)
	}
	if _, err := configRun(t, path, "wait", "default", "--yes"); err != nil {
		t.Fatal(err)
	}
	if wait, _, _ := queue.Waits(path); wait != "" {
		t.Fatalf("wait %q after default", wait)
	}
}

// A value the queue would refuse is never written: under a second, or no duration at all.
func TestConfigQueueRefusesWhatTheQueueWouldRefuse(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	for _, value := range []string{"1ms", "soon"} {
		_, err := configRun(t, path, "limit", value, "--yes")
		var p *serve.Problem
		if !errors.As(err, &p) || p.Type != serve.TypeFailed || p.Reason != serve.ReasonConfigUnreadable || !strings.Contains(p.Detail, "queue.limit") {
			t.Fatalf("%s: got %v", value, err)
		}
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("a refused value was written")
	}
}
