package serve

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"golang.org/x/term"
	"gopkg.in/yaml.v3"

	"github.com/tugbayatilla/mankit/v3/ask"
	"github.com/tugbayatilla/mankit/v3/features"
)

// The kinds of the answers the commands print with -o json or yaml.
const (
	ServerKind     = "Server"
	ServerListKind = "ServerList"
	StoppedKind    = "StoppedList"
	LinkKind       = "Link"
)

// Commands are serve and servers for the app's root: open builds what serve serves, confirm (such as prefs.Asker.Ask) asks before a change.
func (a App) Commands(open func(*cobra.Command) (Site, error), confirm func(*cobra.Command, ask.Action) error) ([]*cobra.Command, error) {
	if err := a.check(); err != nil {
		return nil, err
	}
	if open == nil || confirm == nil {
		return nil, errors.New("serve: Commands needs open, which builds what serve serves, and confirm, which asks before servers stop and servers token renew")
	}
	return []*cobra.Command{a.serveCommand(open), a.serversCommand(confirm)}, nil
}

// terminal reports whether w is a terminal, the only place the token link is printed.
func terminal(w io.Writer) bool {
	f, ok := w.(*os.File)
	return ok && term.IsTerminal(int(f.Fd()))
}

// printing are the flags of a command that prints an answer.
type printing struct {
	app      App
	output   string
	contract string
}

func (a App) printing(c *cobra.Command) *printing {
	p := &printing{app: a}
	c.Flags().StringVarP(&p.output, "output", "o", "table", "output format: table, json or yaml")
	c.Flags().StringVar(&p.contract, "contract", "", "the contract version to answer with -o json or yaml, such as v1")
	return p
}

// print writes data as kind in the envelope for json and yaml, else as the table rows draws.
func (p *printing) print(w io.Writer, name, kind string, data any, rows func(io.Writer)) error {
	if p.output == "table" {
		tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		rows(tw)
		return tw.Flush()
	}
	contract, err := p.app.Contract(p.contract)
	if err != nil {
		return err
	}
	env := p.app.Answer(name, contract, kind, data)
	if p.output == "json" {
		return Encode(w, env)
	}
	raw, err := json.Marshal(env)
	if err != nil {
		return err
	}
	var doc any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return err
	}
	enc := yaml.NewEncoder(w)
	enc.SetIndent(2)
	if err := enc.Encode(doc); err != nil {
		return err
	}
	return enc.Close()
}

// check refuses an unknown -o or a contract the app does not answer before anything happens.
func (p *printing) check() error {
	if !slices.Contains([]string{"table", "json", "yaml"}, p.output) {
		return fmt.Errorf("unknown output %q (want table, json or yaml)", p.output)
	}
	_, err := p.app.Contract(p.contract)
	return err
}

