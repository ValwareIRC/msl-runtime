// Command mslrt runs mIRC scripts against one or more IRC servers.
//
//	mslrt [flags] script.mrc [more.mrc ...]
//	mslrt -config mslrt.json
//	mslrt install | uninstall
//
// Lines typed on standard input run as commands, like mIRC's editbox with //.
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/ValwareIRC/msl-runtime/internal/engine"
	"github.com/ValwareIRC/msl-runtime/internal/gui"
	"github.com/ValwareIRC/msl-runtime/internal/irc"
	"github.com/ValwareIRC/msl-runtime/internal/script"
)

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "install":
			exitOn(install())
			return
		case "uninstall":
			exitOn(uninstall())
			return
		}
	}

	configPath := flag.String("config", "", "JSON configuration file")
	server := flag.String("server", "", "server to connect to, as host[:port] (prefix the port with + for TLS)")
	nick := flag.String("nick", "", "nickname")
	altNick := flag.String("altnick", "", "alternate nickname")
	join := flag.String("join", "", "comma-separated channels to join after connecting")
	raw := flag.Bool("raw", false, "keep colour and formatting codes in output")
	noStdin := flag.Bool("no-stdin", false, "do not read commands from standard input")
	httpAddr := flag.String("http", "", "serve the local control API on this address (for example 127.0.0.1:0)")
	headless := flag.Bool("headless", false, "keep dialogs and @windows in memory instead of showing them")
	dataDir := flag.String("data", "", "data directory ($mircdir) for files, vars.ini and users.ini")
	logPath := flag.String("log", "", "also write output and errors to this file")
	status := flag.Bool("status", false, "show a status window with output and a command line; closing it stops the runtime")
	flag.Parse()

	events := &hub{}
	out := &Console{Raw: *raw, Hub: events}
	if *logPath != "" {
		if err := out.LogTo(*logPath); err != nil {
			fmt.Fprintln(os.Stderr, err)
		}
	}

	cfg := &Config{}
	if *configPath != "" {
		var err error
		if cfg, err = LoadConfig(*configPath); err != nil {
			out.Error(err.Error())
			if !*status {
				os.Exit(1)
			}
			cfg = &Config{}
		}
	}
	cfg.Scripts = append(cfg.Scripts, flag.Args()...)
	if *nick != "" {
		cfg.Nick = *nick
	}
	if *altNick != "" {
		cfg.AltNick = *altNick
	}
	if *server != "" {
		cfg.Servers = append(cfg.Servers, serverFlag(*server, *join))
	}
	if *dataDir != "" {
		os.MkdirAll(*dataDir, 0o755)
		cfg.DataDir = *dataDir
	}

	useGUI := gui.Available && !*headless
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	eng := engine.New(out, cfg.dataDir())
	eng.IRC.Identity = irc.Identity{Nick: cfg.Nick, AltNick: cfg.AltNick, User: cfg.User, RealName: cfg.RealName, Email: cfg.Email}
	for _, c := range eng.IRC.Conns() {
		c.Identity = eng.IRC.Identity
	}
	if cfg.QuitMessage != "" {
		eng.IRC.QuitMessage = cfg.QuitMessage
	}
	if useGUI {
		eng.UI.Front = gui.New(eng.RT, eng.UI)
		if *status {
			openStatus(eng, out, cfg, stop)
		}
	}

	if err := loadScripts(eng, out, cfg); err != nil {
		out.Error(err.Error())
		if !*status {
			os.Exit(1)
		}
	}
	eng.Start()
	for i, s := range cfg.Servers {
		c := eng.IRC.Active()
		if i > 0 || c.Status != irc.Disconnected {
			c = eng.IRC.New()
		}
		c.Identity = s.identity(eng.IRC.Identity)
		eng.IRC.Connect(c, s.spec())
	}

	if *httpAddr != "" {
		url, err := serveAPI(eng, *httpAddr, events)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Println("api:", url)
	}

	if !*noStdin && !*status {
		go readCommands(eng)
	}
	if useGUI {
		// Gio needs the main goroutine; the engine runs beside it.
		go func() {
			eng.Run(ctx)
			os.Exit(0)
		}()
		gui.Main()
		return
	}
	eng.Run(ctx)
}

func exitOn(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "mslrt:", err)
		os.Exit(1)
	}
}

// openStatus shows the status window: script output and errors, with a
// command line. Closing it stops the runtime.
func openStatus(eng *engine.Engine, out *Console, cfg *Config, stop func()) {
	title := "mslrt"
	if len(cfg.Scripts) > 0 {
		names := make([]string, len(cfg.Scripts))
		for i, s := range cfg.Scripts {
			names[i] = filepath.Base(s)
		}
		title += " - " + strings.Join(names, ", ")
	}
	st := gui.OpenStatus(title, func(line string) {
		eng.RT.Post(func() { _ = eng.RT.Exec(line) })
	}, stop)
	out.Listen(func(window, text string, isErr bool) { st.Add(window, text, isErr) })
}

// loadScripts loads alias files and remote scripts, expanding wildcards.
func loadScripts(eng *engine.Engine, out *Console, cfg *Config) error {
	load := func(patterns []string, kind script.Kind) error {
		for _, p := range patterns {
			p = cfg.resolve(p)
			matches, err := filepath.Glob(p)
			if err != nil || len(matches) == 0 {
				matches = []string{p}
			}
			for _, path := range matches {
				_, errs := eng.RT.LoadFile(path, kind)
				for _, e := range errs {
					if os.IsNotExist(e) {
						return fmt.Errorf("script not found: %s", path)
					}
					out.Error("warning: " + e.Error())
				}
				out.Echo("Status Window", "* Loaded script '"+path+"'")
			}
		}
		return nil
	}
	if err := load(cfg.Aliases, script.Aliases); err != nil {
		return err
	}
	return load(cfg.Scripts, script.Remote)
}

func serverFlag(s, join string) ServerConfig {
	sc := ServerConfig{Host: s, Port: 6667}
	if host, port, ok := strings.Cut(s, ":"); ok {
		sc.Host = host
		sc.TLS = strings.HasPrefix(port, "+")
		sc.Port, _ = strconv.Atoi(strings.TrimPrefix(port, "+"))
	}
	if join != "" {
		sc.Join = strings.Split(join, ",")
	}
	return sc
}

// readCommands runs each line of standard input as a command.
func readCommands(eng *engine.Engine) {
	sc := bufio.NewScanner(os.Stdin)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		done := make(chan struct{})
		eng.RT.Post(func() {
			defer close(done)
			_ = eng.RT.Exec(line)
		})
		select {
		case <-done:
		case <-time.After(time.Minute):
		}
	}
}
