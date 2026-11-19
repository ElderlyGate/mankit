package helpout

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// things is a root with the thing server, whose verbs list and stop print what they were given, and server token renew; servers and servers stop are their old forms.
func things() *cobra.Command {
	root := sample(true)
	echo := func(c *cobra.Command, args []string) {
		all, _ := c.Flags().GetBool("all")
		c.Printf("%s %v all=%v\n", c.CommandPath(), args, all)
	}
	server := &cobra.Command{Use: "server", Short: "The servers this app runs"}
	list := &cobra.Command{Use: "list", Short: "List the servers", Run: echo}
	list.Flags().Bool("all", false, "every one")
	stop := &cobra.Command{Use: "stop [addr]", Short: "Stop a server", Run: echo}
	token := &cobra.Command{Use: "token", Short: "The token"}
	token.AddCommand(&cobra.Command{Use: "renew", Short: "Renew the token", Run: echo})
	server.AddCommand(Old(list, "servers"), Old(stop, "servers stop"), token)
	config := &cobra.Command{Use: "config", Short: "The settings"}
	config.AddCommand(Old(&cobra.Command{Use: "get <key>", Short: "Show a setting", Run: echo}, "get config"))
	root.AddCommand(server, config)
	return root
}

func TestAnOldFormRunsItsFullFormAndSaysTheNewNameOnStderr(t *testing.T) {
	for _, c := range []struct {
		old, full []string
		said      string
	}{
		{[]string{"servers", "--all"}, []string{"server", "list", "--all"}, "demo: servers is now server list\n"},
		{[]string{"servers", "stop", "127.0.0.1:7480"}, []string{"server", "stop", "127.0.0.1:7480"}, "demo: servers stop is now server stop\n"},
		{[]string{"get", "config", "theme"}, []string{"config", "get", "theme"}, "demo: get config is now config get\n"},
		{[]string{"--debug", "servers"}, []string{"server", "list", "--debug"}, "demo: servers is now server list\n"},
	} {
		oldOut, oldErr, oldCode, err := execute(things(), c.old...)
		fullOut, fullErr, fullCode, ferr := execute(things(), c.full...)
		if oldOut != fullOut || oldCode != fullCode || err != nil || ferr != nil {
			t.Errorf("%v: stdout %q code %d err %v; the full form %v: stdout %q code %d err %v", c.old, oldOut, oldCode, err, c.full, fullOut, fullCode, ferr)
		}
		if oldErr != c.said+fullErr {
			t.Errorf("%v: stderr %q, want %q", c.old, oldErr, c.said+fullErr)
		}
	}
}

func TestAnOldFormIsNeverListed(t *testing.T) {
	out, _, _, _ := execute(things(), "help")
	for _, word := range []string{"servers", "get"} {
		if strings.Contains(out, "  "+word+" ") {
			t.Errorf("help lists the old form %q:\n%s", word, out)
		}
	}
	got := OldForms(things())
	want := []OldForm{{"get config", "config get"}, {"servers", "server list"}, {"servers stop", "server stop"}}
	if len(got) != len(want) {
		t.Fatalf("OldForms = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("OldForms[%d] = %v, want %v", i, got[i], want[i])
		}
	}
}

func TestAnOldFormThatIsACommandOfItsOwnIsTheAppsMistake(t *testing.T) {
	root := things()
	server, _, _ := root.Find([]string{"server"})
	Old(server, "status")
	_, _, code, err := execute(root, "server", "list")
	if code != 1 || err == nil || !strings.Contains(err.Error(), `old form "status" is a command of its own`) {
		t.Errorf("code %d, err %v", code, err)
	}
}

func TestAThingsHelpIsTheLayoutAtEveryLevel(t *testing.T) {
	const server = `demo server: The servers this app runs

Commands:
  help    Help about server
  list    List the servers
  stop    Stop a server
  token   The token

More: https://github.com/example/demo
`
	const token = `demo server token: The token

Commands:
  help    Help about token
  renew   Renew the token

More: https://github.com/example/demo
`
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"server", "help"}, server},
		{[]string{"server", "--help"}, server},
		{[]string{"help", "server"}, server},
		{[]string{"server"}, server},
		{[]string{"server", "token", "help"}, token},
		{[]string{"help", "server", "token"}, token},
	} {
		out, errOut, code, err := execute(things(), c.args...)
		if out != c.want || errOut != "" || code != 0 || err != nil {
			t.Errorf("%v: code %d, err %v, stderr %q, stdout:\n%s\nwant:\n%s", c.args, code, err, errOut, out, c.want)
		}
	}
	// A verb's own help stays cobra's.
	out, _, _, _ := execute(things(), "server", "stop", "--help")
	if !strings.Contains(out, "Usage:\n  demo server stop [addr]") {
		t.Errorf("server stop --help:\n%s", out)
	}
}