func (a App) serveCommand(open func(*cobra.Command) (Site, error)) *cobra.Command {
	var (
		addr        string
		hosts       []string
		allowRemote bool
		detach      bool
	)
	c := &cobra.Command{
		Use:   "serve",
		Short: "Serve the web page and the API on one port",
		Long: `Serve the web page and the HTTP API on one port, on this machine's own address.

It answers only to this machine's own address (--host names another), and every
API request carries this machine's token, as Authorization: Bearer <token>. The
page opens from the link serve prints (` + "`servers link`" + ` prints it again), which
leaves the token in a cookie. ` + "`servers token renew`" + ` replaces the token without a
restart.

--addr binds this machine's own address only; another needs --allow-remote, as
every machine that reaches it can then try the token.

--detach returns to the prompt with the server still up, in its own session. It
waits until the server serves, so an address in use fails the command. ` + "`servers`" + `
lists the detached servers and ` + "`servers stop`" + ` stops one.`,
		Args: cobra.NoArgs,
	}
	out := a.printing(c)
	c.RunE = func(cmd *cobra.Command, _ []string) error {
		if err := out.check(); err != nil {
			return err
		}
		if !allowRemote && !loopback(addr) {
			return fmt.Errorf("%s is reachable from other machines; serve binds this machine's own address only, unless --allow-remote", addr)
		}
		dir, err := a.serversDir()
		if err != nil {
			return err
		}
		child := os.Getenv(detachedEnv) != ""
		logPath := os.Getenv(logEnv)
		// Read once, so a server this child starts later is not taken for a detached child itself.
		os.Unsetenv(detachedEnv)
		os.Unsetenv(logEnv)
		if detach && !child {
			return a.detach(cmd, out, dir, addr)
		}
		site, err := open(cmd)
		if err != nil {
			return err
		}
		handler, err := a.Handler(site, hosts)
		if err != nil {
			return err
		}
		if err := a.logBroken(cmd.ErrOrStderr(), site.Features); err != nil {
			return err
		}
		ln, err := net.Listen("tcp", addr)
		if err != nil {
			return fmt.Errorf("cannot listen on %s: %w", addr, err)
		}
		bound := ln.Addr().String()
		if child && logPath != "" {
			// The log was named for the address asked for; port 0 binds another, and the log follows the address bound.
			if named := filepath.Join(filepath.Dir(logPath), logName(bound)); named != logPath && os.Rename(logPath, named) == nil {
				logPath = named
			}
		}
		// Every server leaves a record, in the foreground too (ADR-S12); a crash leaves it behind, which is why nothing believes one without asking its address.
		rec := record{
			APIVersion: recordAPIVersion(), Kind: recordKind,
			Metadata: recordMetadata{App: a.ID, Addr: bound, PID: os.Getpid()},
			Spec:     recordSpec{Name: site.Name, Version: a.Version, StartedAt: time.Now().UTC(), Log: logPath, Detached: child},
		}
		if err := rec.save(dir); err != nil {
			ln.Close()
			return err
		}
		defer func() { _ = removeRecord(dir, bound) }()
		w := cmd.OutOrStdout()
		fmt.Fprintf(w, "%s serving on http://%s\n", a.command(), bound)
		switch {
		case child:
			fmt.Fprintf(w, "detached; `%s servers stop %s` stops it\n", a.command(), bound)
		case terminal(w):
			token, err := a.readToken(dir)
			if err != nil {
				ln.Close()
				return err
			}
			fmt.Fprintf(w, "open: %s\npress ctrl-c to stop\n", pageLink(bound, token))
		default:
			// A running server's output may be its log, so the link goes there only when it is a terminal.
			fmt.Fprintf(w, "`%s servers link %s` prints the link that opens it\n", a.command(), bound)
		}
		return run(cmd.Context(), &http.Server{Handler: handler, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 2 * time.Minute}, ln)
	}
	c.Flags().StringVar(&addr, "addr", a.Addr, "address to bind (this machine's own address by default)")
	c.Flags().StringSliceVar(&hosts, "host", nil, "a name besides localhost this server answers to, such as a tunnel's host name")
	c.Flags().BoolVar(&allowRemote, "allow-remote", false, "let --addr be an address other machines reach; they still need the token")
	c.Flags().BoolVarP(&detach, "detach", "d", false, "run in the background and return to the prompt; see servers")
	return c
}

// logBroken says at start, one line each, which features are on but miss values, so the owner sees why they answer feature_needs_values.
func (a App) logBroken(w io.Writer, list *features.List) error {
	if list == nil {
		return nil
	}
	broken, err := list.Broken()
	if err != nil {
		return err
	}
	for _, s := range broken {
		fmt.Fprintf(w, "%s: %v; the rest serves\n", a.command(), &features.NeedsValues{ID: s.ID, Missing: s.Missing})
	}
	return nil
}

// run serves on ln until ctx ends or a signal comes, then shuts down gracefully so no write is cut off.
func run(ctx context.Context, srv *http.Server, ln net.Listener) error {
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	StopOnShutdown(srv)
	errCh := make(chan error, 1)
	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()
	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), shutdownFor)
		defer cancel()
		return srv.Shutdown(shutdown)
	}
}

// detach starts this binary again with the same command and the flags this process parsed, and reports the server once it serves.
func (a App) detach(cmd *cobra.Command, out *printing, dir, addr string) error {
	recs, err := readRecords(dir)
	if err != nil {
		return err
	}
	if held, err := a.find(recs, addr); err == nil && !a.probe(addr, probeWait).refused {
		return fmt.Errorf("a server is already on %s (pid %d); `%s servers stop %s` stops it", addr, held.Metadata.PID, a.command(), addr)
	}
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("cannot find this binary to start it again: %w", err)
	}
	wd, err := os.Getwd()
	if err != nil {
		return err
	}
	rec, err := spawn(exe, childArgs(cmd), wd, dir, addr)
	if err != nil {
		return err
	}
	token, err := a.readToken(dir)
	if err != nil {
		return err
	}
	link := pageLink(rec.Metadata.Addr, token)
	return out.print(cmd.OutOrStdout(), rec.Spec.Name, ServerKind, rec, func(w io.Writer) {
		fmt.Fprintf(w, "%s serving on http://%s\n", a.command(), rec.Metadata.Addr)
		fmt.Fprintf(w, "open:\t%s\npid:\t%d\nlog:\t%s\nstop:\t%s servers stop %s\n", link, rec.Metadata.PID, rec.Spec.Log, a.command(), rec.Metadata.Addr)
	})
}

