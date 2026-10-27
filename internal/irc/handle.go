package irc

import (
	"strconv"
	"strings"
	"time"

	"github.com/ValwareIRC/msl-runtime/internal/builtins"
	"github.com/ValwareIRC/msl-runtime/internal/interp"
	"github.com/ValwareIRC/msl-runtime/internal/script"
)

// handleLine processes one line from the server.
func (c *Conn) handleLine(line string) {
	c.debugLine("<-", line)
	if line = c.mgr.fireParseline(c, "in", line); line == "" {
		return
	}
	m := ParseMessage(line)
	if m.Command == "" {
		return
	}
	if isNumeric(m.Command) {
		c.numeric(m)
		return
	}
	switch m.Command {
	case "PING":
		halted := c.fire(interp.Fire{Name: "PING", Event: c.eventFor("PING", &m), Params: m.Params})
		_ = halted // mIRC always answers server pings
		c.Send("PONG :" + m.Last())
	case "PONG":
		if !c.pingSent.IsZero() {
			c.Lag = time.Since(c.pingSent)
		}
		c.fire(interp.Fire{Name: "PONG", Event: c.eventFor("PONG", &m), Params: m.Params})
	case "CAP":
		c.handleCap(m)
	case "AUTHENTICATE":
		c.handleAuthenticate(m)
	case "JOIN":
		c.onJoin(m)
	case "PART":
		c.onPart(m)
	case "KICK":
		c.onKick(m)
	case "QUIT":
		c.onQuit(m)
	case "NICK":
		c.onNick(m)
	case "MODE":
		c.onMode(m)
	case "TOPIC":
		c.onTopic(m)
	case "INVITE":
		c.onInvite(m)
	case "PRIVMSG":
		c.onPrivmsg(m)
	case "NOTICE":
		c.onNotice(m)
	case "WALLOPS":
		ev := c.eventFor("WALLOPS", &m)
		c.fire(interp.Fire{Name: "WALLOPS", Event: ev, Params: strings.Fields(m.Last()), Address: m.Source, Match: textOnly(m.Last())})
	case "ERROR":
		ev := c.eventFor("ERROR", &m)
		c.fire(interp.Fire{Name: "ERROR", Event: ev, Params: strings.Fields(m.Last()), Match: textOnly(m.Last())})
	case "ACCOUNT":
		if e := c.ial(m.Nick()); e != nil {
			e.Account = strings.TrimPrefix(m.Param(0), "*")
		}
		c.raw(m)
	case "AWAY":
		if e := c.ial(m.Nick()); e != nil {
			e.Away = len(m.Params) > 0
		}
		c.raw(m)
	case "CHGHOST":
		if e := c.ial(m.Nick()); e != nil {
			e.User, e.Host = m.Param(0), m.Param(1)
		}
		c.raw(m)
	default:
		c.raw(m)
	}
}

func isNumeric(cmd string) bool {
	if len(cmd) != 3 {
		return false
	}
	for i := 0; i < 3; i++ {
		if cmd[i] < '0' || cmd[i] > '9' {
			return false
		}
	}
	return true
}

// raw fires raw events for a numeric or unrecognised message and reports
// whether a script halted it.
func (c *Conn) raw(m Message) bool {
	ev := c.eventFor(m.Command, &m)
	ev.Values["numeric"] = m.Command
	params := splitParams(m.Params)
	text := strings.Join(params, " ")
	return c.fire(interp.Fire{
		Kind: "raw", Name: m.Command, Event: ev, Params: params, Address: m.Source,
		Match: func(f *interp.Frame, e *script.Event) (bool, error) {
			if !strings.EqualFold(e.Name, m.Command) && e.Name != "*" {
				return false, nil
			}
			if len(e.Fields) == 0 {
				return true, nil
			}
			return MatchText(f, e, e.Fields[0], text)
		},
	})
}

// splitParams flattens message parameters into $1- words.
func splitParams(ps []string) []string {
	var out []string
	for _, p := range ps {
		out = append(out, strings.Fields(p)...)
	}
	return out
}

// ial returns the address list entry for a nick, if known.
func (c *Conn) ial(nick string) *IALEntry { return c.IAL[c.Fold(nick)] }

