package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/ValwareIRC/msl-runtime/internal/builtins"
	"github.com/ValwareIRC/msl-runtime/internal/irc"
)

// Config is the JSON configuration file.
type Config struct {
	Nick        string         `json:"nick"`
	AltNick     string         `json:"altnick"`
	User        string         `json:"user"`
	RealName    string         `json:"realname"`
	Email       string         `json:"email"`
	QuitMessage string         `json:"quitMessage"`
	Scripts     []string       `json:"scripts"`
	Aliases     []string       `json:"aliases"`
	Servers     []ServerConfig `json:"servers"`
	// DataDir is $mircdir, where relative file paths in scripts point.
	DataDir string `json:"dataDir"`

	dir string
}

// ServerConfig is one server to connect to at startup.
type ServerConfig struct {
	Host     string   `json:"host"`
	Port     int      `json:"port"`
	TLS      bool     `json:"tls"`
	Password string   `json:"password"`
	Nick     string   `json:"nick"`
	AltNick  string   `json:"altnick"`
	Join     []string `json:"join"`
	SASL     *struct {
		Method string `json:"method"`
		User   string `json:"user"`
		Pass   string `json:"pass"`
	} `json:"sasl"`
}

// LoadConfig reads a configuration file; relative script paths are taken
// from the file's directory.
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	cfg := &Config{dir: filepath.Dir(path)}
	if err := json.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return cfg, nil
}

// dataDir returns $mircdir: the configured data directory, else the
// configuration file's directory, else the working directory ("").
func (c *Config) dataDir() string {
	if c.DataDir == "" {
		return c.dir
	}
	return c.resolve(c.DataDir)
}

func (c *Config) resolve(p string) string {
	if filepath.IsAbs(p) || c.dir == "" {
		return p
	}
	return filepath.Join(c.dir, p)
}

func (s ServerConfig) spec() irc.ServerSpec {
	spec := irc.ServerSpec{Host: s.Host, Port: s.Port, TLS: s.TLS, Password: s.Password, Join: s.Join}
	if spec.Port == 0 {
		spec.Port = 6667
		if s.TLS {
			spec.Port = 6697
		}
	}
	if s.SASL != nil {
		spec.LoginMethod, spec.LoginName, spec.LoginPass = s.SASL.Method, s.SASL.User, s.SASL.Pass
		if spec.LoginMethod == "" {
			spec.LoginMethod = "sasl"
		}
	}
	return spec
}

func (s ServerConfig) identity(base irc.Identity) irc.Identity {
	if s.Nick != "" {
		base.Nick = s.Nick
	}
	if s.AltNick != "" {
		base.AltNick = s.AltNick
	}
	return base
}

// Console prints echoes and errors to the terminal with a timestamp and the
// window name, and passes them on to the control API, a log file and any
// listeners (such as the status window).
type Console struct {
	Raw bool
	// Hub, when set, receives every line for the control API.
	Hub *hub

	mu        sync.Mutex
	log       *os.File
	listeners []func(window, text string, isErr bool)
}

// LogTo appends every line to a file as well.
func (c *Console) LogTo(path string) error {
	os.MkdirAll(filepath.Dir(path), 0o755)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	c.log = f
	return nil
}

// Listen registers fn to receive every line.
func (c *Console) Listen(fn func(window, text string, isErr bool)) {
	c.mu.Lock()
	c.listeners = append(c.listeners, fn)
	c.mu.Unlock()
}

func (c *Console) emit(window, text string, isErr bool) {
	text = strings.TrimRight(text, "\r\n")
	if c.Hub != nil {
		c.Hub.publish(apiLine{Time: time.Now().UnixMilli(), Window: window, Text: text, Error: isErr})
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	stamp := time.Now().Format("15:04:05")
	if isErr {
		fmt.Fprintf(os.Stderr, "%s %s\n", stamp, text)
	} else {
		fmt.Printf("%s [%s] %s\n", stamp, window, text)
	}
	if c.log != nil {
		fmt.Fprintf(c.log, "%s [%s] %s\r\n", stamp, window, text)
	}
	for _, fn := range c.listeners {
		fn(window, text, isErr)
	}
}

// Echo prints a line shown in a window.
func (c *Console) Echo(window, text string) {
	if !c.Raw {
		text = builtins.Strip(text, "")
	}
	c.emit(window, text, false)
}

// Error prints a script error.
func (c *Console) Error(text string) { c.emit("Status Window", text, true) }
