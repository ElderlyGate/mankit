package serve

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/tugbayatilla/mankit/v4/ask"
	"github.com/tugbayatilla/mankit/v4/features"
	"github.com/tugbayatilla/mankit/v4/helpout"
	"github.com/tugbayatilla/mankit/v4/logs"
	"github.com/tugbayatilla/mankit/v4/problem"
)

// The kinds of the answers the commands print with -o json or yaml.
const (
	ServerKind     = "Server"
	ServerListKind = "ServerList"
	StoppedKind    = "StoppedList"
	LinkKind       = "Link"
)

// Commands are serve and the thing server for the app's root: open builds what serve serves, confirm (such as prefs.Asker.Ask) asks before a change.
func (a App) Commands(open func(*cobra.Command) (Site, error), confirm func(*cobra.Command, ask.Action) error) ([]*cobra.Command, error) {
	if err := a.check(); err != nil {
		return nil, err
	}
	if open == nil || confirm == nil {
		return nil, errors.New("serve: Commands needs open, which builds what serve serves, and confirm, which asks before server stop and server token renew")
	}
	return []*cobra.Command{a.serveCommand(open), a.serverCommand(confirm)}, nil
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
	return p.printWith(w, name, kind, data, nil, rows)
}

// printWith is print with the problems the answer carries beside its data, in the envelope's errors; a table leaves them to the caller.
func (p *printing) printWith(w io.Writer, name, kind string, data any, problems []error, rows func(io.Writer)) error {
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
	for _, err := range problems {
		env.Errors = append(env.Errors, problemOf(err))
	}
	if p.output == "json" {
		return Encode(w, env)
	}
	raw, err := json.Marshal(env)
	if err != nil {
		return err
	}
	// Through its JSON, so the YAML has the envelope its contract names.
	var doc any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return err
	}
	return helpout.Encode(w, "yaml", doc)
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
page opens from the link serve prints (` + "`server link`" + ` prints it again), which
leaves the token in a cookie. ` + "`server token renew`" + ` replaces the token without a
restart.

--addr binds this machine's own address only; another needs --allow-remote, as
every machine that reaches it can then try the token.

