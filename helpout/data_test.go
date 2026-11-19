package helpout

import (
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// stub is an Answerer that writes the data as plain JSON, with -o and no envelope: the app's own Answerer adds the envelope.
type stub struct{ output *string }

func (s stub) Check() error {
	if *s.output != "json" {
		return fmt.Errorf("unknown output %q", *s.output)
	}
	return nil
}

func (s stub) Print(w io.Writer, _, kind string, data any, _ func(io.Writer)) error {
	return json.NewEncoder(w).Encode(map[string]any{"kind": kind, "data": data})
}

func answering() *cobra.Command {
	root := thingsLaidOut(false)
	Apply(root, Info{What: "a demo dashboard", Start: []string{"demo up"}, More: "https://github.com/example/demo",
		Answer: func(c *cobra.Command) Answerer {
			var output string
			c.Flags().StringVarP(&output, "output", "o", "table", "output format")
			return stub{&output}
		}})
	return root
}

func TestHelpAsJSONIsTheLayoutsCommandsAndTheOldForms(t *testing.T) {
	out, errOut, code, err := execute(answering(), "help", "-o", "json")
	if code != 0 || err != nil || errOut != "" {
		t.Fatalf("code %d, err %v, stderr %q", code, err, errOut)
	}
	var got struct {
		Kind string
		Data HelpData
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	d := got.Data
	if got.Kind != HelpKind || d.Name != "demo" || d.What != "a demo dashboard" || d.More != "https://github.com/example/demo" || !reflect.DeepEqual(d.Start, []string{"demo up"}) {
		t.Errorf("the head: %+v", d)
	}
	var names []string
	for _, c := range d.Commands {
		names = append(names, c.Name)
	}
	if strings.Join(names, " ") != "config help server status up" {
		t.Errorf("commands %v: the layout's, with no old form or hidden one", names)
	}
	server := d.Commands[2]
	if len(server.Commands) != 4 || server.Commands[1].Name != "list" || server.Commands[1].Usage != "server list" || server.Commands[2].Usage != "server stop [addr]" {
		t.Errorf("server: %+v", server)
	}
	if f := server.Commands[1].Flags; len(f) != 1 || f[0].Name != "all" || f[0].Type != "bool" || f[0].Default != "" {
		t.Errorf("server list flags: %+v", f)
	}
	if len(d.Flags) != 2 || d.Flags[0].Name != "theme" || d.Flags[0].Shorthand != "t" || d.Flags[1].Name != "debug" {
		t.Errorf("root flags: %+v", d.Flags)
	}
	if len(d.Old) != 4 || !reflect.DeepEqual(d.Old[1], OldForm{Words: "queue wait", Now: "config get", Args: []string{"queue.wait"}}) {
		t.Errorf("old: %+v", d.Old)
	}
}

func TestHelpAsJSONOfAThingIsThatThing(t *testing.T) {
	out, _, code, err := execute(answering(), "help", "server", "-o", "json")
	var got struct{ Data HelpData }
	if code != 0 || err != nil || json.Unmarshal([]byte(out), &got) != nil || len(got.Data.Commands) != 1 || got.Data.Commands[0].Name != "server" {
		t.Errorf("code %d, err %v:\n%s", code, err, out)
	}
}

func TestHelpAsJSONWithNoAnswerIsRefusedAndHelpStaysTheLayout(t *testing.T) {
	_, _, code, err := execute(things(), "help", "-o", "json")
	if code != 1 || err != errNoAnswer {
		t.Errorf("code %d, err %v", code, err)
	}
	out, _, code, err := execute(things(), "help", "-o", "table")
	if code != 0 || err != nil || !strings.HasPrefix(out, "demo: a demo dashboard\n") {
		t.Errorf("code %d, err %v:\n%s", code, err, out)
	}
}
