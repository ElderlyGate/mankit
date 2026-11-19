package store

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/tugbayatilla/mankit/v3/ask"
	"github.com/tugbayatilla/mankit/v3/internal/disk"
	"github.com/tugbayatilla/mankit/v3/serve"
)

// The kinds of the answers store unfinished list and clear give, on the CLI and over HTTP alike (contracts mankit/v1 store-unfinished-list, store-unfinished-cleared).
const (
	UnfinishedListKind = "StoreUnfinishedList"
	ClearedKind        = "StoreUnfinishedCleared"
)

// UnfinishedData is the data of store unfinished list: every change cut short, those with marks first, then this user's left journals.
type UnfinishedData struct {
	Changes []Unfinished `json:"changes"`
}

// ClearedData is the data of store unfinished clear: the thing, and the changes whose marks were removed.
type ClearedData struct {
	Thing   Key          `json:"thing"`
	Cleared []Unfinished `json:"cleared"`
}

// askName asks for the thing's name before a clear, whatever style the app's config picks for delete: a clear may lose a cut-short change, so it is a change to the system itself (G-STACK-6), and no remembered answer silences it.
var askName = func(c *cobra.Command, a ask.Action) error {
	_, err := ask.Change(c, a, ask.Name)
	return err
}

// UnfinishedCommand is `unfinished`, which every app with a store scope mounts under its own store command, as it does config queue, and serves through UnfinishedRoutes:
// `list [<thing>]` shows the changes cut short in the scope open opens, and `clear <thing>` shows those holding thing and, once the person types its name, removes the marks it showed (G-STACK-13).
// It lives here, beside Unfinished and ClearShown, and prints through serve's Printer, so every app shows and answers it the same way, and the unfinished_change refusal names it.
func UnfinishedCommand(a serve.App, open func() (*Scope, error)) *cobra.Command {
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
	cmd.AddCommand(listCommand(a, open), clearCommand(a, open))
	return cmd
}

