# mankit

What the `-man` dashboards share: [gitman](https://github.com/ElderlyGate/gitman),
[dcman](https://github.com/ElderlyGate/dcman) and
[checkman](https://github.com/ElderlyGate/checkman). A change here reaches all
three, instead of being made three times.

| Package | What it is |
|---|---|
| `desktop` | Copy to and paste from the clipboard (OSC 52, tmux, WSL `clip.exe`, system tools), open a URL in the browser, `IsWSL` |
| `textinput` | bubbles' text input, with paste through `desktop` |
| `selection` | Drag with the mouse to select text on the screen and copy it |
| `light` | The header's traffic light: 🟢 focused, 🟡 refreshing in the background, 🔴 not updating |
| `notice` | The status line's latest message, put first for a few seconds so "copied 2 lines" shows even over a search or a running action |
| `search` | Search the pane: every hit marked, `n`/`N` between them, the status line |
| `theme` | The shared colours: five palettes (`default`, `nord`, `gruvbox`, `solarized`, `high-contrast`), each with a dark- and a light-terminal variant, plus `mono`. `theme.Resolve(app, flag, set)` picks one from `--theme`, then `NO_COLOR`, then `theme:` and `background:` (auto, dark, light) in `<app folder>/config.yaml` (see [Where the files are](#where-the-files-are)). An app's theme embeds `theme.Base` and builds its own colours from the chosen `Colors` |
| `header` | Pieces of the top line: `Location` shortens a path from the left |
| `palette` | The command palette (`:` or `ctrl+p`) over the app's own command type, and the footer's favorites. `palette.Settings(rows)` makes a command of every setting (`setting:<key>`, labelled `key: value`, found by its help with `SettingHelp`); running one opens the Settings view on that row (`SettingKey`), and a setting is never starred: `Favorites.Toggle` answers `palette.ErrSetting` ("settings can't be starred"), a notice to show, not a failure |
| `confirm` | Asking before acting: `y` or `enter` says yes, any other key cancels |
| `list` | The left-hand list: the highlighted row wraps to show its whole text, with scrolling and mouse clicks that account for it |
| `split` | The border between the list and the pane: `[` widens the pane, `]` the list, `=` resets, or drag the border; the list keeps its width when the window grows, saved in `<app folder>/split` |
| `settings` | Every key of `config.yaml` to see and change: mankit's `theme`, `background` and `commands`, then the app's own. `settings/cli` adds `<app> config list` and `<app> config set <key> <value>` |
| `usercmd` | Your own commands from `<app folder>/config.yaml` (an old `commands.yaml` is moved in on first load): palette entry, optional key, a shell line with the selection filled in. `usercmd/cli` is `<app> command path`, `create`, `check` and `edit`. One schema for all apps: [`usercmd/commands.schema.json`](usercmd/commands.schema.json) |
| `helpout` | The one layout for `<app> help`, `<app> --help`, `<app> <thing> help` and a usage mistake: what the app is, how to start it, its commands, the repository; and the old forms of renamed commands, for one release |
| `build` | Whether the running binary is a release or a dev build: `build.Released()`, set only by the release tag stamped at build (see [Release or dev build](#release-or-dev-build)) |
| `home` | Where the org's apps keep their files: the org's config and data folders and a folder per product, `<product id>-dev` in a dev build; it names paths and writes nothing |
| `ask` | Asking before a CLI verb changes anything, in the style the user picked for that type of change (`name`, `yes-no`, `never`); refused without `--yes` when there is no terminal |
| `prefs` | What every app adds beside `settings/cli`: `<app> confirm list`, `<app> location list`, `config set confirm.<type>`, and `prefs.Asker`, which asks in the app's styles and saves a remembered answer |
| `serve` | The server every app shares: the token guard, the contract, answers in the contracts envelope, asking before a change over HTTP, and `serve [--detach]`, `server list`, `server stop`, `server link`, `server token renew` (see [Serve](#serve)) |
| `features` | An app's list of switchable features: each starts on or off, names the values it needs and has a tier, free or enterprise; the owner's switch first, then each closer handed in (public mode, a license); it knows no app (see [Features](#features)) |
| `licenses` | What unlocks an app's enterprise features: Ed25519-signed license files, checked offline against the keys the app's build trusts; the org license and the app's own; a closer for the feature list; `licenses/cli` is `<app> license show` and `license add [--org] <file>` (see [Licenses](#licenses)) |
| `events` | Live events: CloudEvents 1.0 checked against the app's catalog, sent on `GET /api/events` as Server-Sent Events through `serve`; a feature of the app's list, off means no stream (see [Events](#events)) |
| `store/queue` | Shows the change queue and sets its waits: the tickets in a line, `queue.wait` and `queue.limit`, `<app> queue show`, `set` and `unset`. The turns are the store's own (see [Change queue](#change-queue)) |
| `logs` | What every app logs: records in OpenTelemetry's log shape through `log/slog`, a detached server's rotated file in the logs folder, and the telemetry feature that sends them over OTLP (see [Logs](#logs)) |
| `problem` | A Problem (contracts problem/v1) and its types, below everything that makes one: the store and serve say what went wrong the same way |
| `store` | The one storage port (ADR-S22): a scope names its things by keys, reading never waits, every change takes the thing's turn and is made all or nothing, then read back; `store.Migrate` and `store.CopyOnce` bring old files along; `store/cli` is `<app> store unfinished`; `store/storetest` is the conformance suite every adapter passes (see [Store](#store)) |

## Settings

`config.yaml` is the one file of each app a person edits. `settings` lists its
keys, with the value in the file or the default, and changes one key at a
time: a value the key does not allow is refused, naming the allowed ones, and
an empty value puts the key back to its default. Only that key's line changes;
every other byte of the file stays as it was. A value it cannot change in place
(a block, a list, an anchor, a key written twice, a second document) is refused,
to be changed by hand.

Every write of mankit's own goes through the store (ADR-S22), like every app's:
`config.yaml` (`settings.Set`, `ask.Set`, `usercmd`), the split's width, the
palette's favorites, `store.Migrate`'s mark and `store.CopyOnce`, and serve's
records and token. A package handed a path opens its folder with
`store.Own(app, dir)`, which changes nothing it did not make: a folder that is
there keeps its mode and what is in it, one it makes is 0700, and each file it
writes is this user's alone. `store.EditFile(path, edit)` reads, changes and
writes one file in that folder's turn; called inside a change that already
holds that turn (an app's change of its config folder calling `settings.Set`),
it joins that change instead of waiting for it, and a folder another scope's
change holds is refused at once. `store.PutExec` puts a file that may be run,
so `store.CopyOnce` keeps a script runnable. A `config.yaml` that is a link, to a dotfiles repository say, is read as
it is but refused on a write (`refused`, `outside`): nothing follows a link
out, and the link is never replaced. Change the file it leads to instead.

```go
keys := settings.Keys("hubman", settings.Key{Name: "github", Default: "https", Values: []string{"https", "ssh"}, Help: "how to reach GitHub"})
cli.AddTo(root, keys, func() string { return settings.Path("hubman", "") })
```

```sh
hubman config list            # KEY VALUE SOURCE HELP; -o json or -o yaml
hubman config set theme nord
hubman config set theme ""   # back to the default
```

`settings.Path(app, dir)` is `<dir>/<app>/config.yaml`, the app folder
(`home.File(app, "config.yaml")`) when `dir` is empty; `theme.ResolveIn(path, flag, set)` reads the colours from
any path, and `usercmd/cli.CommandAt` serves `<app> command` for any path.
`cli.AddToE` reports a clash with an existing `config` command or `-o` flag
instead of working around it. `settings.Next` is the value after the current one, for a dashboard
that changes a key with `enter`.

## Where the files are

Every app of the org keeps its files in one place, the same on Linux and macOS
but for its logs (G-STACK-8):

| What | Where | `home` |
|---|---|---|
| the org's config folder | `$XDG_CONFIG_HOME/elderlygate`, else `~/.config/elderlygate` | `home.Dir()` |
| the org's own files (the org license and its queue) | the org's config folder; in a dev build `elderlygate-dev` beside it | `home.OrgDir()` |
| one app's config (the app folder) | `<config folder>/<product id>/`, `<product id>-dev/` in a dev build | `home.Product(id)` |
| the org's data folder | `$XDG_DATA_HOME/elderlygate`, else `~/.local/share/elderlygate` | `home.Data()` |
| one app's data | `<data folder>/<product id>/`, `<product id>-dev/` in a dev build | `home.ProductData(id)` |
| the org's logs folder | Linux: `$XDG_STATE_HOME/elderlygate`, else `~/.local/state/elderlygate`; macOS: `~/Library/Logs/elderlygate` | `home.Logs()` |
| one app's logs | `<logs folder>/<product id>/` | `home.ProductLogs(id)` |

The folder name is the one constant `home.Org`, so the org's rename changes one
line. A relative `XDG_*` value is ignored, as the XDG rules say.
`home.Folder(id)` is the name of an app's config and data folders: the product
ID in a release, `<product id>-dev` in a dev build (ADR-L5), so a dev build
never reads or writes a release's config and data. The org's own files, the
org license among them, are in `home.OrgDir()`: the org's config folder in a
release, `elderlygate-dev` beside it in a dev build. The logs folders are the
same in both.

`home.File(id, name)` is `<app folder>/<name>`, a path and nothing more: it
never copies or makes a file, so a read-only verb changes nothing.
`home.Config(id, dir)` is `<dir>/<home.Folder(id)>/config.yaml`, the app folder's
`config.yaml` when `dir` is empty.

`store.Migrate(id, names...)` brings each named file the app kept before in the
system's config folder (`~/.config/<app>/` on Linux,
`~/Library/Application Support/<app>/` on macOS) into the app folder. The app
calls it in one explicit step before its first write: in a verb that changes
something, or when its TUI starts; a read-only verb never calls it, so it
changes nothing. mankit's own files are
`config.yaml`, `split` and `favorites`, so gitman, dcman, checkman and hubman
keep their settings. The old file stays as it was, so an older build of the
app still finds it. Each copy runs once: when it is done, Migrate leaves a mark
in `<config folder>/.migrated/<home.Folder(app)>/`, so a file the user deletes afterwards
never comes back. A name with no old file, or a copy that fails, leaves no
mark and is tried again next time. Both live in `store`, since they write, and
`home` only names paths.

```go
if err := store.Migrate("gitman", "config.yaml", "split", "favorites"); err != nil {
	fmt.Fprintln(os.Stderr, "warning: old settings not copied:", err)
}
```

Until then a read finds the old file where it is: `home.Current(id, name)` is
`home.File(id, name)`, or the old file while the new one is missing, Migrate has
not copied it and the old one exists. It never touches a file, so a read-only
verb shows the value in use before the first change.

`store.CopyOnce(from, to)` does the same for anything else: when `to` is missing
and `from` exists, it copies the file or the whole folder, and leaves `from`
as it was. A folder appears whole or not at all, and a `to` another process
made while the copy ran is kept: the copy is placed only where nothing is.

```go
dir, _ := home.ProductData("control-room")
h, _ := os.UserHomeDir()
old := filepath.Join(h, ".local", "share", "control-room", "github.json")
if err := store.CopyOnce(old, filepath.Join(dir, "github.json")); err != nil {
	return err
}
```

## Help

Every app answers `<app> help` and `<app> --help` in one layout. The app says
what it is, how to start it and where its repository is; the commands and
their one-line help come from cobra, hidden and deprecated ones left out. A
`Flags:` section lists the root's own flags, when it has any. A thing, a
command with commands of its own (G-STACK-5), answers `<app> <thing> help`,
`<app> <thing> --help` and `<app> help <thing>` in the same layout: its own
short line first, then its verbs and its own things, its flags and `More:`.
`Execute` gives every thing its `help` verb. `<app> help <verb>` and
`<app> <verb> --help` keep cobra's full help.

`<app> help -o json` (or `-o yaml`) prints the same commands and flags as
data, with the hidden old forms marked old: contracts mankit/v1 `Help`.
`<app> help <thing> -o json` holds only that thing. It is the app's answer, in
the app's envelope, so the app hands helpout its printer:

```go
helpout.Apply(root, helpout.Info{
	What:   "a live visual board",
	Answer: func(c *cobra.Command) helpout.Answerer { return app.Printer(c) },
})
```

An app that sets no `Answer` refuses `help -o json` with a plain error, never
text.

```go
helpout.Apply(root, helpout.Info{
	What:  "a git dashboard for the terminal",
	Start: []string{"gitman", "run it inside a git repository"},
	More:  "https://github.com/ElderlyGate/gitman",
})
```

```
gitman: a git dashboard for the terminal

Start:
  gitman
  run it inside a git repository

Commands:
  help      Help about any command
  version   Print the version

Flags:
      --theme string   the colours: default, nord, gruvbox, solarized, high-contrast, mono

More: https://github.com/ElderlyGate/gitman
```

A usage mistake prints `error: <msg>`, then the layout, both to stderr, and
exits 2: an unknown flag, an unknown verb, `<app> help <unknown topic>` and a
wrong number of arguments. A mistake on a verb shows that verb's usage instead
of the layout. A command with verbs and no `Args`, the root or a group, takes
no other word (`<app> remote nope` is a mistake on `remote`); an app that wants
positional arguments sets `Args`. A command with verbs and no `Run` gets one
that shows its help, so its `PersistentPreRun` hooks run first. A run-time error (whatever a `RunE`
returns) never shows the layout: it comes back untouched, for the app to print
with its own message and exit code. `Execute` owns the root's flag error func
and silences cobra's own error and usage printing.

```go
code, err := helpout.Execute(ctx, root) // 0, 2 after a mistake, 1 with err
if err != nil {
	code = report(os.Stderr, err) // the app's own message and exit code
}
os.Exit(code)
```

### Old forms of renamed commands

A renamed command keeps its old form for one release (G-STACK-5). The app
marks the full command with its old words, after the app's name:

```go
helpout.Old(listCmd, "servers")       // server list
helpout.Old(stopCmd, "servers stop")  // server stop
```

An old form that stood for a verb with an argument names it:
`helpout.OldArgs(setCmd, "config queue wait", "wait")` runs `queue set wait`.
`Execute` makes each old form a hidden command: it is never listed in help, it
runs its full form with the rest of the line, and it says `<app>: servers stop
is now server stop` on stderr, so stdout is the same as the full form's. An old
form that is a command of its own is the app's mistake, and `Execute` refuses
to run. `helpout.OldForms(root)` lists them. `helptest.Check(t, newRoot)`
checks an app's old forms: each names a command, none is listed in help, and
each gives the same stdout as its full form (run with `--help`, so nothing is
done). Old forms, `helpout.Old` and `helptest` go at mankit's next major.

## Ask before a change

Reading never asks; a verb that changes something asks first and shows what it
will change. How it asks is the user's choice, per type of change.

Every change has one **type**: `create`, `edit`, `move`, `focus`, `archive`,
`close`, `delete`, `settings`, `connect` or `run` (`ask.Create` … `ask.Run`).
Each type has a **style**:

| Style | In a terminal |
|---|---|
| `name` | `Type <name> to go ahead (add ! to stop asking for <type>): `: the exact name says yes |
| `yes-no` | `Proceed? [y/N/a] `: `y` or `yes` says yes, `a` or `always` says yes and stop asking |
| `never` | no question |

The style lives in the app's own config only, as `confirm: {delete: name, move: never}`.
`ask.Load(path)` reads it, `ask.Set(path, type, style)` writes one type (`""`
puts it back to its default) and `ask.Remember(path, type)` saves `never` after
"Remember my answer"; each keeps the rest of the file. `<app> config set
confirm.delete never` does the same from the command line and refuses a style
that is not one of the three.

By default every type asks `yes-no`, `delete` included. A change to the system
itself (uninstalling, removing a token, dropping data, deleting a board) asks
for the name instead: the app marks those verbs with `System: true` on their
`ask.Action`, and the user can still pick another style for the type
(`ask.Default`): a stored `name` or `yes-no` holds for it. A `never` does not:
"Remember my answer" stops the type's everyday questions, but a change to the
system still asks for the name, and only `--yes` skips it. So its question
offers no way to stop asking: `Type <name> to go ahead: ` or `Proceed? [y/N] `,
and `<name>!` or `a` there says no.

`ask.Resolve(action, product)` gives the style in use for the action and where
it comes from, to show beside it: `product` or `default`. A settings page asks
it per type with `ask.Action{Type: t}`. `ask.ValidType`
and `ask.ParseStyle` check what a config holds, and `ask.Types` lists the
types for a settings page.

`ask.Flag` adds `--yes`, the same help text in every app. `ask.Change` does the
rest:

- with `--yes` the change goes ahead without a question;
- with no terminal on stdin and stderr (a script, a pipe, an agent) the change is
  refused, whatever the style: `refused: <what>; pass --yes to make the change`;
- otherwise it asks in the style. Any other answer, an empty line or the end
  of input says no: `refused: <what>`.

A refusal wraps `ask.ErrRefused`, and `helpout.Execute` exits 3 on it, so a
caller can tell a refusal from a failure (1). When the answer has `Remember`,
the user asked to stop asking for that type: the app saves `never` for it in
its own config. `ask.Change`
writes no file.

```go
archive := &cobra.Command{
	Use:  "archive <card>",
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		action := ask.Action{Type: ask.Archive, What: "archive card " + args[0], Name: args[0]}
		path := settings.Path("app", "")
		styles, err := ask.Load(path)
		if err != nil {
			return err
		}
		style, _ := ask.Resolve(action, styles)
		answer, err := ask.Change(cmd, action, style)
		if err != nil {
			return err
		}
		if answer.Remember {
			if err := ask.Remember(path, ask.Archive); err != nil {
				fmt.Fprintln(cmd.ErrOrStderr(), "warning: archive will still ask:", err)
			}
		}
		return board.Archive(args[0])
	},
}
ask.Flag(archive)
```

An empty or unknown style is `ask.Default(action)`, and `name` with no `Name`
asks yes-no.

```sh
app archive 7          # archive card 7
                       # Proceed? [y/N/a]
app archive 7 --yes    # no question
app archive 7 < /dev/null; echo $?   # refused: archive card 7; pass --yes to make the change
                                     # 3
```

### prefs: the same asking in every app

`prefs.Asker` is the example above, done once: it resolves the style from the
app's config, asks with `ask.Change`, and saves `never` when the user asks to
stop asking. `prefs.AddTo` (after `settings/cli`) adds `confirm list` (each
type's style and whether it is the app's or the default), `location list`
(the app's config, its data and the app's `Extra` places) and makes
`config set` ask first and take `confirm.<type>`.

Nothing is copied or written before the yes. Until the first change the styles
are read from `Read`, the old file in place (`home.Current`), so `confirm list`
shows the style in use; a no, or no terminal without `--yes`, writes nothing.
`Migrate` runs only after the yes, before the change is made.
`Options.System` names the types some verb of which changes the system
itself (`System: true`, such as `delete` for an uninstall): `confirm list` then
says how that change asks where the type's style does not hold for it, e.g.
`never — a change to the system itself still asks for the name`.

A command run with `--dry-run` asks nothing and writes nothing: no copy, no
mark, no remembered answer, and `config set` only says what it would change.

```go
read := func() string { p, _ := home.Current("gitman", "config.yaml"); return p }
asker := prefs.Asker{Config: configFile, Read: read, Migrate: migrate}
err := prefs.AddTo(root, prefs.Options{App: "gitman", Config: configFile, Types: confirmTypes, Asker: asker})
// in a verb:
if err := asker.Ask(cmd, ask.Action{Type: ask.Delete, What: "delete branch x", Name: "x"}); err != nil {
	return err
}
```

## Serve

`serve` is the HTTP door every app shares, so no app writes its own guard,
contract or server commands. The app gives its routes; serve adds the rest.

```go
app := serve.App{ID: "designer", Command: "e-designer", Version: version, Contracts: []serve.Contract{{Version: "v1"}}, Addr: "127.0.0.1:7480"}
open := func(cmd *cobra.Command) (serve.Site, error) {
	return serve.Site{
		Name: "shop",
		Routes: []serve.Route{
			{Pattern: "GET /api/blocks", Handler: listBlocks},
			{Pattern: "POST /api/blocks", Action: ask.Action{Type: ask.Create}, Handler: addBlock},
		},
		UI:     page,
		Styles:   func() (map[ask.Type]ask.Style, error) { return ask.Load(configPath) },
		Remember: func(t ask.Type) error { return ask.Remember(configPath, t) },
	}, nil
}
cmds, err := app.Commands(open, asker.Ask)
if err != nil {
	return err
}
root.AddCommand(cmds...)
```

A handler answers with `serve.Write(w, r, status, kind, data)` or
`serve.WriteError(w, r, status, err)`. Every answer is the contracts envelope:
`apiVersion` (`<org>/<app>/app/<contract>`), `kind`, `source` (where the answer
comes from; contracts envelope/v2 describes its fields), `data` and `errors`.
The CLI builds the same answer with `app.Answer` or `app.Failure` and writes it
with `serve.Encode`, so both doors give the same bytes.

Each error is a Problem (contracts `problem/v1`), from the package `problem`,
which sits below everything that makes one: `problem.Fail(type, reason,
detail)` makes one, with a few general types (`problem.TypeRefused`,
`TypeFailed`, `TypeNotFound`, `TypeUnsupportedContract` …) and a reason when a
program needs more (`serve.ReasonToken` …); a program decides by type and
reason, never by detail. An error that is not a `*problem.Problem` is type
`failed`. A failed answer's kind is `Error` in both envelopes.

A person's browser gets a refusal as a short readable page instead: what went
wrong (the Problem's detail), why, and what to do, for each reason a person
meets (a host the server does not answer to, the token, the origin, a contract
it does not serve, a body that is not JSON or too large, settings it cannot
read), with the same status. A browser is a navigation, or an Accept that
ranks `text/html` above `application/json`, as a browser's does; a program,
such as the app's own page fetching `/api/`, `curl`, a health check or the
CLI, asks with `*/*`, for JSON or with no Accept, and keeps the envelope on
every path. The page is built from the
Problem and the app's command alone, never from the request, so the token a
link carries never reaches it; it is not kept (`no-store`) and sends no
referrer. `serve.WriteError` gives it too.

Each of `App.Contracts` is a `serve.Contract{Version, Envelope}`, oldest
first, the last the default. Envelope is `v1` or `v2`; empty is `v1`. A
contract an app has released keeps its shape (contracts CT-RULE-1), so an app
moves to envelope/v2 with its next contract, such as
`[]serve.Contract{{Version: "v1"}, {Version: "v2", Envelope: "v2"}}`. A
contract on envelope/v1 is written as mankit v2 wrote it, byte for byte: `ran`,
and error/v1 items with the code each refusal had before Problems
(`token_refused` …) and `supported` for `unsupported_contract`. A reader
decoding `serve.Envelope` reads either. `Handler` and `Commands` refuse an
envelope serve does not write, a version given twice, no contract, and an
envelope going back, since a reader moving forward never meets an older one.
When envelope/v3 comes, it is a new Envelope value, never a new field.

One server may serve many things, such as one per project: it names each
answer's thing with `serve.WriteNamed` / `serve.WriteErrorNamed`; an empty name,
`Write`, `WriteError` and every refusal serve gives itself use `Site.Name`.
Contracts `envelope/v2` says what `source` and each of its fields are for. The
server's record names the server, `Site.Name`, never each thing it serves.

What serve does for every app:

- answers only on this machine's own address, or a name given with `--host`;
- opens the page only from its link (`?token=`), which it swaps for the cookie
  `<app>-token-<port>` (Strict, HttpOnly); an API request carries the token as
  `Authorization: Bearer <token>` or in that cookie, comes from the page itself,
  and sends JSON; a refusal (`refused` with reason `host`, `origin`, `token` or
  `not_json`) is in the contract the reader asked for when the app answers it;
- answers the contract asked for in `Accept-Contract`, else 406
  `unsupported_contract`; `GET /api/version` is never refused a contract: it is
  the one version answer (contracts `version/v1`, on envelope/v1 for good),
  listing the contracts the app answers in `data.supportedContracts`, in the
  contract asked for when served, else the newest;
- answers a change (POST, PUT, PATCH, DELETE) that asks with 428 and a
  Question answer (contracts `question/v1`, its wrapper in the asked
  contract), unless the user picked `never` for its type. The Question holds a
  one-time id bound to the request (method, path, query, body and type), the
  change's type, whether it changes the system itself, how it asks
  (`type_name` or `yes_no`) and what will change. A person's yes on the page
  sends the same request with `confirm=<id>`: it runs once, within five
  minutes, and any other id asks again (`ask.Questions` keeps them, one per
  server). `confirm=<id>&remember=yes` also stops asking for the type
  (`Site.Remember`). A program's yes in one call is `confirm=yes` with its token
  in `Authorization: Bearer`, as a script or `curl` sends it, HTTP's `--yes`.
  From the page's cookie, `confirm=yes` and `confirm=remember` are old forms,
  still taken in v4 and gone at the next major once every page sends the id.
  The guard already refuses a request from another page (`Origin`,
  `Sec-Fetch-Site`); the id is defence in depth, and makes a person's yes count
  for exactly the change they were shown. Every change route names its action type; `Handler`
  refuses one that does not. `Route.ActionFor`, when set, says what one
  request changes and whether it asks at all: a request that only follows a
  yes already given, rehearses, or changes nothing the person is asked about
  goes through unasked, and one may ask as the type it says. ActionFor reads a
  copy of the body, read once within the size cap (413 `refused`, reason
  `too_large`, past it), so the handler still gets it whole; a request that
  does not ask remembers nothing; an ActionFor that
  fails is logged and answered 500 `failed`. A change to the system itself
  always asks. The question is for the person, never a guard:
  the token is the guard.
- reaches a switchable module only through `Site.Features`: the `serve.Part`
  attached to a feature adds its routes, answered 404 `not_found` while the
  feature is off and 503 `failed`, reason `feature_needs_values`, while it is on but misses a
  value it needs, and hears of each change answered well (`Route.Changes` says
  the thing it changes), or that it missed one while off or broken, so no
  handler calls the module (see [Events](#events)). `serve` writes one line
  at start for each broken feature, naming each missing value and its purpose;
- refuses to start, with `failed`, reason `core_needs_values`, naming each missing value and its
  purpose, while a value the app's core cannot run without is not set
  (`Site.Needs`, found by `Site.Lookup`): a core part is not a feature and has
  no switch.

Each error has a code (`serve.Code…`); a program decides by the code, never by
the message. `<app> serve --help` and `<app> server help` are the reference
for the commands. Every server, detached or in the foreground, leaves a record
in `<data folder>/<folder>/servers/`, in the shape of contracts `mankit/v1`;
`server list` lists them and says which is detached. The link that carries the
token is printed only when asked for: by `serve` (to a terminal only, since a
running server's output may be its log), `serve --detach`, `server link` and
`server token renew`; never into a server's log.

A server logs records (see [Logs](#logs)): to stdout in the foreground, and
detached to `<logs folder>/<folder>/<address>.log`, which its record names.

`<folder>` is `App.Folder`, else `App.ID`: an app whose folders are named apart
from its ID until the rename (e-designer answers as `designer`) sets it, and the
ID still names its answers, cookie and command.

The token is the machine's, one per app, in `<data folder>/<folder>/servers/token`
(only the user's, 0600), and it stays there: `Handler` reads it itself, an app
never holds or stores one, and every page and client gets in through the link
`server link` prints. `server token renew` replaces it whole; every running
server checks the file on each request and refuses the old token from the next
one on, with no restart. A token file that cannot be read, is empty, or is open
to other users lets nobody in.

The tests check the real answers against the schemas in
[contracts](https://github.com/ElderlyGate/contracts).

## Logs

`logs` is what every app logs: records in OpenTelemetry's log data model, one
JSON object a line, through `log/slog` (`logs.New(w, id, version)`):
`timestamp`, `severityText` and `severityNumber` (DEBUG 5, INFO 9, WARN 13,
ERROR 17), `body`, `attributes` (flat, a group's keys joined by dots) and
`resource` (`service.name`, `service.version`). A Problem in a record is an
attribute in its own shape (contracts problem/v1), so a reader of the log
decides by its type and reason as a reader of an answer does.

- A foreground `serve` logs to stdout, every other command to stderr (the
  store's and the queue's notices are records there unless `store.App.Log`
  says otherwise), and a detached server to its file.
- A person at a terminal reads each record as one plain line,
  `designer: serving on 127.0.0.1:5174`, warnings and errors marked and a
  Problem's detail after it; JSON lines go to anything else: a pipe, a file,
  a container's log.
- `logs.Open(path)` is a log file, this user's alone, rotated at 10 MB with
  the five before it kept as `<name>.1` (the newest) to `<name>.5` (G-STACK-8).
  Logs are the one thing an app writes outside the store (ADR-S22).
- `logs.Writer(logger, level)` logs each line written to it, for what reports
  through a writer, such as `serve.Site.Log`.
- `logs.Telemetry` is the telemetry feature for the app's list (ADR-E5, E9):
  off by default; on, every record is also sent over OTLP to
  `OTEL_EXPORTER_OTLP_ENDPOINT` with the other standard `OTEL_*` variables.
  Sending lives in `logs/otlp`, the one package that links OpenTelemetry's
  modules: an app that offers telemetry sets
  `serve.Site.Telemetry = otlp.For(list, id, version)`, and an app that does
  not links none of them.
  On without the endpoint sends nothing, and the list says so as
  `feature_needs_values`, which the app's License and features section shows
  and the server logs at start.

## Features

A part of an app that can be switched off lives in one module and is reached
only through the app's feature list. `features` is that list; the
app hands in its features, where the owner's switches are kept and how a value
is found. A part the app cannot run without is not a feature and is not on the
list.

```go
list := features.New(
	[]features.Feature{{ID: "telemetry", Title: "Telemetry", Covers: "traces", StartsOff: true,
		Needs: []features.Value{{Name: "OTEL_EXPORTER_OTLP_ENDPOINT", Purpose: "where traces go"}}}},
	features.Store{Off: readOff, Keep: keepOff, On: readOn},
	features.Closer{By: "public", Closed: publicClosed},
).WithLookup(func(name string) bool { return os.Getenv(name) != "" })
states, err := list.States() // each feature on, off and what keeps it off, or on and broken
err = list.Set("telemetry", true)
broken, err := list.Broken() // for the start log
```

A feature is on or off by its own switch, never because a value is set. It
starts on unless it says `StartsOff`. A feature that starts off is on only
while the owner's on is kept, so a list holding one needs `Store.On`, and its
`Keep(id, false)` keeps the id there as well as taking it out of `Off`;
`Keep(id, true)` takes it out of `On`. Off by the owner's switch is named
`features.Switch` in `State.By`, off by its start `features.Default`, else the
first closer that closes it. A closer only closes: switching a feature back on
never opens what another closer closed. A list with a feature that needs values
needs `WithLookup`, and one with a feature that starts off needs `Store.On`;
`Attach` refuses a list without them, so a wrong wiring shows at start.
Each feature has a `Tier`, `features.Free` (the zero value) or
`features.Enterprise`, answered as `tier` (contracts mankit/v1
`featureState.tier` says what each means). Only an enterprise feature can be
locked (ADR-L2): a closer `By: features.License` never closes a free one. A
tier the list does not know is a wrong wiring too. `events.Feature` is
enterprise (ADR-E9).
`WithLookup` and `Attach` come before serving; the store's funcs, the lookup
and the closers are called on every request and must be safe for concurrent
use.

Switching a feature on without every value it needs is refused with
`features.NeedsValues`, reason `feature_needs_values`, naming each missing value
and its purpose; nothing is kept. A feature that is on but finds a value gone
is broken by itself: `State.Missing` names the values, `On` answers
`NeedsValues`, `Broken` lists it for the app to log at start, and the rest of
the app runs. A program acts on the reason `feature_needs_values` and reads the
missing names from the feature listing (`missing`), never from the message,
which is for people. `Set` refuses an id the list does not hold, naming the ones it
does, and a store that cannot be read is an error, never an empty list.

## Release or dev build

A goreleaser release from a tag is a release; every other build is dev
(ADR-L5). `build.Released()` says which, and nothing at run time can change
it: no flag, setting or environment variable. Each app stamps its release tag
at build, beside its own version stamp:

```yaml
ldflags:
  - -X github.com/tugbayatilla/mankit/v4/build.version=v{{.Version}}
```

Only a stamp of the form `vX.Y.Z` makes a release. A build without it, such as
`go build`, `go install` or `go test`, is dev, so an app that moves to this
mankit adds the line in the same change, or its releases keep their files in
the `-dev` folders. A dev build keeps its config and data in
`<product id>-dev/` (`home.Folder`), the org license in `elderlygate-dev/`
(`home.OrgDir`), and takes licenses signed by a dev key only.

## Licenses

An enterprise feature (`features.Enterprise`) is locked until a license opens
it (ADR-L1, L2). `licenses` checks them for every app; nw-license signs them.

```go
c, err := licenses.New(licenses.Config{ID: "designer", Trust: licenses.Trust{
	Release: build.Released(), // ADR-L5
	Prod:    map[string]ed25519.PublicKey{"prod-2026": prodKey},
	Dev:     map[string]ed25519.PublicKey{"dev-2026": devKey},
}})
list := features.New(all, store, c.Closer(all))
root.AddCommand(cli.Command(app, c, list, asker.Ask))
```

- A license file is a Resource (ADR-S15) whose spec is the payload's bytes as
  they were signed and the Ed25519 signature over them. Its one Go type is
  `licenses.License` (`ID`, `Kind`, `For`, `Products`, `Issued`, `Until`,
  `KeyID`); what each field means is in contracts mankit/v1 `license-file` and
  `license-payload`. `licenses.Sign(l, signer)` writes one,
  `licenses.Verify(raw, trust)` reads one.
- `License.Check` refuses what `Sign` refuses, without a signer; see its
  [Go doc](https://pkg.go.dev/github.com/tugbayatilla/mankit/v4/licenses#License.Check).
- A release takes licenses signed by a prod key only, any other build by a dev
  key only. A file it cannot read or verify is refused with its file and a
  Problem reason (`licenses.Reason…`, meanings in contracts problem/v1), never
  taken for no license. A link is followed, as fleet puts the files in.
- `Checker.Read` reads the org's `<home.OrgDir()>/license` and the app's
  `<app folder>/license` anew on every call. `Found.Has` and `Found.From` say
  whether and from where a feature is open; `Checker.Closer` is the license
  closer of the feature list. `Taken.DaysLeft` and `Ended` are all it says
  about time; when to warn is a threshold's (ADR-S23).
- `<app> license show` answers contracts mankit/v1 `LicenseShow`
  (`Checker.Show`, the same for a Settings page). `<app> license add <file>`
  keeps a product license or trial key for the app, `--org` an org license for
  every app: it checks the file, asks, naming the license it replaces, and
  writes it whole through the store.

## Events

`events` tells the app's page, and any other reader, what changed as it
happens. Every event is a CloudEvent 1.0 in structured JSON, in the shape of
contracts `event/v1`. It is a feature of its own, reached only through the
app's feature list: no handler sends an event, and the routes say nothing about
events. A change route says the thing it changes (`serve.Change`); serve tells
the module attached to the list of each change answered well with
`serve.Write`, and events sends the event its config names for that route.

```go
list := features.New([]features.Feature{events.Feature}, store) // store keeps On: events starts off
err := events.Attach(list, events.Config{
	Product: "designer",
	Thing:   "shop", // the source is /designer/shop
	Catalog: []string{"designer.resource.applied"},
	Causes:  map[string]events.Cause{"POST /api/resources": {Type: "designer.resource.applied"}},
})
site := serve.Site{Features: list, Routes: []serve.Route{{
	Pattern: "POST /api/resources",
	Action:  ask.Action{Type: ask.Create},
	Changes: serve.Change{Thing: "resource", ID: func(data any) string { return data.(Resource).ID }},
	Handler: apply,
}}}
```

One server serving many things names each event's thing in its source, as
`source.name` does for answers (ADR-S08): `Config.ThingFor` reads it from the
change's routed request, such as `func(r *http.Request) string { return
r.PathValue("project") }`, and the source is `/designer/<project>`. Nil, or
an empty answer, keeps `Thing`. A thing longer than 64 characters, made only of
dots, or one that would break the event names keeps `Thing` too, and so does a
ThingFor that panics; each such answer is said in the log once, cut to 64
characters. ThingFor runs before an event is built, never under its lock.

A change no route answers, such as a file another program changed, comes from
`Config.Source`: a function the broker runs while at least one stream is open
and stops, by its context, when the last one ends or the feature goes off, so
an app looks for changes only while somebody listens. It gives each change it
finds to `send` as an `events.Found` (type, thing, subject, data) and never
writes to a stream itself: the broker checks the type against the catalog,
names the thing in the source as ThingFor does, numbers the event, keeps it
for `Last-Event-ID` and sends it, with a trace of its own. A type outside the
catalog, or a thing that would break the event names, is said once in the log
and not sent. The Source must return promptly once its context ends; runs never
overlap, and a send from a stopped run is dropped. What changes while nobody
listens is not looked for: when the last stream ends the broker starts a new
run of event ids, so a stream that comes back gets the gapped event and reads
everything again. A Source that panics is said once and starts again with the
next stream.

```go
Source: func(ctx context.Context, send func(events.Found)) {
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			for _, c := range changedSinceLastLook() {
				send(events.Found{Type: "designer.board.changed", Thing: c.Project, Subject: c.Design, Data: c})
			}
		}
	}
},
```

Events is an enterprise feature and starts off (ADR-E9): `GET /api/events`
answers 404 until the owner switches it on. Taking events out is taking out
`events.Feature` and the `Attach` call: the routes stay as they are, and
`GET /api/events` answers 404.

- `type` is `<product>.<thing>.<verb>`, the verb in the past. `Attach` refuses
  a type of another product and a cause not in the catalog
  (`events.ErrNotInCatalog`); serve refuses at start a cause whose route does
  not exist or does not change the thing its type names. The subject is the
  route's `Changes.ID`; `Cause.Data` shapes the event's data from the answer's,
  and without it the data is the answer's.
- Every event carries `traceparent`: the request's `traceparent` and
  `tracestate` headers, or a new trace when there is none or it is not valid.
- `GET /api/events` is `text/event-stream`: `id:` is the event's id and `data:`
  the event. An idle stream gets a `: heartbeat` comment. A stream that comes
  back with `Last-Event-ID` gets the events after that one, from the last
  `Keep` kept in memory. At most `Streams` (64) are open at once; one more
  gets 503 `refused`, reason `too_many_streams`. A stream that cannot keep up is ended and comes
  back with `Last-Event-ID`.
- A client that missed events gets `<product>.stream.gapped`, subject
  `stream`, with data `{"lastKnown": "<id>", "oldestKept": "<id>"}` (contracts
  `mankit/v1`), and reloads its state. A stream gets it first when what came
  after its `Last-Event-ID` is no longer kept: too old, from another server, or
  from before the feature was off (`lastKnown` is empty when the id is not
  this server's, `oldestKept` left out when nothing is kept); coming back with
  the gap's id is no gap. An event that cannot be built after a change is said
  on stderr (its type and route, never its data) and the gapped event takes
  its place. Each app lists `<product>.stream.gapped` in its AsyncAPI file.
- serve's guard stands in front of the stream: no stream without the token.
  The browser's `EventSource` sends the page's cookie.
- With the feature off, `GET /api/events` answers 404 `not_found` and no event
  is built or kept. Switching it off with `List.Set` ends open streams at once;
  another closer, such as a license, ends them within a heartbeat.
- Streams end when the client leaves or the server stops (`serve.Stopping`), so
  no stream holds the server open. An app that runs its own `http.Server`
  calls `serve.StopOnShutdown(srv)` once before serving.

## Change queue

The change queue keeps every change to one served thing in one line (ADR-S14), so CLI
runs, servers, page clicks and other apps never change it at once. The line is
kept in `<served folder>/<app folder>/.queue/`, which only this user may enter
and which ignores itself in git. It holds tickets, never commands: each
process runs its own change, and nothing in the folder is ever executed. The
rules every app sharing a queue follows on disk are fixed in contracts
`mankit/v1`. One process may hold the queues of many served things at once:
each folder has its own line, so a change in one never waits for another.

The queue is the store's own (ADR-S22): a change takes its turn through
`store.Scope.Change`, and no app takes a turn itself. The package `store/queue`
shows the line and sets its waits; the turns live in `store/internal/queue`,
which Go keeps every package outside the store from.

```go
rowWait, rowLimit, err := queue.Waits(configPath) // the app's rows queue.wait and queue.limit
if err != nil {
	return err
}
wait, limit, err := queue.Settings(rowWait, rowLimit)
if err != nil {
	return err
}
s, err := store.Served(store.App{ID: "designer", Version: version, Wait: wait, Limit: limit}, root, line)
```

Every app adds `queue.WaitKey` and `queue.LimitKey` to its settings keys, so a
user sets `queue.wait` (30s) and `queue.limit` (2m) the same way in each.
`queue.Settings` refuses a value that is not a duration of at least a second in whole
seconds, naming the row; `queue.Duration` is that one rule, the one contracts
mankit/v1 queue-settings states, and `d.String()` (`45s`, `2m0s`) is the form
written and shown. Each bounds only this process's own waiting and holding, so apps
sharing a queue may set them apart. `queue.Waits(path)` reads the two rows from
the app's config.yaml for `queue.Settings`.

`queue.Command(app, path, confirm)` is the thing `<app> queue`, which each app
mounts at its root: `queue show` shows both rows with the value in effect, the
default and whether the config sets it (`-o json` answers contracts mankit/v1
`QueueSettings`), `queue set wait 45s` or `queue set limit 3m` sets one after
the app's confirm asks, written as `d.String()` gives it, and `queue unset wait`
puts the default back. An argument the queue would refuse is refused with
ADR-S21's reasons (`refused` with `wrong_type`, `out_of_range`, `empty` or
`unknown_key`), and a value edited by hand into the file is reported as
`config_unreadable`. An app with a queue of its own adds its verbs, such as
`list`, to the thing. It prints through
`serve.App.Printer`, which another package's command uses to answer as serve's
own commands do.

An app passes `Change` a context that ends on Ctrl-C (`signal.NotifyContext`), so the
ticket leaves the line before the process does.

- A change writes a ticket at the end of the line, waits until it is first and
  holds the turn, makes its writes, then reads them back: it is done only once
  read back. The ticket leaves the line whether the change ran or not. Past
  `Limit` (2 minutes) the change stops before its next write and fails with
  `change_too_long`; a change that was made and read back is done, even past
  the limit.
- The turn is a lock on a file (`github.com/gofrs/flock`), so it holds across
  processes; within one process a lock in memory keeps changes apart as well.
  Locks sit on lock files beside what others read (`<id>.lock` beside each
  ticket, `turn.lock` beside `turn`), since a lock on Windows keeps others from
  reading what it covers. A ticket's lock file stays locked by its process
  while it waits and runs: when the process dies
  the system gives the lock back, and the next reader removes the ticket and
  says so. A ticket whose lock is held but whose file was not touched for 30s
  (touched every 5s), or one that cannot be read or understood, is moved to
  `.queue/bad/`, said on stderr with the file and why, and skipped. A change
  still running when its ticket is set aside keeps the turn.
- `.queue/backend`, when present, names how every writer of the folder takes
  turns (contracts `mankit/v1/queue-backend`): `files`, as above, is the only
  one this build runs, and the one without a marker. A marker naming another
  backend, such as `lock-service`, or one that cannot be read, makes a change
  and `queue.List` refuse with `queue_backend_unsupported`; they never fall back to
  files. The marker never holds an address or a token.
- Errors are Problems of type `failed`, with a reason (`queue.Reason…`): `queue_timeout` when the turn did not
  come within `Wait` (30s), `not_read_back` when the change ran but could not
  be read back, `ticket_lost` when another process set the waiting ticket
  aside, `queue_unusable` when the folder cannot be made, read or written,
  `queue_backend_unsupported` for a marker this build does not run,
  `change_too_long` past `Limit`. `queue_timeout` names the change that
  holds the turn: its ticket, pid, host and how long it has held it.
- `queue.List(ctx, folder, app, version)` is what `<app> queue` shows: the tickets in order, which one's
  change runs now and since when, and the files in `bad/`. Answered as kind
  `queue.ListKind` it meets contracts `mankit/v1/queue-list`; every ticket meets
  `mankit/v1/queue-ticket` (apiVersion `elderlygate/mankit/queue/v1`, kind `QueueTicket`, whose header
  never changes, ADR-S15).

What it holds for:

- Local disks only, never a network file system: their locks cannot be trusted.
- Every process sharing one served folder runs on one kernel: on one machine,
  or all inside the same container. A lock does not cross from a container to
  its host.
- `git clean -fdX` while changes run is not safe: it removes the queue.
- An app writes whole files (G-CODE-5): a process that dies mid-write gives
  the turn back, and the next change must not find half a file.
- 0700 keeps the folder to this user on Linux and macOS; Windows has no such
  mode.

## Store

`store` is the one port every app keeps what it writes through (ADR-S22). An
app opens a scope once and names a thing by a key, a slash path inside it,
never a machine path:

```go
app := store.App{ID: "designer", Version: version, Features: list}
s, err := store.Served(app, filepath.Join(repo, ".designer"), func(thing store.Key) string {
	if thing == "" {
		return filepath.Join(repo, ".designer", ".queue", "root") // the folder's own turn
	}
	return filepath.Join(repo, ".designer", ".queue", "design", string(thing))
})
cfg, err := store.Config(app) // ~/.config/elderlygate/<app>, made private; store.Data likewise
defer s.Close()

item, there, err := s.Get(key)              // never waits; Item{Body, Sum "sha256:…"}
err = s.Change(ctx, "shop", "design apply", func(tx store.Tx) error {
	return tx.Put(key, body, store.Holds(item.Sum)) // or store.Absent(), store.Anything()
})
```

- `ParseKey` gives a key in its one form (contracts `mankit/v1` `storeKey`); a
  `..` step, a backslash, a machine path's start are refused as `outside`, and
  so is a step named `.queue` or `.store`, the store's own folders: stricter
  than `storeKey`, which allows them.
- A thing is a key's first step. `Change(ctx, thing, what, fn)` takes the
  thing's turn in the change queue (ADR-S14), and the folder's own turn first
  when the thing is not there yet; `Change(ctx, "", …)` is a change to the
  scope itself, which takes the folder's turn and the turn of each thing it
  writes as it writes it. `Config` and `Data` have one turn, in `<scope>/.queue/`.
  There is no separate lock.
- `fn` stages `Put`, `Remove` and `Move`; nothing is written unless it returns
  nil. `Tx.Get` reads the change as it stands: what was there, with what `fn`
  has staged on top; `store.ListIn(tx, prefix)` lists it the same way, in the
  order `List` gives. Each write's `Want` is checked as it is staged, against
  that, and again at commit against the disk, for a writer outside the store;
  either refuses with `changed`. `fn` must not call `Change` on the same scope:
  it would wait for ever for the turn its own change holds.
- One write alone is whole by itself. More are all or nothing: the change's
  mark goes into the `.queue/` of every turn it holds, its journal into this
  user's data folder (`<data>/<app>/.store/<machine>/`, 0700, one per machine),
  then the writes, the read-back, the journal removed, the marks last. The
  shapes and their lifetime are contracts `mankit/v1` `store-journal`,
  `store-journal-change` and `store-unfinished`.
- Putting a change back removes the core's temporary files, named
  `.<file>.~disk-<random>`, beside each file it puts back and inside each
  folder it takes away.
- The change queue's folders are private by user id, so users who share a
  served folder share it as one uid (as workspace containers do); the store
  tells them apart by user name and machine.
- Past the queue's limit (`App.Limit`) a change stops before its next write,
  is put back and fails with `change_too_long`; one already made and read back
  when the limit passes is done, and that it overran is said on the app's log.
- A change cut short is put back by the next change of the same user on the
  same machine, once its writer no longer runs: its process id no longer runs
  there (a pid reused by another process meanwhile counts as running; where
  the system cannot be asked, outside Linux and macOS, it always does). A cut
  short change of another thing that cannot be put back is said and does not
  stop this one. Another user's change to a key a mark holds is refused with
  `unfinished_change`, naming the user, the machine and `<app> store unfinished
  clear <thing>`; `Unfinished` lists the marks, with this user's journal file,
  and this user's journals whose marks are gone (`Left`), and `ClearShown`
  removes only the marks a person was shown, for that command, which asks first.
- `cli.UnfinishedCommand(app, open)` (package `store/cli`, above the store, so
  the store imports neither serve nor ask) is that command, which each app with a
  store scope mounts under its own `store`, and `cli.UnfinishedRoutes(open)` serves it: the app adds them to
  its `Site.Routes`. `store unfinished list [<thing>]` and `GET
  /api/store/unfinished[?thing=]` show every user's marks in the scope `open`
  opens (the folder's own turn, every thing's, and those the folder's marks and
  this user's journals name; a mark only in the turn of a thing that is gone
  shows with `list <thing>`, as the refusal names the thing), with this
  user's journal file, and this user's left journals (contracts mankit/v1
  `StoreUnfinishedList`). `store unfinished remove <thing>` shows each change
  holding the thing (user, machine, mark, journal, keys and, for this user's
  journal, what each write was to do, or that the journal cannot be read) and
  asks for the thing's name, a change to the system itself whatever the app's
  config says for `delete` (G-STACK-6): refused without `--yes` when there is
  no terminal. `DELETE /api/store/unfinished/<thing>?id=<mark>…` is serve's
  change door for the same, asked first in the same way. Both remove only the
  marks shown that still hold the thing once their turns are taken
  (`Scope.ClearShown`): nothing is put back, the journal stays
  (`StoreUnfinishedCleared`), and none left to remove is `not_found`. The
  refusal and the command name the app's CLI as `App.Command`, else its ID.
- A machine is known by its host name, so a product's container needs a fixed
  `--hostname` (such as `<product>-<project>`): a container made anew under
  another name would never find its own journals to put back.
- The safety rules live once, in `store/internal/disk`, which the queue writes
  through too, and which Go keeps every package outside the store from: every call inside one open folder (`os.Root`), regular files
  only, opened without waiting on a pipe, written whole. No link is followed,
  one leading inside the scope included: a key with a link on its way is
  refused as `outside`. The app's own scopes and the journals are private:
  when the folder itself is open to others, it is made 0700 and what this user
  owns inside it loses what others may do, each said on the app's log, and
  what another user owns inside is said and left as it is; a private folder is
  not walked. An own folder that is another user's, or is itself a link (as a
  dotfiles manager such as stow makes), is refused as `not_owned`, as every
  link is. A served file keeps the mode of the file it
  replaces, a moved file its own, and a new one gets 0644 whatever the umask
  says. Putting a change back in the process that made it gives a removed
  file its mode back. After a crash, a removed file is put back 0644 in a
  served folder and 0600 in the own scopes, as the journal holds no mode; the
  mode comes with the next store contract version.
- `Revision` moves whenever a thing changes; a reader polls it, or a server's
  live events watch it (ADR-E3).
- The adapter is picked in one place, through the app's feature list
  (G-CODE-7): files, always on, unless an enterprise adapter's feature is on
  (ADR-E9); a feature that is on but cannot run refuses the scope and never
  falls back to files. No other adapter is built yet.
- Problems are ADR-S21's, with `at[].file` the key: `outside`, `changed`,
  `not_owned`, `unfinished_change` (`store.Reason…`), and the queue's reasons.

`storetest.Run(t, harness)` is the conformance suite any adapter runs, with a
crash after each write of every kind of change. `store/testdata/twouser.sh`
runs two users on two machines (containers) sharing one served folder.

## Why textinput is vendored

`github.com/atotto/clipboard`, which bubbles' text input imports, searches
`PATH` for clipboard tools in `init`. Under WSL `PATH` holds the Windows
directories on `/mnt/c`, where each lookup is slow: every start lost one to
four seconds before the first frame. Here the tools are looked up only when
copying or pasting. Keep atotto out of the Linux build:

```sh
go list -deps ./... | grep atotto   # prints nothing on Linux
```

## Develop alongside the apps

A `go.work` in the folder above the four repos points the apps at this
checkout, so a change here shows in them without a release:

```sh
go work init ./mankit ./gitman ./dcman ./checkman
```

A merge to `main` releases mankit: the pipeline tags the next version. Then in
each app `go get github.com/tugbayatilla/mankit/v4@vX.Y.Z && go mod tidy`. The
module path is `github.com/tugbayatilla/mankit/v4` until a later migration moves
it with the org rename; the repository itself lives at
`github.com/ElderlyGate/mankit`.

CI runs [gorelease](https://pkg.go.dev/golang.org/x/exp/cmd/gorelease) on every
pull request: a change that breaks the public API fails it, unless the module
path itself moves to the next major (`/v4`), which is how a major is declared.

## Licence

MIT. See [LICENSE](LICENSE).
