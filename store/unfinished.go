package store

import (
	"fmt"
	"io"
	"os"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/tugbayatilla/mankit/v3/ask"
	"github.com/tugbayatilla/mankit/v3/serve"
)

// The kinds of the answers <app> store unfinished list and clear give (contracts mankit/v1 store-unfinished-list, store-unfinished-cleared).
const (
	UnfinishedListKind = "StoreUnfinishedList"
	ClearedKind        = "StoreUnfinishedCleared"
)

// UnfinishedData is the data of <app> store unfinished list: every change cut short, those with marks first, then this user's left journals.
type UnfinishedData struct {
	Changes []Unfinished `json:"changes"`
}

// ClearedData is the data of <app> store unfinished clear: the thing, and the changes whose marks were removed.
type ClearedData struct {
	Thing   Key          `json:"thing"`
	Cleared []Unfinished `json:"cleared"`
}

// UnfinishedCommand is `unfinished`, which every app with a store scope mounts under its own store command, as it does config queue:
// `list [<thing>]` shows the changes cut short in the scope open opens, and `clear <thing>` removes their marks after confirm asks, as a change of type delete (G-STACK-13).
// It lives here, beside Unfinished and Clear, and prints through serve's Printer, so every app shows and answers it the same way, and the unfinished_change refusal names it.
func UnfinishedCommand(a serve.App, open func() (*Scope, error), confirm func(*cobra.Command, ask.Action) error) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "unfinished",
		Short: "Show and clear changes cut short",
		Long: "A change of more than one write that was cut short, by a crash or a kill, holds\n" +
			"its keys until its user's next run on the same machine puts it back. Until\n" +
			"then another user's change to those keys is refused. `unfinished list` shows\n" +
			"each one: whose it is, the machine, its journal and the keys it holds.\n" +
			"`unfinished clear <thing>` removes the marks once you are sure the change was\n" +
			"put back or given up: it puts nothing back, and the journal stays.",
		Args: cobra.NoArgs,
	}
	cmd.AddCommand(listCommand(a, open), clearCommand(a, open, confirm))
	return cmd
}

func listCommand(a serve.App, open func() (*Scope, error)) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list [<thing>]",
		Short: "Show the changes cut short, every user's, and your journals left",
		Long: "Show every change cut short whose mark is in the scope, every user's, with your\n" +
			"journal's file when it is yours on this machine, and your journals whose marks\n" +
			"are gone (left): kept as the only copy of what was there. A thing shows only\n" +
			"the changes that hold it.",
		Args: cobra.MaximumNArgs(1),
	}
	out := a.Printer(cmd)
	cmd.RunE = func(c *cobra.Command, args []string) error {
		if err := out.Check(); err != nil {
			return err
		}
		var thing Key
		if len(args) == 1 {
			k, err := thingOf(args[0])
			if err != nil {
				return err
			}
			thing = k
		}
		s, err := open()
		if err != nil {
			return err
		}
		defer s.Close()
		changes, err := everyUnfinished(s, thing)
		if err != nil {
			return err
		}
		return out.Print(c.OutOrStdout(), "", UnfinishedListKind, UnfinishedData{Changes: changes}, func(w io.Writer) {
			if len(changes) == 0 {
				fmt.Fprintln(w, "no change is cut short")
				return
			}
			fmt.Fprintln(w, "MARK\tUSER\tMACHINE\tSTATE\tKEYS\tJOURNAL")
			for _, u := range changes {
				state, journal := "holds", u.Journal
				if u.Left {
					state = "left"
				}
				if u.File != "" {
					journal = u.File
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", u.ID, u.User, u.Host, state, keyList(u.Keys), journal)
			}
		})
	}
	return cmd
}

func clearCommand(a serve.App, open func() (*Scope, error), confirm func(*cobra.Command, ask.Action) error) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "clear <thing>",
		Short: "Remove the marks of the changes cut short that hold a thing",
		Long: "Remove the marks of every change cut short that holds thing, so changes to its\n" +
			"keys are made again. Only once you are sure the change was put back or given\n" +
			"up: it puts nothing back, and a journal stays where it is, yours shown as left\n" +
			"by `unfinished list`. It shows each change and asks first, as a change of type\n" +
			"delete.",
		Args: cobra.ExactArgs(1),
	}
	out := a.Printer(cmd)
	ask.Flag(cmd)
	cmd.RunE = func(c *cobra.Command, args []string) error {
		if err := out.Check(); err != nil {
			return err
		}
		thing, err := thingOf(args[0])
		if err != nil {
			return err
		}
		s, err := open()
		if err != nil {
			return err
		}
		defer s.Close()
		got, err := s.Unfinished(thing)
		if err != nil {
			return err
		}
		held := slices.DeleteFunc(got, func(u Unfinished) bool { return u.Left })
		if len(held) == 0 {
			return fail(serve.TypeNotFound, "", string(thing), fmt.Sprintf("no change cut short holds %s; `%s store unfinished list` shows them", thing, commandOf(a)))
		}
		if err := confirm(c, ask.Action{Type: ask.Delete, Name: string(thing), What: clearing(a, thing, held)}); err != nil {
			return err
		}
		cleared, err := s.Clear(c.Context(), thing)
		if err != nil {
			return err
		}
		for i := range cleared {
			cleared[i] = shown(cleared[i])
		}
		return out.Print(c.OutOrStdout(), "", ClearedKind, ClearedData{Thing: thing, Cleared: cleared}, func(w io.Writer) {
			for _, u := range cleared {
				fmt.Fprintf(w, "cleared the change %s made on %s (mark %s); nothing was put back\n", u.User, u.Host, u.ID)
				if u.File != "" {
					fmt.Fprintf(w, "your journal stays at %s, the only copy of what was there\n", u.File)
				} else {
					fmt.Fprintf(w, "its journal %s stays in %s's data folder on %s\n", u.Journal, u.User, u.Host)
				}
			}
		})
	}
	return cmd
}