--detach returns to the prompt with the server still up, in its own session. It
waits until the server serves, so an address in use fails the command. ` + "`server list`" + `
lists the detached servers and ` + "`server stop`" + ` stops one.`,
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
		log, closeLog, err := a.logger(cmd, site, child, logPath)
		if err != nil {
			ln.Close()
			return err
		}
		defer closeLog()
		if site.Log == nil {
			site.Log = logs.Writer(log, slog.LevelError)
		}
		handler, err := a.Handler(site, hosts)
		if err != nil {
			ln.Close()
			return err
		}
		if err := a.logBroken(log, site.Features); err != nil {
			ln.Close()
			return err
		}
		// Every server leaves a record, in the foreground too (ADR-S12); a crash leaves it behind, which is why nothing believes one without asking its address.
		rec := record{
			APIVersion: recordAPIVersion(), Kind: recordKind,
			Metadata: recordMetadata{App: a.ID, Addr: bound, PID: os.Getpid()},
			Spec:     recordSpec{Name: site.Name, Version: a.Version, StartedAt: time.Now().UTC(), Log: logPath, Detached: child},
		}
		if err := a.save(dir, rec); err != nil {
			ln.Close()
			return err
		}
		// The record goes before the listener closes, so once its address refuses, the server writes nothing more: a server stop that saw it stop finds the record gone.
		var forget sync.Once
		drop := func() { forget.Do(func() { _ = a.removeRecord(dir, bound) }) }
		defer drop()
		log.Info("serving on "+bound, "url", "http://"+bound, "detached", child, "stop", a.command()+" server stop "+bound)
		// The link carries the token, so it goes only to a person at a terminal, never into a log.
		if w := cmd.OutOrStdout(); !child && logs.Terminal(w) {
			token, err := a.readToken(dir)
			if err != nil {
				ln.Close()
				return err
			}
			fmt.Fprintf(w, "open: %s\npress ctrl-c to stop\n", pageLink(bound, token))
		} else if !child {
			log.Info("`"+a.command()+" server link "+bound+"` prints the link that opens the page", "link", a.command()+" server link "+bound)
		}
		return run(cmd.Context(), &http.Server{Handler: handler, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 2 * time.Minute}, ln, drop)
	}
	c.Flags().StringVar(&addr, "addr", a.Addr, "address to bind (this machine's own address by default)")
	c.Flags().StringSliceVar(&hosts, "host", nil, "a name besides localhost this server answers to, such as a tunnel's host name")
	c.Flags().BoolVar(&allowRemote, "allow-remote", false, "let --addr be an address other machines reach; they still need the token")
	c.Flags().BoolVarP(&detach, "detach", "d", false, "run in the background and return to the prompt; see server list")
	return c
}

// logBroken says at start, one record each, which features are on but miss values, so the owner sees why they answer feature_needs_values.
func (a App) logBroken(log *slog.Logger, list *features.List) error {
	if list == nil {
		return nil
	}
	broken, err := list.Broken()
	if err != nil {
		return err
	}
	for _, s := range broken {
		missing := &features.NeedsValues{ID: s.ID, Missing: s.Missing}
		log.Warn("a feature is on but cannot run; the rest serves", "problem", problem.Fail(problem.TypeFailed, features.ReasonNeedsValues, missing.Error()))
	}
	return nil
}

// logger is where a server logs (G-STACK-8): its file in the logs folder when detached, else stdout; with the Site's Telemetry, every record is also sent there (ADR-E5). close flushes and closes both.
func (a App) logger(cmd *cobra.Command, site Site, child bool, logPath string) (*slog.Logger, func(), error) {
	var w io.Writer = cmd.OutOrStdout()
	var file *logs.File
	if child && logPath != "" {
		f, err := logs.Open(logPath)
		if err != nil {
			return nil, nil, fmt.Errorf("cannot open the log %s: %w", logPath, err)
		}
		file, w = f, f
		// What the server says before it logs, a crash say, follows the file across its rotations.
		if err := f.TakeStd(); err != nil {
			f.Close()
			return nil, nil, err
		}
	}
	ctx := context.Background()
	var exp slog.Handler
	stop := func(context.Context) error { return nil }
	if site.Telemetry != nil {
		var err error
		if exp, stop, err = site.Telemetry(ctx); err != nil {
			if file != nil {
				file.Close()
			}
			return nil, nil, err
		}
	}
	var more []slog.Handler
	if exp != nil {
		more = append(more, exp)
	}
	return logs.New(w, a.ID, a.Version, more...), func() {
		_ = stop(ctx)
		if file != nil {
			file.Close()
		}
	}, nil
}

// run serves on ln until ctx ends or a signal comes, then runs before and shuts down gracefully so no write is cut off.
func run(ctx context.Context, srv *http.Server, ln net.Listener, before func()) error {
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
		before()
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
		return fmt.Errorf("a server is already on %s (pid %d); `%s server stop %s` stops it", addr, held.Metadata.PID, a.command(), addr)
	}
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("cannot find this binary to start it again: %w", err)
	}
	wd, err := os.Getwd()
	if err != nil {
		return err
	}
	logPath, err := logs.Path(a.folder(), logName(addr))
	if err != nil {
		return err
	}
	rec, err := spawn(exe, childArgs(cmd), wd, dir, logPath)
	if err != nil {
		return err
	}
	// The link carries the token, so it goes only to a person at a terminal; elsewhere the table names the command that prints it.
	open := fmt.Sprintf("`%s server link %s` prints the link", a.command(), rec.Metadata.Addr)
	if logs.Terminal(cmd.OutOrStdout()) {
		token, err := a.readToken(dir)
		if err != nil {
			return err
		}
		open = pageLink(rec.Metadata.Addr, token)
	}
	return out.print(cmd.OutOrStdout(), rec.Spec.Name, ServerKind, rec, func(w io.Writer) {
		fmt.Fprintf(w, "%s serving on http://%s\n", a.command(), rec.Metadata.Addr)
		fmt.Fprintf(w, "open:\t%s\npid:\t%d\nlog:\t%s\nstop:\t%s server stop %s\n", open, rec.Metadata.PID, rec.Spec.Log, a.command(), rec.Metadata.Addr)
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

// serverCommand is the thing server: list, stop, link and token renew, each with its old form under servers for one release (G-STACK-5).
func (a App) serverCommand(confirm func(*cobra.Command, ask.Action) error) *cobra.Command {
	server := &cobra.Command{Use: "server", Short: "The servers of this app on this machine"}
	token := a.tokenCommand(confirm)
	server.AddCommand(
		helpout.Old(a.listCommand(), "servers"),
		helpout.Old(a.stopCommand(confirm), "servers stop"),
		helpout.Old(a.linkCommand(), "servers link"),
		helpout.Old(token, "servers token"),
	)
	return server
}

func (a App) listCommand() *cobra.Command {
	c := &cobra.Command{
		Use:   "list",
		Short: "List the servers on this machine",
		Long: `List every server of this app on this machine, in the foreground or detached
(serve --detach), and say which.

A record is a claim, not proof, so every one is asked as it is listed, and a
record whose address refuses a connection is removed.

--check, for a health check, fails unless at least one server is listed and every
one listed answers: the list is the same, and the answer's errors say why.

