package helpout

import (
	"errors"
	"io"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// HelpKind is the kind of the answer <app> help -o json gives (contracts mankit/v1 help).
const HelpKind = "Help"

// Answerer prints a command's answer as the app's other commands do: rows as a table, or the envelope with -o json or yaml. serve.App.Printer gives one.
type Answerer interface {
	Check() error
	Print(w io.Writer, name, kind string, data any, rows func(io.Writer)) error
}

// The data of <app> help -o json; contracts mankit/v1 help says what each field is.
type (
	HelpData struct {
		Name     string        `json:"name"`
		What     string        `json:"what"`
		Start    []string      `json:"start"`
		Commands []HelpCommand `json:"commands"`
		Flags    []HelpFlag    `json:"flags"`
		More     string        `json:"more"`
		Old      []OldForm     `json:"old"`
	}
	HelpCommand struct {
		Name     string        `json:"name"`
		Summary  string        `json:"summary"`
		Usage    string        `json:"usage"`
		Commands []HelpCommand `json:"commands"`
		Flags    []HelpFlag    `json:"flags"`
	}
	HelpFlag struct {
		Name      string `json:"name"`
		Shorthand string `json:"shorthand,omitempty"`
		Type      string `json:"type"`
		Default   string `json:"default,omitempty"`
		Usage     string `json:"usage"`
	}
)

// errNoAnswer is help -o json or yaml in an app that gave helpout no Answer.
var errNoAnswer = errors.New("this app does not answer help -o json or yaml yet; plain help shows the same commands")

// answerHelp gives root's help command -o, and with -o json or yaml prints the commands as data through info.Answer; a table is the layout, as before.
func answerHelp(root *cobra.Command, info Info) {
	root.InitDefaultHelpCmd()
	help := child(root, "help")
	if help == nil || help.Flags().Lookup("output") != nil {
		return
	}
	var out Answerer
	var output string
	if info.Answer != nil {
		out = info.Answer(help)
	} else {
		help.Flags().StringVarP(&output, "output", "o", "table", "output format: table, json or yaml")
	}
	run, runE := help.Run, help.RunE
	help.Run, help.RunE = nil, func(c *cobra.Command, args []string) error {
		if f := c.Flags().Lookup("output"); f == nil || f.Value.String() == "table" {
			if runE != nil {
				return runE(c, args)
			}
			run(c, args)
			return nil
		}
		if out == nil {
			return errNoAnswer
		}
		if err := out.Check(); err != nil {
			return err
		}
		data := helpData(root, info)
		if len(args) > 0 {
			topic, _, err := root.Find(args)
			if err != nil {
				return err
			}
			data.Commands = []HelpCommand{command(root, topic)}
		}
		return out.Print(c.OutOrStdout(), "", HelpKind, data, func(io.Writer) {})
	}
}

// helpData is root's help as data: what the layout shows, and the old forms.
func helpData(root *cobra.Command, info Info) HelpData {
	d := HelpData{Name: root.Name(), What: info.What, Start: append([]string{}, info.Start...), More: info.More,
		Commands: commands(root), Flags: flags(root), Old: OldForms(root)}
	if d.Old == nil {
		d.Old = []OldForm{}
	}
	return d
}

// commands are c's commands people see, as the layout lists them.
func commands(c *cobra.Command) []HelpCommand {
	out := []HelpCommand{}
	for _, sub := range c.Commands() {
		if sub.IsAvailableCommand() || sub.Name() == "help" && !sub.Hidden {
			out = append(out, command(c.Root(), sub))
		}
	}
	return out
}

func command(root, c *cobra.Command) HelpCommand {
	usage := strings.TrimPrefix(strings.TrimPrefix(c.UseLine(), root.Name()), " ")
	usage = strings.TrimSuffix(usage, " [flags]")
	return HelpCommand{Name: c.Name(), Summary: c.Short, Usage: usage, Commands: commands(c), Flags: flags(c)}
}

// flags are c's own flags, help and hidden ones left out.
func flags(c *cobra.Command) []HelpFlag {
	out := []HelpFlag{}
	c.LocalNonPersistentFlags().VisitAll(func(f *pflag.Flag) { out = appendFlag(out, f) })
	c.PersistentFlags().VisitAll(func(f *pflag.Flag) { out = appendFlag(out, f) })
	return out
}

func appendFlag(out []HelpFlag, f *pflag.Flag) []HelpFlag {
	if f.Hidden || f.Name == "help" {
		return out
	}
	h := HelpFlag{Name: f.Name, Shorthand: f.Shorthand, Type: f.Value.Type(), Usage: f.Usage}
	switch f.DefValue {
	case "", "false", "0", "[]":
	default:
		h.Default = f.DefValue
	}
	return append(out, h)
}