// learn records a user's address in the IAL.
func (c *Conn) learn(source string) *IALEntry {
	nick, user, host := SplitAddress(source)
	if nick == "" {
		return nil
	}
	k := c.Fold(nick)
	e := c.IAL[k]
	if e == nil {
		e = &IALEntry{Nick: nick}
		c.IAL[k] = e
	}
	e.Nick = nick
	if user != "" {
		e.User = user
	}
	if host != "" {
		e.Host = host
	}
	return e
}

// forget drops a nick from the IAL when no common channel remains.
func (c *Conn) forget(nick string) {
	k := c.Fold(nick)
	if k == c.Fold(c.Me) {
		return
	}
	for _, ch := range c.Channels {
		if _, ok := ch.Members[k]; ok {
			return
		}
	}
	delete(c.IAL, k)
}

func (c *Conn) isMe(nick string) bool { return c.Fold(nick) == c.Fold(c.Me) }

// IsOp reports whether the runtime's user is an op on a channel.
func (c *Conn) IsOp(channel string) bool { return c.hasMode(channel, c.Me, 'o') }

func (c *Conn) hasMode(channel, nick string, mode byte) bool {
	ch := c.Channels[c.Fold(channel)]
	if ch == nil {
		return false
	}
	mem := ch.Members[c.Fold(nick)]
	return mem != nil && strings.IndexByte(mem.Modes, mode) >= 0
}

func (c *Conn) channelFire(name string, m *Message, channel string, params []string) bool {
	ev := c.eventFor(name, m)
	ev.Values["chan"] = channel
	ev.Values["target"] = channel
	return c.fire(interp.Fire{
		Name: name, Event: ev, Params: params, Address: m.Source, Me: c.isMe(m.Nick()),
		IsOp: func() bool { return c.IsOp(channel) }, Match: c.chanMatcher(channel),
	})
}

func (c *Conn) onJoin(m Message) {
	channel := m.Param(0)
	e := c.learn(m.Source)
	if len(m.Params) >= 3 && e != nil { // extended-join: account and real name
		e.Account = strings.TrimPrefix(m.Param(1), "*")
	}
	k := c.Fold(channel)
	if c.isMe(m.Nick()) {
		c.Channels[k] = newChannel(channel)
		c.Send("MODE " + channel)
	}
	if ch := c.Channels[k]; ch != nil {
		ch.add(c.Fold(m.Nick()), m.Nick(), "")
	}
	c.channelFire("JOIN", &m, channel, nil)
	for _, fn := range c.mgr.OnJoin {
		fn(c, m.Nick(), m.Source, channel)
	}
}

func (c *Conn) onPart(m Message) {
	channel := m.Param(0)
	c.channelFire("PART", &m, channel, strings.Fields(m.Param(1)))
	k := c.Fold(channel)
	if c.isMe(m.Nick()) {
		delete(c.Channels, k)
		c.pruneIAL()
		return
	}
	if ch := c.Channels[k]; ch != nil {
		ch.remove(c.Fold(m.Nick()))
	}
	c.forget(m.Nick())
}

func (c *Conn) onKick(m Message) {
	channel, victim := m.Param(0), m.Param(1)
	ev := c.eventFor("KICK", &m)
	ev.Values["chan"] = channel
	ev.Values["target"] = channel
	ev.Values["knick"] = victim
	c.fire(interp.Fire{
		Name: "KICK", Event: ev, Params: strings.Fields(m.Param(2)), Address: m.Source,
		Me: c.isMe(m.Nick()), IsOp: func() bool { return c.IsOp(channel) }, Match: c.chanMatcher(channel),
	})
	k := c.Fold(channel)
	if c.isMe(victim) {
		delete(c.Channels, k)
		c.pruneIAL()
		return
	}
	if ch := c.Channels[k]; ch != nil {
		ch.remove(c.Fold(victim))
	}
	c.forget(victim)
}

func (c *Conn) onQuit(m Message) {
	ev := c.eventFor("QUIT", &m)
	c.fire(interp.Fire{Name: "QUIT", Event: ev, Params: strings.Fields(m.Param(0)), Address: m.Source, Me: c.isMe(m.Nick())})
	k := c.Fold(m.Nick())
	for _, ch := range c.Channels {
		ch.remove(k)
	}
	delete(c.IAL, k)
}