func listCommand(a serve.App, open func() (*Scope, error)) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list [<thing>]",
		Short: "Show the changes cut short, every user's, and your journals left",
		Long: "Show every change cut short whose mark is in the scope, every user's, with your\n" +
			"journal's file when it is yours on this machine, and your journals whose marks\n" +
			"are gone (left): kept as the only copy of what was there. A thing shows only\n" +
			"the changes that hold it. A change to a thing that is gone, whose mark is only\n" +
			"in that thing's turn, shows only with `list <thing>`: the refusal names it.",
		Args: cobra.MaximumNArgs(1),
	}
	out := a.Printer(cmd)
	cmd.RunE = func(c *cobra.Command, args []string) error {
		if err := out.Check(); err != nil {
			return err
		}
		thing := ""
		if len(args) == 1 {
			thing = args[0]
		}
		data, err := listed(open, thing)
		if err != nil {
			return err
		}
		return out.Print(c.OutOrStdout(), "", UnfinishedListKind, data, func(w io.Writer) {
			if len(data.Changes) == 0 {
				fmt.Fprintln(w, "no change is cut short")
				return
			}
			fmt.Fprintln(w, "MARK\tUSER\tMACHINE\tSTATE\tKEYS\tJOURNAL")
			for _, u := range data.Changes {
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

func clearCommand(a serve.App, open func() (*Scope, error)) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "clear <thing>",
		Short: "Remove the marks of the changes cut short that hold a thing",
		Long: "Remove the marks of every change cut short that holds thing, so changes to its\n" +
			"keys are made again. Only once you are sure the change was put back or given\n" +
			"up: it puts nothing back, and a journal stays where it is, yours shown as left\n" +
			"by `unfinished list`. It shows each change and asks you to type the thing's\n" +
			"name, whatever your config says for delete; only the marks it showed are\n" +
			"removed.",
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
		held, err := holding(s, thing)
		if err != nil {
			return err
		}
		if err := askName(c, clearAction(s, thing, held)); err != nil {
			return err
		}
		data, err := clearShown(c.Context(), s, thing, ids(held))
		if err != nil {
			return err
		}
		return out.Print(c.OutOrStdout(), "", ClearedKind, data, func(w io.Writer) {
			for _, u := range data.Cleared {
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

// listed is what store unfinished list answers, on the CLI and over HTTP: the changes cut short holding thing, or with no thing every one in the scope.
func listed(open func() (*Scope, error), arg string) (UnfinishedData, error) {
	var thing Key
	if arg != "" {
		k, err := thingOf(arg)
		if err != nil {
			return UnfinishedData{}, err
		}
		thing = k
	}
	s, err := open()
	if err != nil {
		return UnfinishedData{}, err
	}
	defer s.Close()
	changes, err := everyUnfinished(s, thing)
	return UnfinishedData{Changes: changes}, err
}

// holding is the changes whose marks hold thing, as clear shows them; none is not_found, so nothing is asked about and nothing cleared by a typo.
func holding(s *Scope, thing Key) ([]Unfinished, error) {
	got, err := s.Unfinished(thing)
	if err != nil {
		return nil, err
	}
	held := slices.DeleteFunc(got, func(u Unfinished) bool { return u.Left })
	if len(held) == 0 {
		return nil, fail(serve.TypeNotFound, "", string(thing), fmt.Sprintf("no change cut short holds %s; `%s store unfinished list` shows them", thing, s.app.command()))
	}
	for i := range held {
		held[i] = shown(held[i])
	}
	return held, nil
}

// clearShown removes the marks of ids that still hold thing, on the CLI and over HTTP; none left to remove is not_found: they were put back or cleared meanwhile.
func clearShown(ctx context.Context, s *Scope, thing Key, ids []string) (ClearedData, error) {
	cleared, err := s.ClearShown(ctx, thing, ids)
	if err != nil {
		return ClearedData{}, err
	}
	if len(cleared) == 0 {
		return ClearedData{}, fail(serve.TypeNotFound, "", string(thing), fmt.Sprintf("nothing was cleared: no change shown still holds %s, as it was put back or cleared meanwhile; `%s store unfinished list` shows what holds it now", thing, s.app.command()))
	}
	for i := range cleared {
		cleared[i] = shown(cleared[i])
	}
	return ClearedData{Thing: thing, Cleared: cleared}, nil
}

// clearAction is the question before a clear: a delete of the system itself, so it asks for the thing's name and no remembered answer silences it (G-STACK-6).
func clearAction(s *Scope, thing Key, held []Unfinished) ask.Action {
	return ask.Action{Type: ask.Delete, System: true, Name: string(thing), What: clearing(s, thing, held)}
}

// everyUnfinished is Unfinished of thing, or with no thing of the folder's own turn and of every thing: those in the scope, and those the folder's marks and this user's journals name, so a mark on a thing gone is found too. Each change once.
func everyUnfinished(s *Scope, thing Key) ([]Unfinished, error) {
	first, err := s.Unfinished(thing)
	if err != nil || thing != "" {
		return order(first), err
	}
	var things []Key
	add := func(k Key) {
		if t := k.thing(); !slices.Contains(things, t) {
			things = append(things, t)
		}
	}
	for _, u := range first {
		for _, k := range u.Keys {
			add(k)
		}
	}
	keys, err := s.List("")
	if err != nil {
		return nil, err
	}
	for _, k := range keys {
		add(k)
	}
	all := first
	for _, t := range things {
		got, err := s.Unfinished(t)
		if err != nil {
			return nil, err
		}
		all = append(all, got...)
	}
	return order(all), nil
}

// once tells one change from another: a mark, or a left journal, which may share its mark's id.
type once struct {
	left        bool
	id, journal string
}

// order is each change once, those with marks first, then the left journals.
func order(all []Unfinished) []Unfinished {
	var held, left []Unfinished
	seen := map[once]bool{}
	for _, u := range all {
		k := once{u.Left, u.ID, u.Journal}
		if seen[k] {
			continue
		}
		seen[k] = true
		if u.Left {
			left = append(left, shown(u))
		} else {
			held = append(held, shown(u))
		}
	}
	return append(append([]Unfinished{}, held...), left...)
}

// shown is u as an answer gives it: keys a list, never null.
func shown(u Unfinished) Unfinished {
	if u.Keys == nil {
		u.Keys = []Key{}
	}
	return u
}

func ids(held []Unfinished) []string {
	out := make([]string, len(held))
	for i, u := range held {
		out[i] = u.ID
	}
	return out
}

// clearing is what clear asks about: each change, whose it is, the keys it holds, and what its journal says it was doing when it is this user's on this machine.
func clearing(s *Scope, thing Key, held []Unfinished) string {
	var b strings.Builder
	fmt.Fprintf(&b, "clear %d change(s) cut short holding %s in %s; nothing is put back:\n", len(held), thing, s.app.command())
	for _, u := range held {
		fmt.Fprintf(&b, "  %s on %s, mark %s, journal %s\n", u.User, u.Host, u.ID, u.Journal)
		fmt.Fprintf(&b, "    holds %s\n", keyList(u.Keys))
		if u.File == "" {
			fmt.Fprintf(&b, "    its journal is in %s's data folder on %s; that user's next run there puts it back\n", u.User, u.Host)
			continue
		}
		fmt.Fprintf(&b, "    your journal %s stays\n", u.File)
		writes, err := doing(u.File)
		if err != nil {
			fmt.Fprintf(&b, "    what it was doing is not known: the journal %v\n", err)
		}
		for _, d := range writes {
			fmt.Fprintf(&b, "    was to %s\n", d)
		}
	}
	return strings.TrimSuffix(b.String(), "\n")
}

// doing is what the journal at file says its change was making, one write a line, read through the safety core as the store reads it.
func doing(file string) ([]string, error) {
	r, err := os.OpenRoot(filepath.Dir(file))
	if err != nil {
		return nil, fmt.Errorf("cannot be read: %v", err)
	}
	defer r.Close()
	raw, _, err := disk.Read(r, filepath.Base(file))
	if err != nil {
		return nil, fmt.Errorf("cannot be read: %v", err)
	}
	_, lines, err := parseJournal(raw)
	if err != nil {
		return nil, err
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
	return out, nil
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

// problemType is err's Problem type, empty for an error that is no Problem.
func problemType(err error) string {
	var p *serve.Problem
	if errors.As(err, &p) {
		return p.Type
	}
	return ""
}