// childArgs is cmd's path under the root and every flag set on it but --detach and the printing ones.
func childArgs(cmd *cobra.Command) []string {
	args := strings.Fields(cmd.CommandPath())[1:]
	cmd.Flags().Visit(func(f *pflag.Flag) {
		switch f.Name {
		case "detach", "output", "contract":
			return
		}
		if s, ok := f.Value.(pflag.SliceValue); ok {
			for _, v := range s.GetSlice() {
				args = append(args, "--"+f.Name+"="+v)
			}
			return
		}
		args = append(args, "--"+f.Name+"="+f.Value.String())
	})
	return args
}

func (a App) serversCommand(confirm func(*cobra.Command, ask.Action) error) *cobra.Command {
	c := &cobra.Command{
		Use:   "servers",
		Short: "List the servers on this machine",
		Long: `List every server of this app on this machine, in the foreground or detached
(serve --detach), and say which.

A record is a claim, not proof, so every one is asked as it is listed, and a
record whose address refuses a connection is removed.`,
		Args: cobra.NoArgs,
	}
	out := a.printing(c)
	c.RunE = func(cmd *cobra.Command, _ []string) error {
		if err := out.check(); err != nil {
			return err
		}
		dir, err := a.serversDir()
		if err != nil {
			return err
		}
		live, err := a.live(dir)
		if err != nil {
			return err
		}
		return out.print(cmd.OutOrStdout(), "", ServerListKind, live, func(w io.Writer) {
			if len(live) == 0 {
				fmt.Fprintf(w, "no server is running; `%s serve --detach` starts one\n", a.command())
				return
			}
			fmt.Fprintln(w, "ADDRESS\tNAME\tPID\tUP\tVERSION\tDETACHED\tANSWERING")
			for _, s := range live {
				r := s.Record
				fmt.Fprintf(w, "%s\t%s\t%d\t%s\t%s\t%t\t%t\n", r.Metadata.Addr, r.Spec.Name, r.Metadata.PID,
					time.Since(r.Spec.StartedAt).Round(time.Second), r.Spec.Version, r.Spec.Detached, s.Answering)
			}
		})
	}
	c.AddCommand(a.stopCommand(confirm), a.linkCommand(), a.tokenCommand(confirm))
	return c
}

func (a App) stopCommand(confirm func(*cobra.Command, ask.Action) error) *cobra.Command {
	var all bool
	c := &cobra.Command{
		Use:   "stop [addr|name]",
		Short: "Stop a server",
		Long: `Stop a server, detached or in the foreground, named by its address or by the
name of what it serves; --all stops every one.

It sends SIGTERM, which serve answers by shutting down gracefully, and never
escalates to a kill. A record whose server is already gone is removed and said
so. It asks first, as a change of type run.`,
		Args: cobra.MaximumNArgs(1),
	}
	out := a.printing(c)
	c.RunE = func(cmd *cobra.Command, args []string) error {
		if err := out.check(); err != nil {
			return err
		}
		if all == (len(args) == 1) {
			return errors.New("give an address or a name, or --all, not both")
		}
		dir, err := a.serversDir()
		if err != nil {
			return err
		}
		targets, err := readRecords(dir)
		if err != nil {
			return err
		}
		if !all {
			one, err := a.find(targets, args[0])
			if err != nil {
				return err
			}
			targets = []record{one}
		}
		if len(targets) == 0 {
			return fmt.Errorf("no server is recorded; `%s servers` lists them", a.command())
		}
		addrs := make([]string, 0, len(targets))
		for _, r := range targets {
			addrs = append(addrs, r.Metadata.Addr)
		}
		if err := confirm(cmd, ask.Action{Type: ask.Run, What: "stop the " + a.command() + " server at " + strings.Join(addrs, ", ")}); err != nil {
			return err
		}
		// Every target is tried even when one fails, so no one has to work out which are still up.
		results := []stopped{}
		var failures []error
		for _, r := range targets {
			res, err := a.stop(dir, r)
			if err != nil {
				failures = append(failures, err)
				continue
			}
			results = append(results, res)
		}
		if err := out.print(cmd.OutOrStdout(), "", StoppedKind, results, func(w io.Writer) {
			for _, res := range results {
				if res.Stale {
					fmt.Fprintf(w, "%s\twas not running; its record is removed\n", res.Addr)
					continue
				}
				fmt.Fprintf(w, "%s\tstopped (pid %d)\n", res.Addr, res.PID)
			}
		}); err != nil {
			return err
		}
		return errors.Join(failures...)
	}
	c.Flags().BoolVar(&all, "all", false, "stop every server")
	ask.Flag(c)
	return c
}

