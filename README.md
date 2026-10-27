# msl-runtime

A runtime for mIRC scripts (mSL), written in Go. It loads a script or a
collection of scripts, connects to one or more IRC servers, and runs them the
way mIRC 7.85 would. It is not an IRC client: there is no chat interface, and
mIRC's own UI (popups for its windows, toolbars, switchbar, sounds, ...) is out
of scope. Dialogs, @windows and picture windows that scripts create are in
scope and appear as native desktop windows.

It is cross-platform. Windows-only mIRC features (COM, DDE, `/dll`,
SendMessage) are not part of it.

> **Note:** this project was written with Claude Opus 5.5, Anthropic's AI model.

## Documentation

- [Using mslrt](docs/usage.md): command line, configuration file, GUI and
  headless builds, control API, Windows install
- [Architecture](docs/architecture.md): packages, threading model, how
  features are registered
- [Known differences and extensions](#known-differences-from-mirc) (below)

## Installing (Windows)

Requires [Go](https://go.dev/dl/) 1.27 or newer.

```powershell
.\install.ps1              # build and install for your user; no admin needed
.\install.ps1 -Uninstall   # remove it again (your settings are kept)
```

This builds `mslrt.exe` (for terminals) and `mslrtw.exe` (no console window),
installs them to `%LOCALAPPDATA%\Programs\mslrt`, adds that folder to your PATH,
and registers `.mrc` files. Double-clicking one runs it with a status window
that shows its output and has a command line. Closing the status window stops
the script. Right-click > Edit opens it in Notepad. Scripts opened this way
keep their settings in `%APPDATA%\mslrt`, with a log in `mslrt.log` there.

If Windows was told to always open `.mrc` files with another app, the installer
says so. Pick mslrt under right-click > Open with > Choose another app.

On other platforms, `go install github.com/ValwareIRC/msl-runtime/cmd/mslrt@latest`
installs the `mslrt` command.

## Running

```sh
go build ./cmd/mslrt
mslrt bot.mrc more.mrc                     # load scripts, fire on START
mslrt -server irc.libera.chat:+6697 -nick mybot -join "#chan" bot.mrc
mslrt -config mslrt.json                   # see docs/usage.md
mslrt -headless ...                        # keep dialogs/@windows in memory
mslrt -http 127.0.0.1:0 ...                # local control API (state, exec, live output)
```

Lines typed on standard input run as commands, like mIRC's editbox with `//`.
`examples/mslrt.json` and `examples/scripts/hello.mrc` are a starting point.

`mslrt.json`:

```json
{
  "nick": "mybot", "altnick": "mybot_", "realname": "My bot",
  "scripts": ["scripts/*.mrc"], "aliases": ["aliases.ini"],
  "dataDir": "data",
  "servers": [
    { "host": "irc.libera.chat", "port": 6697, "tls": true, "join": ["#chan"],
      "sasl": { "user": "mybot", "pass": "secret" } }
  ]
}
```

`dataDir` is `$mircdir`. Relative file paths in scripts point there, and
`vars.ini` and `users.ini` live there. Global variables and the user list are
saved on exit and loaded at startup.

Build with `-tags nogui` for servers and headless machines. The GUI uses Gio,
which needs cgo and system libraries on Linux and macOS. Windows needs
neither.

## Example: a modern IRC client in mIRC script

`examples/client/chat.mrc` is a full chat client written entirely in mIRC
script and drawn in a picture window. It has a sidebar of networks and
conversations with unread badges, avatars, grouped messages and mention
highlighting, plus the IRCv3 features that modern chat apps have:

- **typing notifications**: sends and shows `+typing` (`TAGMSG`)
- **replies**: `+draft/reply` with a quoted preview. Click a message to reply.
- **reactions**: `+draft/react` and `+draft/unreact`. Right-click a message,
  click a chip, or type `/react 👍`.
- **echo-message**: your own messages get their real `msgid`, so others can
  reply to them.

```sh
mslrt examples/client/chat.mrc
```

Then type `/server irc.example.net +6697` and `/join #channel` in the box at
the bottom. The IRCv3 features need a server with `message-tags`, such as
Ergo. On other servers it still works as a plain IRC client.

## What is implemented

Coverage is measured against an inventory of every command, identifier and
event in mIRC 7.85's help file (`go run ./tools/coverage`). Everything in scope
is implemented: 206 commands, 380 identifiers and 69 events.

| Area | Packages |
|---|---|
| Language: parser, evaluation, `[ ]`, `$+`, `$!`, conditions, aliases, goto, `:error` | `internal/script`, `internal/interp` |
| Text, numbers, tokens, time, `$calc` | `internal/builtins` |
| IRC: multi-server, CAP, IRCv3 message tags, SASL, STS, IAL, modes, notify (MONITOR), ignore, proxies, `/debug` | `internal/irc` |
| Files, INI, `/fopen`, directories | `internal/storage` |
| Hash tables, `&binvars`, regex (`regexp2`), hashes, encodings, compression | `internal/data`, `internal/regex` |
| Users and levels, aop/avoice/protect, timers, signals, groups, `/load`, `$urlget` | `internal/control` |
| Sockets: TCP, listen, UDP, TLS | `internal/sockets` |
| DCC chat, send/get with resume and passive, fserve, DCC Server | `internal/dcc` |
| Dialogs, @windows, picture-window drawing, `@window` menus, prompts | `internal/ui` |
| Native windows (Gio) | `internal/gui` |

## Tests

```sh
go test ./...                 # unit, IRC/socket loopback, UI, client and conformance tests
go test -tags nogui ./...     # the same without the GUI
go run ./tools/coverage       # inventory coverage summary (-missing, -json)
```

Conformance cases live in `conformance/testdata`. Each records whether its
expected output comes from the help file (`spec`) or from a real mIRC 7.85 run
(`mirc-7.85`). `examples/client` drives the chat client against a fake IRC
server.

## Known differences from mIRC

- Expected behaviour comes from the help file. No case has yet been checked
  against a running mIRC 7.85, so undocumented quirks may differ.
- Text is measured and drawn with the system's fonts, found through `sfnt`.
  Missing glyphs fall back to Segoe UI Emoji and then the Go fonts. Kerning
  and hinting differ from Windows GDI, so `$width`, `$height` and `$wrap` can
  be off by a pixel or so.
- Hash tables keep insertion order (mIRC's order is unspecified). `$hash` uses
  FNV-1a, and `/hsave -b` uses this runtime's own binary layout.
- Regex is PCRE-like (`regexp2`). The `U` (ungreedy) flag and possessive
  quantifiers are not supported. Argon2d and encrypted `$zip` archives are not
  available.
- The clipboard uses the platform's command-line tools.
- Incoming DCC chats and sends are refused unless a script accepts them
  (`/dcc get`, `/creq auto`, `/sreq auto` or `/dcc trust`). With no display,
  there is nobody to ask.
- SASL EXTERNAL cannot be given a client certificate yet.
- Windows Defender may flag DCC-related source files as `Worm:IRC/Generic`, a
  false positive common to IRC tooling. To build or test `internal/dcc`, add an
  exclusion for the project folder.

### Extensions

These go beyond mIRC so that scripts can build richer @window interfaces:

- `menu @window` blocks get a `wheel` event for the mouse wheel, with the
  scroll amount in `$mouse.dy`.
- `$editbox(@window)` and `/editbox @window` work on custom windows that were
  opened with an editbox (`/window -e`).
- Some identifiers that mIRC accepts but its help index does not list (such
  as `$true`, `$false`, `$tab` and the case-sensitive `*cs` variants) are
  implemented too. `tools/coverage` lists them as "registered but not in the
  inventory".

## Regenerating the inventory

The inventory records names, syntax lines and help locations only, not the help
text. To rebuild it from an installed mIRC on Windows:

```powershell
hh.exe -decompile $env:TEMP\mirc-chm mirc.chm   # run from a path without spaces
go run ./tools/specgen -chm $env:TEMP\mirc-chm
```
