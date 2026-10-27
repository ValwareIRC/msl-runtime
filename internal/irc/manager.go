package irc

import (
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ValwareIRC/msl-runtime/internal/builtins"
	"github.com/ValwareIRC/msl-runtime/internal/interp"
	"github.com/ValwareIRC/msl-runtime/internal/regex"
	"github.com/ValwareIRC/msl-runtime/internal/script"
)

// Version is the client name sent in CTCP VERSION replies.
const Version = "mslrt (mIRC script runtime)"

// Manager owns all connections of a runtime.
type Manager struct {
	rt     *interp.Runtime
	conns  []*Conn
	nextID int
	active int

	// QuitMessage is the default /quit message.
	QuitMessage string
	// Identity is the default identity for new connections.
	Identity Identity
	// CTCPs reports whether ctcp events are processed (/ctcps).
	AutoJoinInvite bool
	// Autojoin enables joining channels listed in ServerSpec.Join.
	Autojoin bool

	// DCC receives CTCP DCC requests; the dcc package sets it.
	DCC dccHandler
	// ChatSend delivers /msg =nick and /describe =nick over DCC chat.
	ChatSend func(nick, text string, action bool) error
	// Lists are the notify and ignore lists.
	Lists

	// Proxy is the /proxy setting for server connections.
	Proxy Proxy
	// PerformOn is the /perform switch.
	PerformOn bool
	// ResolvePath turns a script filename into a path (-relative).
	ResolvePath func(string) string

	// OnJoin runs after a user joins a channel (for auto-op lists).
	OnJoin []func(c *Conn, nick, address, channel string)
	// OnDisconnect runs when a connection closes (for online timers).
	OnDisconnect []func(c *Conn)

	flood floodControl
	// lastServer is reused by /server with no parameters.
	lastServer ServerSpec
	// started records when the online timers began.
	onlineTotal time.Duration
}

type managerKey struct{}

// Install registers the IRC commands, identifiers, operators and events on
// rt and returns the manager.
func Install(rt *interp.Runtime) *Manager {
	m := &Manager{rt: rt, nextID: 1, QuitMessage: Version, Autojoin: true,
		Lists: Lists{NotifyOn: true, IgnoreOn: true}}
	m.active = m.New().ID
	rt.SendRaw = func(f *interp.Frame, line string) error {
		c := m.FrameConn(f)
		if c == nil || c.conn == nil {
			cmd, _, _ := strings.Cut(line, " ")
			return interp.Errorf(strings.ToLower(cmd), "not connected to server")
		}
		return c.Send(line)
	}
	interp.Ext(rt, managerKey{}, func() *Manager { return m })
	m.installCommands()
	m.installIdents()
	m.installOperators()
	m.installExtras()
	rt.DeclareEvents(handledEvents...)
	return m
}

// From returns the manager installed on rt, or nil.
func From(rt *interp.Runtime) *Manager {
	return interp.Ext(rt, managerKey{}, func() *Manager { return nil })
}

// New creates a disconnected connection (a new "server window").
func (m *Manager) New() *Conn {
	c := newConn(m, m.nextID)
	c.Identity = m.Identity
	m.nextID++
	m.conns = append(m.conns, c)
	return c
}

// Conns returns connections in creation order.
func (m *Manager) Conns() []*Conn { return m.conns }

// ByID returns a connection by its cid.
func (m *Manager) ByID(id int) *Conn {
	for _, c := range m.conns {
		if c.ID == id {
			return c
		}
	}
	return nil
}

// Active returns the active connection.
func (m *Manager) Active() *Conn {
	if c := m.ByID(m.active); c != nil {
		return c
	}
	if len(m.conns) > 0 {
		return m.conns[0]
	}
	return m.New()
}

// SetActive changes the active connection.
func (m *Manager) SetActive(id int) { m.active = id }

// FrameConn returns the connection a frame acts on.
func (m *Manager) FrameConn(f *interp.Frame) *Conn {
	if f != nil && f.Cid != 0 {
		if c := m.ByID(f.Cid); c != nil {
			return c
		}
	}
	return m.Active()
}

// Connect starts a connection on c (or a new one).
func (m *Manager) Connect(c *Conn, spec ServerSpec) {
	if c == nil {
		c = m.New()
	}
	m.lastServer = spec
	c.Connect(spec)
}