func (a App) linkCommand() *cobra.Command {
	c := &cobra.Command{
		Use:   "link [addr]",
		Short: "Print the link that opens a server's page",
		Long: `Print the link that opens the page of the server on addr (` + a.Addr + ` by default).

The link carries this machine's token once: the server swaps it for a cookie and
takes it out of the address, so the page never holds it.`,
		Args: cobra.MaximumNArgs(1),
	}
	out := a.printing(c)
	c.RunE = func(cmd *cobra.Command, args []string) error {
		if err := out.check(); err != nil {
			return err
		}
		dir, err := a.serversDir()
		if err != nil {
			return err
		}
		token, err := a.readToken(dir)
		if err != nil {
			return err
		}
		addr := a.Addr
		if len(args) == 1 {
			addr = args[0]
		}
		link := pageLink(addr, token)
		return out.print(cmd.OutOrStdout(), "", LinkKind, map[string]string{"url": link}, func(w io.Writer) { fmt.Fprintln(w, link) })
	}
	return c
}

func (a App) tokenCommand(confirm func(*cobra.Command, ask.Action) error) *cobra.Command {
	c := &cobra.Command{
		Use:   "token",
		Short: "The token every server of this app on this machine asks for",
		Long: `The token every server of this app on this machine asks for. It stays in this
app's data folder and is never printed alone: every page and client gets in
through the link ` + "`servers link`" + ` prints.`,
		Args: cobra.NoArgs,
	}
	c.AddCommand(a.renewCommand(confirm))
	return c
}

func (a App) renewCommand(confirm func(*cobra.Command, ask.Action) error) *cobra.Command {
	c := &cobra.Command{
		Use:   "renew",
		Short: "Replace the token with a new one, without restarting the servers",
		Long: `Replace this machine's token with a new one and print the link that opens the
page of the server on ` + a.Addr + ` with it; ` + "`servers link <addr>`" + ` prints the link
for another.

Every running server takes the new token on its next request and refuses the
old one from then on, so every open page and every client needs the new link.
It asks for the name first, as a change to the system itself; --yes skips the
question.`,
		Args: cobra.NoArgs,
	}
	out := a.printing(c)
	c.RunE = func(cmd *cobra.Command, _ []string) error {
		if err := out.check(); err != nil {
			return err
		}
		dir, err := a.serversDir()
		if err != nil {
			return err
		}
		if err := confirm(cmd, ask.Action{
			Type:   ask.Connect,
			What:   "renew the token of every " + a.command() + " server on this machine: every open page and client needs the new link",
			Name:   a.command(),
			System: true,
		}); err != nil {
			return err
		}
		token, err := a.renewToken(dir)
		if err != nil {
			return err
		}
		link := pageLink(a.Addr, token)
		return out.print(cmd.OutOrStdout(), "", LinkKind, map[string]string{"url": link}, func(w io.Writer) { fmt.Fprintln(w, link) })
	}
	ask.Flag(c)
	return c
}

// loopback reports whether addr binds this machine's own address only.
func loopback(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// Printer prints a command's answer the way serve's own commands do: rows as a table, or the envelope with -o json or yaml, in the contract asked with --contract; another package's command, such as `config queue`, uses it.
type Printer struct{ p *printing }

// Printer gives c the -o and --contract flags and prints its answer.
func (a App) Printer(c *cobra.Command) Printer { return Printer{a.printing(c)} }

// Check refuses an unknown -o or a contract the app does not answer, before anything happens.
func (p Printer) Check() error { return p.p.check() }

// Print writes data as kind in the envelope for json and yaml, else as the table rows draws.
func (p Printer) Print(w io.Writer, name, kind string, data any, rows func(io.Writer)) error {
	return p.p.print(w, name, kind, data, rows)
}