func (c *Conn) onNick(m Message) {
	old, nick := m.Nick(), m.Param(0)
	ev := c.eventFor("NICK", &m)
	ev.Values["newnick"] = nick
	me := c.isMe(old)
	c.fire(interp.Fire{Name: "NICK", Event: ev, Params: []string{nick}, Address: m.Source, Me: me})
	if me {
		c.Me = nick
	}
	ok, nk := c.Fold(old), c.Fold(nick)
	for _, ch := range c.Channels {
		ch.rename(ok, nk, nick)
	}
	if e := c.IAL[ok]; e != nil {
		delete(c.IAL, ok)
		e.Nick = nick
		c.IAL[nk] = e
	}
}

func (c *Conn) onTopic(m Message) {
	channel := m.Param(0)
	if ch := c.Channels[c.Fold(channel)]; ch != nil {
		ch.Topic, ch.TopicBy, ch.TopicAt = m.Param(1), m.Source, time.Now()
	}
	c.channelFire("TOPIC", &m, channel, strings.Fields(m.Param(1)))
}

func (c *Conn) onInvite(m Message) {
	channel := m.Param(1)
	if c.mgr.ignored(m.Source, 'i') {
		return
	}
	ev := c.eventFor("INVITE", &m)
	ev.Values["chan"] = channel
	ev.Values["target"] = channel
	c.lastInvite = channel
	c.fire(interp.Fire{Name: "INVITE", Event: ev, Params: []string{channel}, Address: m.Source, Match: c.chanMatcher(channel)})
	if c.mgr.AutoJoinInvite && c.isMe(m.Param(0)) {
		c.Send("JOIN " + channel)
	}
}

// statusPrefixes are STATUSMSG prefixes a target may carry (@#chan).
func (c *Conn) splitStatusTarget(target string) string {
	for target != "" && strings.IndexByte(c.Support.Prefixes, target[0]) >= 0 && !c.Support.IsChannel(target) {
		target = target[1:]
	}
	return target
}

func (c *Conn) onPrivmsg(m Message) {
	target := c.splitStatusTarget(m.Param(0))
	text := m.Param(1)
	c.learn(m.Source)
	kind := byte('p')
	if c.Support.IsChannel(target) {
		kind = 'c'
	}
	if _, _, isCTCP := CTCP(text); isCTCP {
		if cmd, _, _ := CTCP(text); cmd != "ACTION" {
			kind = 't'
		}
	}
	if c.mgr.ignored(m.Source, kind) {
		return
	}
	if ch := c.Channels[c.Fold(target)]; ch != nil {
		if mem := ch.Members[c.Fold(m.Nick())]; mem != nil {
			mem.LastSeen = time.Now()
		}
	}
	if cmd, arg, ok := CTCP(text); ok {
		if cmd == "ACTION" {
			c.message("ACTION", &m, target, arg)
			return
		}
		c.onCTCP(m, target, cmd, arg)
		return
	}
	if !c.Support.IsChannel(target) && !c.queries.has(c.Fold(m.Nick())) {
		if c.openQuery(&m, text) {
			return // a ^OPEN handler halted it
		}
	}
	c.message("TEXT", &m, target, text)
}

// openQuery fires on OPEN for a private message from a new nick and reports
// whether a script halted it.
func (c *Conn) openQuery(m *Message, text string) bool {
	ev := c.eventFor("OPEN", m)
	ev.Values["target"] = m.Nick()
	halted := c.fire(interp.Fire{Name: "OPEN", Event: ev, Params: strings.Fields(text), Address: m.Source,
		Match: func(f *interp.Frame, e *script.Event) (bool, error) {
			if len(e.Fields) == 0 || (e.Fields[0] != "?" && e.Fields[0] != "*") {
				return false, nil
			}
			if len(e.Fields) < 2 {
				return true, nil
			}
			return MatchText(f, e, e.Fields[1], text)
		}})
	if !halted {
		c.queries.add(c.Fold(m.Nick()), m.Nick())
	}
	return halted
}

