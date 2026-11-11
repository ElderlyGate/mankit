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
| `settings` | Every key of `config.yaml` to see and change: mankit's `theme`, `background` and `commands`, then the app's own. `settings/cli` adds `<app> get config` and `<app> set config <key> <value>` |
| `usercmd` | Your own commands from `<app folder>/config.yaml` (an old `commands.yaml` is moved in on first load): palette entry, optional key, a shell line with the selection filled in. One schema for all apps: [`usercmd/commands.schema.json`](usercmd/commands.schema.json) |
| `helpout` | The one layout for `<app> help`, `<app> --help` and a usage mistake: what the app is, how to start it, its commands, the repository |
| `home` | Where the org's apps keep their files: the org's config and data folders, a folder per product, and `CopyOnce` to bring old files along |
| `ask` | Asking before a CLI verb changes anything, in the style the user picked for that type of change (`name`, `yes-no`, `never`); refused without `--yes` when there is no terminal |
| `prefs` | What every app adds beside `settings/cli`: `<app> get confirm`, `<app> get locations`, `set config confirm.<type>`, and `prefs.Asker`, which asks in the app's styles and saves a remembered answer |
| `serve` | The server every app shares: the token guard, the contract, answers in the contracts envelope, asking before a change over HTTP, and `serve [--detach]`, `servers`, `servers stop`, `servers link` (see [Serve](#serve)) |
| `features` | An app's list of switchable features: the owner's switch first, then each closer handed in (public mode, a license); it knows no app (see [Features](#features)) |
| `events` | Live events: CloudEvents 1.0 checked against the app's catalog, sent on `GET /api/events` as Server-Sent Events through `serve`; a feature of the app's list, off means no stream (see [Events](#events)) |

## Settings

`config.yaml` is the one file of each app a person edits. `settings` lists its
keys, with the value in the file or the default, and changes one key at a
time: a value the key does not allow is refused, naming the allowed ones, and
an empty value puts the key back to its default. Only that key's line changes;
every other byte of the file stays as it was. A value it cannot change in place
(a block, a list, an anchor, a key written twice, a second document) is refused,
to be changed by hand.

```go
keys := settings.Keys("hubman", settings.Key{Name: "github", Default: "https", Values: []string{"https", "ssh"}, Help: "how to reach GitHub"})
cli.AddTo(root, keys, func() string { return settings.Path("hubman", "") })
```

```sh
hubman get config            # KEY VALUE SOURCE HELP; -o json or -o yaml
hubman set config theme nord
hubman set config theme ""   # back to the default
```

`settings.Path(app, dir)` is `<dir>/<app>/config.yaml`, the app folder
(`home.File(app, "config.yaml")`) when `dir` is empty; `theme.ResolveIn(path, flag, set)` reads the colours from
any path, and `usercmd/cli.CommandAt` serves `<app> commands` for any path.
`cli.AddToE` reports a clash with an existing `config` command or `-o` flag
instead of working around it. `settings.Next` is the value after the current one, for a dashboard
that changes a key with `enter`.

## Where the files are

Every app of the org keeps its files in one place, the same on Linux and macOS
(never `~/Library`):

| What | Where | `home` |
|---|---|---|
| the org's config folder | `$XDG_CONFIG_HOME/elderlygate`, else `~/.config/elderlygate` | `home.Dir()` |
| one app's config (the app folder) | `<config folder>/<product id>/` | `home.Product(id)` |
| the org's data folder | `$XDG_DATA_HOME/elderlygate`, else `~/.local/share/elderlygate` | `home.Data()` |
| one app's data | `<data folder>/<product id>/` | `home.ProductData(id)` |

The folder name is the one constant `home.Org`, so the org's rename changes one
line. A relative `XDG_*` value is ignored, as the XDG rules say.

`home.File(id, name)` is `<app folder>/<name>`, a path and nothing more: it
never copies or makes a file, so a read-only verb changes nothing.

`home.Migrate(id, names...)` brings each named file the app kept before in the
system's config folder (`~/.config/<app>/` on Linux,
`~/Library/Application Support/<app>/` on macOS) into the app folder. The app
calls it in one explicit step before its first write: in a verb that changes
something, or when its TUI starts; a read-only verb never calls it, so it
changes nothing. mankit's own files are
`config.yaml`, `split` and `favorites`, so gitman, dcman, checkman and hubman
keep their settings. The old file stays as it was, so an older build of the
app still finds it. Each copy runs once: when it is done, Migrate leaves a mark
in `<config folder>/.migrated/<app>/`, so a file the user deletes afterwards
never comes back. A name with no old file, or a copy that fails, leaves no
mark and is tried again next time.

```go
if err := home.Migrate("gitman", "config.yaml", "split", "favorites"); err != nil {
	fmt.Fprintln(os.Stderr, "warning: old settings not copied:", err)
}
```

Until then a read finds the old file where it is: `home.Current(id, name)` is
`home.File(id, name)`, or the old file while the new one is missing, Migrate has
not copied it and the old one exists. It never touches a file, so a read-only
verb shows the value in use before the first change.

`home.CopyOnce(from, to)` does the same for anything else: when `to` is missing
and `from` exists, it copies the file or the whole folder, and leaves `from`
as it was. A folder appears whole or not at all, and a `to` another process
made while the copy ran is kept: the copy is placed only where nothing is.

```go
dir, _ := home.ProductData("control-room")
h, _ := os.UserHomeDir()
old := filepath.Join(h, ".local", "share", "control-room", "github.json")
if err := home.CopyOnce(old, filepath.Join(dir, "github.json")); err != nil {
	return err
}
```

## Help

Every app answers `<app> help` and `<app> --help` in one layout. The app says
what it is, how to start it and where its repository is; the commands and
their one-line help come from cobra, hidden and deprecated ones left out. A
`Flags:` section lists the root's own flags, when it has any. `<app> help <verb>`
and `<app> <verb> --help` keep cobra's full help.

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
"Remember my answer"; each keeps the rest of the file. `<app> set config
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
stop asking. `prefs.AddTo` (after `settings/cli`) adds `get confirm` (each
type's style and whether it is the app's or the default), `get locations`
(the app's config, its data and the app's `Extra` places) and makes
`set config` ask first and take `confirm.<type>`.

Nothing is copied or written before the yes. Until the first change the styles
are read from `Read`, the old file in place (`home.Current`), so `get confirm`
shows the style in use; a no, or no terminal without `--yes`, writes nothing.
`Migrate` runs only after the yes, before the change is made.
`Options.System` names the types some verb of which changes the system
itself (`System: true`, such as `delete` for an uninstall): `get confirm` then
says how that change asks where the type's style does not hold for it, e.g.
`never — a change to the system itself still asks for the name`.

A command run with `--dry-run` asks nothing and writes nothing: no copy, no
mark, no remembered answer, and `set config` only says what it would change.

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
app := serve.App{ID: "designer", Command: "e-designer", Version: version, Contracts: []string{"v1"}, Addr: "127.0.0.1:7480"}
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
`apiVersion` (`<org>/<app>/app/<contract>`), `kind`, `ran` (the thing, the app,
its version and the contract), `data` and `errors`. The CLI builds the same
answer with `app.Answer` or `app.Failure` and writes it with `serve.Encode`, so
both doors give the same bytes.

What serve does for every app:

- answers only on this machine's own address, or a name given with `--host`;
- opens the page only from its link (`?token=`), which it swaps for the cookie
  `<app>-token-<port>` (Strict, HttpOnly); an API request carries the token in
  `X-<App>-Token` or that cookie, comes from the page itself, and sends JSON;
- answers `GET /api/version` with the contracts it answers, and the contract asked for in `Accept-Contract`,
  else 406 `unsupported_contract` with the versions it answers;
- answers a change (POST, PUT, PATCH, DELETE) with 428 `unconfirmed` until it
  comes with `confirm=yes`, unless the user picked `never` for its type.
  `confirm=remember` is a yes that also stops asking for the type
  (`Site.Remember`). Every change route names its action type; `Handler`
  refuses one that does not.

Each error has a code (`serve.Code…`); a program decides by the code, never by
the message. `<app> serve --help` and `<app> servers --help` are the reference
for the commands. Every server, detached or in the foreground, leaves a record
in `<data folder>/<app>/servers/`, in the shape of contracts `mankit/v1`;
`servers` lists them and says which is detached. The token link is printed to a
terminal only.

The tests check the real answers against the schemas in
[contracts](https://github.com/ElderlyGate/contracts).

## Features

A part of an app that can be switched off lives in one module and is reached
only through the app's feature list. `features` is that list; the
app hands in its features and where the owner's switches are kept.

```go
list := features.New(
	[]features.Feature{{ID: "events", Title: "Live events", Covers: "GET /api/events"}},
	features.Store{Off: readOff, Keep: keepOff},
	features.Closer{By: "public", Closed: publicClosed},
)
states, err := list.States() // each feature on, or off and what closed it
err = list.Set("events", false)
```

A feature is on until something closes it. The owner's switch (`features.Switch`)
is asked first, then each closer in order, and the first that closes a feature
is named in `State.By`. A closer only closes: switching a feature back on never
opens what another closer closed. `Set` refuses an id the list does not hold,
naming the ones it does, and a store that cannot be read is an error, never an
empty list.

## Events

`events` tells the app's page, and any other reader, what changed as it
happens. Every event is a CloudEvent 1.0 in structured JSON, in the shape of
contracts `event/v1`. It is a feature of its own: the app puts `events.Feature`
in its feature list and reaches the events only through the broker built from
that list.

```go
list := features.New([]features.Feature{events.Feature}, store)
broker, err := events.New(events.Config{
	Product:  "designer",
	Thing:    "shop", // the source is /designer/shop
	Catalog:  []string{"designer.resource.applied", "designer.layout.changed"},
	Features: list,
})
site.Routes = append(site.Routes, broker.Route()) // GET /api/events, behind serve's guard

ctx := events.WithTrace(r.Context(), r.Header.Get("traceparent"), r.Header.Get("tracestate"))
_, err = broker.Publish(ctx, "designer.resource.applied", "CMP-SERVE", data)
```

- `type` is `<product>.<thing>.<verb>`, the verb in the past; `Publish` refuses
  a type not in the catalog (`events.ErrNotInCatalog`), and `New` refuses a
  catalog type of another product.
- Every event carries `traceparent`: the request's, given with `WithTrace`, or
  a new trace when there is none or it is not valid.
- `GET /api/events` is `text/event-stream`: `id:` is the event's id and `data:`
  the event. An idle stream gets a `: heartbeat` comment. A stream that comes
  back with `Last-Event-ID` first gets the events after that one, from the last
  `Keep` kept in memory, or all of them when that one is no longer kept.
- It is a route like any other, so serve's guard stands in front of it: no
  stream without the token. The browser's `EventSource` sends the page's cookie.
- With the feature off, `GET /api/events` answers 404 `not_found`, `Publish`
  builds and keeps nothing, and open streams end within a heartbeat.
- Streams end when the client leaves or the server stops (`serve.Stopping`), so
  no stream holds the server open.

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

To release, tag mankit (`git tag v0.x.y && git push --tags`), then in each app
`go get github.com/tugbayatilla/mankit@v0.x.y && go mod tidy`. The module path stays `github.com/tugbayatilla/mankit` until a later migration moves it with the org rename; the repository itself lives at `github.com/ElderlyGate/mankit`.

## Licence

MIT. See [LICENSE](LICENSE).