// everyUnfinished is Unfinished of thing, or with no thing of the folder's own turn and of every thing in the scope, each change once.
func everyUnfinished(s *Scope, thing Key) ([]Unfinished, error) {
	things := []Key{thing}
	if thing == "" {
		keys, err := s.List("")
		if err != nil {
			return nil, err
		}
		for _, k := range keys {
			if t := k.thing(); !slices.Contains(things, t) {
				things = append(things, t)
			}
		}
	}
	var held, left []Unfinished
	seen := map[string]bool{}
	for _, t := range things {
		got, err := s.Unfinished(t)
		if err != nil {
			return nil, err
		}
		for _, u := range got {
			if id := fmt.Sprint(u.Left, u.ID, u.Journal); !seen[id] {
				seen[id] = true
				if u.Left {
					left = append(left, shown(u))
				} else {
					held = append(held, shown(u))
				}
			}
		}
	}
	return append(append([]Unfinished{}, held...), left...), nil
}

// shown is u as an answer gives it: keys a list, never null.
func shown(u Unfinished) Unfinished {
	if u.Keys == nil {
		u.Keys = []Key{}
	}
	return u
}

// clearing is what clear asks about: each change, whose it is, the keys it holds, and what its journal says it was doing when it is this user's on this machine.
func clearing(a serve.App, thing Key, held []Unfinished) string {
	var b strings.Builder
	fmt.Fprintf(&b, "clear %d change(s) cut short holding %s in %s; nothing is put back:\n", len(held), thing, commandOf(a))
	for _, u := range held {
		fmt.Fprintf(&b, "  %s on %s, mark %s, journal %s\n", u.User, u.Host, u.ID, u.Journal)
		fmt.Fprintf(&b, "    holds %s\n", keyList(u.Keys))
		if u.File == "" {
			fmt.Fprintf(&b, "    its journal is in %s's data folder on %s; that user's next run there puts it back\n", u.User, u.Host)
			continue
		}
		fmt.Fprintf(&b, "    your journal %s stays\n", u.File)
		for _, d := range doing(u.File) {
			fmt.Fprintf(&b, "    was to %s\n", d)
		}
	}
	return strings.TrimSuffix(b.String(), "\n")
}

// doing is what the journal at file says its change was making, one write a line; nothing when it cannot be read.
func doing(file string) []string {
	raw, err := os.ReadFile(file)
	if err != nil {
		return nil
	}
	_, lines, err := parseJournal(raw)
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		switch {
		case l.Dir && l.From != "":
			out = append(out, fmt.Sprintf("move the folder %s to %s", l.From, l.Key))
		case l.Dir && l.Gone:
			out = append(out, "take away the folder "+string(l.Key))
		case l.Dir:
			out = append(out, "make the folder "+string(l.Key))
		case l.Before == nil:
			out = append(out, "add "+string(l.Key))
		case l.After == nil:
			out = append(out, "remove "+string(l.Key))
		default:
			out = append(out, "rewrite "+string(l.Key))
		}
	}
	return out
}

// thingOf is s as a thing, a key's first step, the way the unfinished_change refusal names it.
func thingOf(s string) (Key, error) {
	k, err := ParseKey(s)
	if err != nil {
		return "", err
	}
	if k == "" || k.thing() != k {
		return "", fail(serve.TypeRefused, "", s, fmt.Sprintf("%q is not a thing: a thing is a key's first step, such as %s", s, k.thing()))
	}
	return k, nil
}

func keyList(keys []Key) string {
	out := make([]string, len(keys))
	for i, k := range keys {
		out[i] = string(k)
	}
	return strings.Join(out, ", ")
}

// commandOf is the app's CLI name, as serve names it.
func commandOf(a serve.App) string {
	if a.Command != "" {
		return a.Command
	}
	return a.ID
}