// Close removes a disconnected connection's window.
func (m *Manager) Close(c *Conn) {
	for i, x := range m.conns {
		if x == c {
			m.conns = append(m.conns[:i], m.conns[i+1:]...)
			break
		}
	}
	if m.active == c.ID && len(m.conns) > 0 {
		m.active = m.conns[0].ID
	}
}

// handledEvents are the on-events this package fires.
var handledEvents = []string{
	"ACTION", "BAN", "CONNECT", "CONNECTFAIL", "CTCPREPLY", "DEHELP", "DEOP",
	"DEVOICE", "DISCONNECT", "ERROR", "HELP", "INVITE", "JOIN", "KICK", "LOGON",
	"MODE", "NICK", "NOTICE", "NOTIFY", "OP", "PARSELINE", "PART", "PING",
	"PONG", "QUIT", "OPEN", "RAWMODE", "SERVERMODE", "SERVEROP", "SNOTICE", "TEXT",
	"TOPIC", "UNBAN", "UNOTIFY", "USERMODE", "VOICE", "WALLOPS", "CTCP", "RAW", "DNS",
}

// eventFor builds the identifier values common to server events.
func (c *Conn) eventFor(name string, msg *Message) *interp.Event {
	ev := &interp.Event{Name: name, Values: map[string]string{}, Conn: c}
	if msg != nil {
		ev.Values["rawmsg"] = msg.Raw
		ev.Values["rawbytes"] = msg.Raw
		if len(msg.Tags) > 0 {
			raw, _, _ := strings.Cut(strings.TrimPrefix(msg.Raw, "@"), " ")
			ev.Values["msgtags"] = raw
			if t, err := time.Parse(time.RFC3339Nano, msg.Tags["time"]); err == nil {
				ev.Values["msgstamp"] = strconv.FormatInt(t.Unix(), 10)
			}
		}
		if msg.Source != "" {
			setUser(ev, msg.Source)
		}
	}
	return ev
}

// setUser fills $nick, $address, $fulladdress, $site and $wildsite.
func setUser(ev *interp.Event, source string) {
	nick, user, host := SplitAddress(source)
	ev.Values["nick"] = nick
	ev.Values["fulladdress"] = source
	if user != "" || host != "" {
		ev.Values["address"] = user + "@" + host
		ev.Values["site"] = host
		ev.Values["wildsite"] = "*!*@" + host
	}
}

// fire dispatches an on-event for a connection.
func (c *Conn) fire(fe interp.Fire) bool {
	fe.Cid = c.ID
	if fe.Kind == "" {
		fe.Kind = "on"
	}
	return c.rt().Dispatch(fe)
}

// matchLocation checks an event's location field ("*", "?", "#", "#a,#b",
// "=" for DCC chats) against where a message arrived.
func matchLocation(c *Conn, field, target string) bool {
	if field == "" || field == "*" {
		return true
	}
	isChan := c.Support.IsChannel(target)
	for _, loc := range strings.Split(field, ",") {
		switch {
		case loc == "*":
			return true
		case loc == "?":
			if !isChan {
				return true
			}
		case loc == "#":
			if isChan {
				return true
			}
		case isChan && interp.WildMatch(loc, target, false):
			return true
		}
	}
	return false
}

// evalField evaluates an event field that uses variables or identifiers.
func evalField(f *interp.Frame, field string) (string, error) {
	if strings.ContainsAny(field, "$%") && !strings.HasPrefix(field, "/") && !strings.HasPrefix(field, "m/") {
		return f.Eval(field)
	}
	return field, nil
}

// MatchText checks an event's match text against text: a wildcard pattern
// (with & for a word), or a regular expression when the event has the $ prefix.
func MatchText(f *interp.Frame, e *script.Event, pattern, text string) (bool, error) {
	if strings.Contains(e.Prefixes, "$") {
		re, err := regex.Compile(pattern)
		if err != nil {
			return false, nil
		}
		if re.Strip {
			text = builtins.Strip(text, "")
		}
		ok, err := re.MatchString(text)
		return ok && err == nil, nil
	}
	p, err := evalField(f, pattern)
	if err != nil {
		return false, err
	}
	return interp.WildMatchWords(p, text, false), nil
}