// message fires TEXT, ACTION or NOTICE.
func (c *Conn) message(name string, m *Message, target, text string) bool {
	ev := c.eventFor(name, m)
	isChan := c.Support.IsChannel(target)
	if isChan {
		ev.Values["chan"] = target
	}
	ev.Values["target"] = target
	ev.Values["stripped"] = strconv.Itoa(len(text) - len(builtins.Strip(text, "")))
	if _, echo := c.Caps["echo-message"]; echo && c.isMe(m.Nick()) {
		ev.Values["parseem"] = "$true"
	}
	return c.fire(interp.Fire{
		Name: name, Event: ev, Params: strings.Fields(text), Address: m.Source, Me: c.isMe(m.Nick()),
		IsOp:  func() bool { return isChan && c.IsOp(target) },
		Match: c.textMatcher(text, target),
	})
}

func (c *Conn) onNotice(m Message) {
	target := c.splitStatusTarget(m.Param(0))
	text := m.Last()
	if !strings.Contains(m.Source, "!") && (c.Status != Connected || m.Source == c.ServerID || m.Source == "") {
		ev := c.eventFor("SNOTICE", &m)
		c.fire(interp.Fire{Name: "SNOTICE", Event: ev, Params: strings.Fields(text), Match: textOnly(text)})
		return
	}
	c.learn(m.Source)
	if c.mgr.ignored(m.Source, 'n') {
		return
	}
	if cmd, arg, ok := CTCP(text); ok {
		ev := c.eventFor("CTCPREPLY", &m)
		ev.Values["target"] = target
		full := strings.TrimSpace(cmd + " " + arg)
		c.fire(interp.Fire{Name: "CTCPREPLY", Event: ev, Params: strings.Fields(full), Address: m.Source, Match: textOnly(full)})
		return
	}
	c.message("NOTICE", &m, target, text)
}

// onCTCP fires ctcp events and sends the standard reply unless halted.
func (c *Conn) onCTCP(m Message, target, cmd, arg string) {
	full := strings.TrimSpace(cmd + " " + arg)
	ev := c.eventFor("CTCP", &m)
	ev.Values["target"] = target
	if c.Support.IsChannel(target) {
		ev.Values["chan"] = target
	}
	halted := false
	if c.rt().EventsEnabled("ctcp") {
		halted = c.fire(interp.Fire{
			Kind: "ctcp", Name: cmd, Event: ev, Params: strings.Fields(full), Address: m.Source,
			IsOp: func() bool { return c.IsOp(target) },
			Match: func(f *interp.Frame, e *script.Event) (bool, error) {
				if len(e.Fields) < 2 {
					return false, nil
				}
				loc, err := evalField(f, e.Fields[1])
				if err != nil || !matchLocation(c, loc, target) {
					return false, err
				}
				if ok, err := MatchText(f, e, e.Fields[0], cmd); ok || err != nil {
					return ok, err
				}
				return MatchText(f, e, e.Fields[0], full)
			},
		})
	}
	if cmd == "DCC" {
		if !halted && c.mgr.DCC != nil {
			c.mgr.DCC(c, m, arg, ev)
		}
		return
	}
	if halted && cmd != "VERSION" {
		return
	}
	reply := ""
	switch cmd {
	case "VERSION":
		reply = Version
	case "PING":
		reply = arg
	case "TIME":
		reply = time.Now().Format("Mon Jan 02 15:04:05 2006")
	case "CLIENTINFO":
		reply = "ACTION CLIENTINFO DCC PING TIME VERSION"
	case "FINGER":
		reply = c.Identity.RealName + " Idle " + strconv.Itoa(int(time.Since(c.LastSent).Seconds())) + " seconds"
	default:
		return
	}
	c.Send("NOTICE " + m.Nick() + " :" + EncodeCTCP(cmd, reply))
}

// pruneIAL drops users who share no channel with the runtime's user.
func (c *Conn) pruneIAL() {
	for k, e := range c.IAL {
		if k == c.Fold(c.Me) {
			continue
		}
		c.forget(e.Nick)
	}
}

// dccHandler receives CTCP DCC requests with the event scripts saw, so the
// DCC package can honour /dcc get and /dcc reject from ctcp events.
type dccHandler func(c *Conn, m Message, arg string, ev *interp.Event)
