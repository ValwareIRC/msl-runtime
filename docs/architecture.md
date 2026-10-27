# Architecture

```
cmd/mslrt           command line, config, control API, Windows install
internal/engine     assembles a runtime with every feature package
internal/script     parser: files, aliases, events, menus, dialog tables
internal/interp     interpreter: evaluation, conditions, control flow, events
internal/builtins   text, numbers, tokens, time, /echo
internal/regex      mIRC-style /pattern/flags regexes on regexp2
internal/storage    files, INI, /fopen handles, directories
internal/data       hash tables, &binvars, regex identifiers, crypto, codecs
internal/irc        connections, IRCv3, IAL, channel state, IRC commands
internal/control    users, timers, signals, groups, /load, $urlget, state
internal/sockets    TCP/UDP/TLS sockets and listeners
internal/dcc        DCC chat, send/get, fserve, DCC Server
internal/ui         model of dialogs, @windows, picture windows, prompts
internal/gui        Gio frontend that renders internal/ui (nogui build: none)
internal/spec       inventory of mIRC 7.85 commands, identifiers and events
internal/coverage   compares the inventory with what is registered
conformance         txtar-style script/expected-output cases
tools/specgen       builds the inventory from a decompiled mirc.chm
tools/coverage      prints coverage
examples            sample scripts, including a full IRC client
```

## One goroutine for scripts

mIRC runs scripts on a single thread, and so does mslrt. An `interp.Runtime`
owns all script state and is only touched from its own goroutine. Network
readers, timers, sockets, DCC transfers and GUI windows run on their own
goroutines and hand work over with `rt.Post(func())`. The engine's loop runs
each posted function to completion, so an event handler never sees state
change underneath it.

Blocking prompts such as `$input` keep running posted work while they wait,
just as mIRC keeps processing messages under a modal dialog.

## Feature packages

`interp` knows only the language. Every command, identifier and event comes
from a feature package that registers it:

```go
rt.RegisterCommand("echo", interp.Command{Fn: cmdEcho})
rt.RegisterIdent("len", identLen)
```

Packages fire events with `rt.Fire(interp.Fire{Event: "TEXT", ...})`. The
interpreter matches levels, prefixes, wildcards and targets and runs the
handlers in script load order, as mIRC does. `engine.New` installs every
package. Tests often install just the ones they need.

Control flow (`/return`, `/halt`, `/break`, `/continue`, `/goto`) travels as
typed error values, so Go call stacks unwind naturally and `:error` handlers
can catch script errors.

## UI model and frontends

`internal/ui` keeps dialogs and windows as plain data on the runtime
goroutine. A `ui.Frontend` renders that data and reports what the user does.
`internal/gui` is the Gio frontend: each dialog or window is its own
`app.Window` with its own theme, re-rendered from snapshots of the model. The
headless frontend does nothing, so scripts behave the same with no display.

Picture windows draw into an `image.RGBA` in the model. Text uses system
fonts found through `sfnt`, falling back to Segoe UI Emoji and then the Go
fonts, so `$width` and `$height` match what is drawn.

## Inventory and coverage

`internal/spec/inventory.json` lists every command, identifier and event that
mIRC 7.85's help file indexes. It holds names, syntax lines and page
references only, never the help text. `spec/scope.json` marks what is out of
scope (mIRC's own UI, COM, DDE, DLLs, and so on). `tools/coverage` reports
anything in scope that is not registered, and a test in `internal/spec`
checks the inventory itself.