--check <addr> asks only the server at addr, at loopback when it is on every
address, and removes no record: for a container's health check, where the
records folder is shared with servers it cannot reach. It lists the records for
that address and fails unless the server answers.`,
		Args: func(cmd *cobra.Command, args []string) error {
			if err := cobra.MaximumNArgs(1)(cmd, args); err != nil {
				return err
			}
			if len(args) == 0 {
				return nil
			}
			if on, _ := cmd.Flags().GetBool("check"); !on {
				return fmt.Errorf("an address is taken only with --check, as in `%s server list --check %s`", a.command(), args[0])
			}
			if _, port, err := net.SplitHostPort(args[0]); err != nil || port == "" {
				return fmt.Errorf("%q is not an address: --check takes host:port, as in `%s server list --check 127.0.0.1:5173`", args[0], a.command())
			}
			return nil
		},
	}
	out := a.printing(c)
	var check bool
	c.Flags().BoolVar(&check, "check", false, "fail unless at least one server is listed and every one answers, or, with an address, unless that server answers")
	c.RunE = func(cmd *cobra.Command, args []string) error {
		if err := out.check(); err != nil {
			return err
		}
		dir, err := a.serversDir()
		if err != nil {
			return err
		}
		var live []status
		var problems []error
		if len(args) == 1 {
			var answering bool
			if live, answering, err = a.one(dir, args[0]); err != nil {
				return err
			}
			if !answering {
				problems = []error{problem.Fail(problem.TypeNotFound, ReasonNotAnswering, fmt.Sprintf("%s does not answer", args[0]))}
			}
		} else {
			if live, err = a.live(dir); err != nil {
				return err
			}
			if check {
				if failed := checked(live); failed != nil {
					problems = []error{failed}
				}
			}
		}
		if err := out.printWith(cmd.OutOrStdout(), "", ServerListKind, live, problems, func(w io.Writer) {
			if len(live) == 0 && len(args) == 1 {
				fmt.Fprintf(w, "no record names %s\n", args[0])
				return
			}
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
		}); err != nil || len(problems) == 0 {
			return err
		}
		if out.output == "table" {
			fmt.Fprintf(cmd.ErrOrStderr(), "%s server list --check: %v\n", a.command(), problems[0])
		}
		return helpout.Printed{Err: problems[0]}
	}
	return c
}

// checked is why server list --check fails on live: no server listed, or one that does not answer; nil when every listed one answers.
func checked(live []status) error {
	var silent []string
	for _, s := range live {
		if !s.Answering {
			silent = append(silent, s.Record.Metadata.Addr)
		}
	}
	switch {
	case len(live) == 0:
		return problem.Fail(problem.TypeNotFound, ReasonNoneListed, "no server is listed")
	case len(silent) > 0:
		return problem.Fail(problem.TypeNotFound, ReasonNotAnswering, fmt.Sprintf("%d of %d servers do not answer: %s", len(silent), len(live), strings.Join(silent, ", ")))
	}
	return nil
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
			return fmt.Errorf("no server is recorded; `%s server list` lists them", a.command())
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
takes it out of the address, so the page never holds it.

--base starts the link with another address than addr's, for a server that
people reach through a proxy or a host's own name, such as
https://board.example.com; the server answers to that name only when serve
was given it with --host.`,
		Args: cobra.MaximumNArgs(1),
	}
	out := a.printing(c)
	var base string
	c.Flags().StringVar(&base, "base", "", "the scheme and host the link starts with, such as https://board.example.com (default: http:// and addr)")
	c.RunE = func(cmd *cobra.Command, args []string) error {
		if err := out.check(); err != nil {
			return err
		}
		start := ""
		if base != "" {
			b, err := baseOf(base)
			if err != nil {
				return err
			}
			start = b
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
		if start != "" {
			link = linkAt(start, token)
		}
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
through the link ` + "`server link`" + ` prints.`,
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
page of the server on ` + a.Addr + ` with it; ` + "`server link <addr>`" + ` prints the link
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

// Printer prints a command's answer the way serve's own commands do: rows as a table, or the envelope with -o json or yaml, in the contract asked with --contract; another package's command, such as `queue show`, uses it.
type Printer struct{ p *printing }

// Printer gives c the -o and --contract flags and prints its answer.
func (a App) Printer(c *cobra.Command) Printer { return Printer{a.printing(c)} }

// Check refuses an unknown -o or a contract the app does not answer, before anything happens.
func (p Printer) Check() error { return p.p.check() }

// Print writes data as kind in the envelope for json and yaml, else as the table rows draws.
func (p Printer) Print(w io.Writer, name, kind string, data any, rows func(io.Writer)) error {
	return p.p.print(w, name, kind, data, rows)
}