// textMatcher returns a Match function for events with matchtext:location fields.
func (c *Conn) textMatcher(text, target string) func(*interp.Frame, *script.Event) (bool, error) {
	return func(f *interp.Frame, e *script.Event) (bool, error) {
		if len(e.Fields) < 2 {
			return false, nil
		}
		loc, err := evalField(f, e.Fields[1])
		if err != nil {
			return false, err
		}
		if !matchLocation(c, loc, target) {
			return false, nil
		}
		return MatchText(f, e, e.Fields[0], text)
	}
}

// chanMatcher returns a Match function for events whose single field is a
// channel list (#, #a,#b, *).
func (c *Conn) chanMatcher(channel string) func(*interp.Frame, *script.Event) (bool, error) {
	return func(f *interp.Frame, e *script.Event) (bool, error) {
		if len(e.Fields) == 0 {
			return true, nil
		}
		loc, err := evalField(f, e.Fields[0])
		if err != nil {
			return false, err
		}
		return matchLocation(c, loc, channel), nil
	}
}

// textOnly returns a Match function for events whose single field is match text.
func textOnly(text string) func(*interp.Frame, *script.Event) (bool, error) {
	return func(f *interp.Frame, e *script.Event) (bool, error) {
		if len(e.Fields) == 0 {
			return true, nil
		}
		return MatchText(f, e, e.Fields[0], text)
	}
}

func (m *Manager) fireConnectFail(c *Conn, err error) {
	ev := c.eventFor("CONNECTFAIL", nil)
	c.fire(interp.Fire{Name: "CONNECTFAIL", Event: ev, Params: strings.Fields(err.Error())})
	m.rt.Out.Echo(interp.StatusWindow, "* Unable to connect to server ("+err.Error()+")")
}

func (m *Manager) fireDisconnect(c *Conn, err error) {
	if !c.ConnectedAt.IsZero() {
		m.onlineTotal += time.Since(c.ConnectedAt)
	}
	ev := c.eventFor("DISCONNECT", nil)
	c.fire(interp.Fire{Name: "DISCONNECT", Event: ev})
	for _, fn := range m.OnDisconnect {
		fn(c)
	}
	if err != nil && !c.quitting {
		m.rt.Out.Echo(interp.StatusWindow, "* Disconnected ("+err.Error()+")")
	}
}

// fireParseline runs on PARSELINE for a line and returns the line to use,
// which /parseline may have changed or cleared.
func (m *Manager) fireParseline(c *Conn, dir, line string) string {
	if !m.hasEvent("PARSELINE") {
		return line
	}
	ev := c.eventFor("PARSELINE", nil)
	ev.Values["parsetype"] = dir
	ev.Values["parseline"] = line
	ev.Values["parseutf"] = "$true"
	c.fire(interp.Fire{Name: "PARSELINE", Event: ev, Params: strings.Fields(line),
		Match: func(f *interp.Frame, e *script.Event) (bool, error) {
			if len(e.Fields) < 2 {
				return false, nil
			}
			if t := e.Fields[0]; t != "*" && !strings.EqualFold(t, dir) {
				return false, nil
			}
			return MatchText(f, e, e.Fields[1], line)
		}})
	return ev.Values["parseline"]
}

// hasEvent reports whether any loaded script defines an on-event, so hot
// paths can skip building event values.
func (m *Manager) hasEvent(name string) bool {
	for _, f := range m.rt.Files() {
		for _, e := range f.Events {
			if e.Kind == "on" && strings.EqualFold(e.Name, name) {
				return true
			}
		}
	}
	return false
}

// floodControl paces outgoing lines when /flood is on.
type floodControl struct {
	mu       sync.Mutex
	on       bool
	burst    int
	interval time.Duration
	tokens   float64
	last     time.Time
}

// delay returns how long to wait before sending a line.
func (f *floodControl) delay(int) time.Duration {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.on {
		return 0
	}
	now := time.Now()
	if f.last.IsZero() {
		f.tokens = float64(f.burst)
	} else {
		f.tokens = min(float64(f.burst), f.tokens+now.Sub(f.last).Seconds()/f.interval.Seconds())
	}
	f.last = now
	if f.tokens >= 1 {
		f.tokens--
		return 0
	}
	wait := time.Duration((1 - f.tokens) * float64(f.interval))
	f.tokens = 0
	f.last = now.Add(wait)
	return wait
}

// resolveAsync looks up a host off the runtime goroutine.
func (m *Manager) resolveAsync(host string, done func(addrs []string, err error)) {
	go func() {
		addrs, err := net.LookupHost(host)
		m.rt.Post(func() { done(addrs, err) })
	}()
}
