# Using mslrt

## Command line

```
mslrt [flags] [script.mrc ...]
mslrt install | uninstall          (Windows only)
```

| Flag | Meaning |
|---|---|
| `-config file` | JSON configuration (see below). Flags given as well override it. |
| `-server host[:port]` | Connect on start. Prefix the port with `+` for TLS (`irc.libera.chat:+6697`). |
| `-nick`, `-altnick` | Nickname and alternate nickname. |
| `-join "#a,#b"` | Channels to join after connecting. |
| `-data dir` | Data directory (`$mircdir`). Relative paths in scripts, `vars.ini` and `users.ini` live here. Defaults to the config file's folder, or the current folder. |
| `-log file` | Also append all output and errors to this file. |
| `-raw` | Keep colour and formatting control codes in printed output. |
| `-no-stdin` | Do not read commands from standard input. |
| `-headless` | Keep dialogs and @windows in memory instead of showing them. |
| `-status` | Show a status window with output and a command line. Closing it stops the runtime. |
| `-http addr` | Serve the local control API (for example `127.0.0.1:0`; the chosen address is printed as `api: ...`). |

Scripts load in order and `on START` fires once all are loaded. Files on the
command line and in `scripts` are remote scripts. Files listed in `aliases`
are alias files in the style of mIRC's aliases.ini, written as
`name commands` with no `alias` keyword.

Each line read on standard input, or typed in the status window, runs as a
command, the way mIRC's editbox runs `//command`: identifiers are evaluated and
`/` is optional.

Output goes to standard output as `HH:MM:SS [window] text`, and errors go to
standard error. Colour codes are stripped unless `-raw` is given.

## Configuration file

```json
{
  "nick": "mybot",
  "altnick": "mybot_",
  "user": "mybot",
  "realname": "My bot",
  "email": "bot@example.net",
  "quitMessage": "bye",
  "scripts": ["scripts/*.mrc"],
  "aliases": ["aliases.ini"],
  "dataDir": "data",
  "servers": [
    {
      "host": "irc.libera.chat", "port": 6697, "tls": true,
      "password": "", "nick": "", "altnick": "", "join": ["#chan"],
      "sasl": { "method": "plain", "user": "mybot", "pass": "secret" }
    }
  ]
}
```

Paths are relative to the configuration file, and `scripts` and `aliases`
may use globs. Each entry in `servers` opens its own connection, so `$cid`,
`/scid` and `/scon` work as they do in mIRC. A server's own `nick` and
`altnick` override the global ones. The `port` defaults to 6697 with TLS and
6667 without. Leave `sasl` out to log in without it. Its `method` is `plain`,
which is the default. `external` is accepted too, but no client certificate
can be configured yet, so it only works where the server does not need one.

Global variables (`/set`) and the user list (`/auser`) are saved to `vars.ini`
and `users.ini` in the data directory on exit, and loaded again at startup.

## GUI and headless builds

The default build shows dialogs, @windows and picture windows as native
windows, using [Gio](https://gioui.org). On Windows this needs no cgo.
On Linux and macOS, Gio needs cgo and the usual system graphics libraries.

```sh
go build ./cmd/mslrt                 # with GUI
go build -tags nogui ./cmd/mslrt     # without; UI stays in memory
```

In a headless run, `/dialog` and `/window` still create their windows and every
identifier still reports on them, so scripts that only use dialogs as data
work unchanged. Prompts such as `$input` and `$sfile` return as if cancelled.

## Control API

`-http 127.0.0.1:0` starts a small HTTP API for tools and dashboards. It binds
only to the address you give it and has no authentication, so keep it on
loopback.

| Endpoint | Description |
|---|---|
| `GET /state` | JSON snapshot: connections (with channels, lag and TLS), the active cid, loaded scripts, global variables, and timers. |
| `POST /exec` | Body `{"line": "/echo hi", "cid": 1}` runs a command, optionally on a given connection. Returns `{"error": ""}`. |
| `GET /events` | Server-sent events, one JSON object per output line: `{"time", "window", "text", "error"}`. The last 500 lines are replayed first. |

## Windows install

`install.ps1` builds `mslrt.exe` and `mslrtw.exe` (the same program, built as
a GUI binary so no console window opens) and then runs `mslrt install`, which:

- copies both binaries to `%LOCALAPPDATA%\Programs\mslrt`,
- adds that folder to your user PATH,
- registers `.mrc` under `HKCU\Software\Classes` so that Open runs
  `mslrtw.exe -status -data "%APPDATA%\mslrt" -log "%APPDATA%\mslrt\mslrt.log" "<file>"`,
  and Edit opens the file in Notepad.

`mslrt uninstall` (or `install.ps1 -Uninstall`) reverses all of this. It keeps
`%APPDATA%\mslrt`. Neither command needs administrator rights.
