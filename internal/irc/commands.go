package irc

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/ValwareIRC/msl-runtime/internal/interp"
)

// maxText is how many bytes of message text go in one PRIVMSG/NOTICE; longer
// text is split, as mIRC does.
const maxText = 400

func (m *Manager) installCommands() {
	cmd := func(name string, fn func(*interp.CmdCall, *Conn) error) {
		m.rt.RegisterCommand(name, interp.Command{Fn: func(cc *interp.CmdCall) error {
			return fn(cc, m.FrameConn(cc.F))
		}})
	}
	cmd("server", m.cmdServer)
	cmd("disconnect", func(cc *interp.CmdCall, c *Conn) error { c.Disconnect(); return nil })
	cmd("quit", func(cc *interp.CmdCall, c *Conn) error {
		if c.conn == nil {
			return cc.Errorf("not connected to server")
		}
		c.Quit(cc.Args)
		return nil
	})
	cmd("nick", m.cmdNick)
	cmd("anick", func(cc *interp.CmdCall, c *Conn) error { c.Identity.AltNick = strings.TrimSpace(cc.Args); return nil })
	cmd("mnick", func(cc *interp.CmdCall, c *Conn) error {
		c.Identity.Nick = strings.TrimSpace(cc.Args)
		if c.conn == nil {
			c.Me = c.Identity.Nick
		}
		return nil
	})
	cmd("tnick", func(cc *interp.CmdCall, c *Conn) error { return sendf(cc, c, "NICK %s", strings.TrimSpace(cc.Args)) })
	cmd("join", m.cmdJoin)
	cmd("part", m.cmdPart)
	cmd("partall", func(cc *interp.CmdCall, c *Conn) error {
		for _, ch := range c.Channels {
			if err := sendf(cc, c, "PART %s :%s", ch.Name, cc.Args); err != nil {
				return err
			}
		}
		return nil
	})
	cmd("hop", m.cmdHop)
	for _, n := range []string{"msg", "privmsg"} {
		cmd(n, func(cc *interp.CmdCall, c *Conn) error { return m.cmdMessage(cc, c, "PRIVMSG", "") })
	}
	cmd("notice", func(cc *interp.CmdCall, c *Conn) error { return m.cmdMessage(cc, c, "NOTICE", "") })
	cmd("describe", func(cc *interp.CmdCall, c *Conn) error { return m.cmdMessage(cc, c, "PRIVMSG", "ACTION") })
	cmd("me", func(cc *interp.CmdCall, c *Conn) error { return m.toCurrent(cc, c, "PRIVMSG", "ACTION") })
	cmd("say", func(cc *interp.CmdCall, c *Conn) error { return m.toCurrent(cc, c, "PRIVMSG", "") })
	cmd("ame", func(cc *interp.CmdCall, c *Conn) error { return m.toAll(cc, c, true, false, "ACTION") })
	cmd("amsg", func(cc *interp.CmdCall, c *Conn) error { return m.toAll(cc, c, true, false, "") })
	cmd("qme", func(cc *interp.CmdCall, c *Conn) error { return m.toAll(cc, c, false, true, "ACTION") })
	cmd("qmsg", func(cc *interp.CmdCall, c *Conn) error { return m.toAll(cc, c, false, true, "") })
	cmd("omsg", func(cc *interp.CmdCall, c *Conn) error { return m.toOps(cc, c, "PRIVMSG") })
	cmd("onotice", func(cc *interp.CmdCall, c *Conn) error { return m.toOps(cc, c, "NOTICE") })
	cmd("ctcp", m.cmdCTCP)
	cmd("ctcpreply", m.cmdCTCPReply)
	cmd("query", m.cmdQuery)
	cmd("queryrn", func(cc *interp.CmdCall, c *Conn) error {
		f := cc.Fields()
		if len(f) < 2 {
			return cc.InvalidParams()
		}
		if c.queries.remove(c.Fold(f[0])) {
			c.queries.add(c.Fold(f[1]), f[1])
		}
		return nil
	})
	cmd("raw", m.cmdRaw)
	cmd("quote", m.cmdRaw)
	cmd("mode", func(cc *interp.CmdCall, c *Conn) error { return sendf(cc, c, "MODE %s", cc.Args) })
	cmd("kick", m.cmdKick)
	cmd("topic", m.cmdTopic)
	cmd("invite", func(cc *interp.CmdCall, c *Conn) error {
		f := cc.Fields()
		if len(f) < 1 {
			return cc.InvalidParams()
		}
		channel := currentChannel(cc)
		if len(f) > 1 {
			channel = f[1]
		}
		return sendf(cc, c, "INVITE %s %s", f[0], channel)
	})
	cmd("whois", func(cc *interp.CmdCall, c *Conn) error { return sendf(cc, c, "WHOIS %s", cc.Args) })
	cmd("away", func(cc *interp.CmdCall, c *Conn) error {
		c.AwayMsg = cc.Args
		if cc.Args == "" {
			return sendf(cc, c, "AWAY")
		}
		return sendf(cc, c, "AWAY :%s", cc.Args)
	})
	cmd("list", func(cc *interp.CmdCall, c *Conn) error {
		_, rest := cc.Switches()
		return sendf(cc, c, "LIST %s", rest)
	})
	cmd("links", func(cc *interp.CmdCall, c *Conn) error { c.links = nil; return sendf(cc, c, "LINKS") })
	cmd("ban", m.cmdBan)
	cmd("pop", func(cc *interp.CmdCall, c *Conn) error { return m.delayedMode(cc, c, 'o') })
	cmd("pvoice", func(cc *interp.CmdCall, c *Conn) error { return m.delayedMode(cc, c, 'v') })
	cmd("scid", func(cc *interp.CmdCall, c *Conn) error { return m.cmdScid(cc, false) })
	cmd("scon", func(cc *interp.CmdCall, c *Conn) error { return m.cmdScid(cc, true) })
	cmd("ial", func(cc *interp.CmdCall, c *Conn) error { return nil })
	cmd("ialclear", m.cmdIALClear)
	cmd("ialfill", func(cc *interp.CmdCall, c *Conn) error {
		_, rest := cc.Switches()
		return sendf(cc, c, "WHO %s", strings.TrimSpace(rest))
	})
	cmd("ialmark", m.cmdIALMark)
	cmd("ajinvite", func(cc *interp.CmdCall, c *Conn) error { return onOff(cc, &m.AutoJoinInvite) })
	cmd("autojoin", func(cc *interp.CmdCall, c *Conn) error { return m.cmdAutojoin(cc, c) })
	cmd("flood", m.cmdFlood)
	cmd("ctcps", func(cc *interp.CmdCall, c *Conn) error { return m.toggleEvents(cc, "ctcp") })
	cmd("events", func(cc *interp.CmdCall, c *Conn) error { return m.toggleEvents(cc, "on") })
	cmd("remote", func(cc *interp.CmdCall, c *Conn) error {
		on := strings.EqualFold(strings.TrimSpace(cc.Args), "on")
		for _, k := range []string{"on", "ctcp", "raw"} {
			m.rt.SetEventsEnabled(k, on)
		}
		return nil
	})
	cmd("parseline", m.cmdParseline)
	cmd("localinfo", func(cc *interp.CmdCall, c *Conn) error {
		_, rest := cc.Switches()
		if f := strings.Fields(rest); len(f) >= 2 {
			c.LocalHost, c.LocalIP = f[0], f[1]
		}
		return nil
	})
	cmd("identd", func(cc *interp.CmdCall, c *Conn) error {
		f := cc.Fields()
		if len(f) > 1 {
			m.IdentUser = f[1]
		}
		return nil
	})
	cmd("dns", m.cmdDNS)
	cmd("notify", m.cmdNotify)
	cmd("ignore", m.cmdIgnore)
}

// sendf formats and sends a line, attributing failures to the command.
func sendf(cc *interp.CmdCall, c *Conn, format string, args ...any) error {
	line := format
	if len(args) > 0 {
		line = strings.TrimSpace(fmt.Sprintf(format, args...))
	}
	if err := c.Send(line); err != nil {
		return cc.Errorf("%s", err.Error())
	}
	return nil
}

// currentChannel is the channel a command without one applies to: the
// event's channel.
func currentChannel(cc *interp.CmdCall) string {
	v, _ := cc.F.Event.Get("chan")
	return v
}

// currentTarget is where /say and /me go: the event's channel, or the user
// who sent a private message.
func currentTarget(cc *interp.CmdCall) string {
	if ch := currentChannel(cc); ch != "" {
		return ch
	}
	if v, ok := cc.F.Event.Get("nick"); ok {
		return v
	}
	return ""
}

func onOff(cc *interp.CmdCall, b *bool) error {
	switch strings.ToLower(strings.TrimSpace(cc.Args)) {
	case "on":
		*b = true
	case "off":
		*b = false
	default:
		return cc.InvalidParams()
	}
	return nil
}

// splitText breaks long message text at spaces into chunks of at most n bytes.
func splitText(text string, n int) []string {
	var out []string
	for len(text) > n {
		cut := strings.LastIndexByte(text[:n], ' ')
		if cut <= 0 {
			cut = n
		}
		out = append(out, text[:cut])
		text = strings.TrimLeft(text[cut:], " ")
	}
	return append(out, text)
}

// say sends text to a target as PRIVMSG/NOTICE, optionally as a CTCP.
func (c *Conn) say(cmd, target, ctcp, text string) error {
	for _, part := range splitText(text, maxText) {
		if ctcp != "" {
			part = EncodeCTCP(ctcp, part)
		}
		if err := c.Send(cmd + " " + target + " :" + part); err != nil {
			return err
		}
	}
	return nil
}

func (m *Manager) cmdMessage(cc *interp.CmdCall, c *Conn, cmd, ctcp string) error {
	_, rest := cc.Switches()
	target, text, _ := strings.Cut(rest, " ")
	if target == "" || text == "" {
		return cc.Errorf("insufficient parameters")
	}
	if strings.HasPrefix(target, "=") && m.ChatSend != nil {
		if err := m.ChatSend(target[1:], text, ctcp == "ACTION"); err != nil {
			return cc.Errorf("%s", err.Error())
		}
		return nil
	}
	if err := c.say(cmd, target, ctcp, text); err != nil {
		return cc.Errorf("%s", err.Error())
	}
	return nil
}

func (m *Manager) toCurrent(cc *interp.CmdCall, c *Conn, cmd, ctcp string) error {
	target := currentTarget(cc)
	if target == "" {
		return cc.Errorf("cannot use in status window")
	}
	if cc.Args == "" {
		return cc.Errorf("insufficient parameters")
	}
	if err := c.say(cmd, target, ctcp, cc.Args); err != nil {
		return cc.Errorf("%s", err.Error())
	}
	return nil
}

// toAll sends to every channel (/amsg, /ame) or query (/qmsg, /qme).
func (m *Manager) toAll(cc *interp.CmdCall, c *Conn, channels, queries bool, ctcp string) error {
	var targets []string
	if channels {
		for _, ch := range c.Channels {
			targets = append(targets, ch.Name)
		}
	}
	if queries {
		targets = append(targets, c.queries.names()...)
	}
	for _, t := range targets {
		if err := c.say("PRIVMSG", t, ctcp, cc.Args); err != nil {
			return cc.Errorf("%s", err.Error())
		}
	}
	return nil
}

func (m *Manager) toOps(cc *interp.CmdCall, c *Conn, cmd string) error {
	channel, text := currentChannel(cc), cc.Args
	if f, rest, _ := strings.Cut(text, " "); c.Support.IsChannel(f) {
		channel, text = f, rest
	}
	if channel == "" {
		return cc.InvalidParams()
	}
	return sendf(cc, c, "%s @%s :%s", cmd, channel, text)
}

func (m *Manager) cmdCTCP(cc *interp.CmdCall, c *Conn) error {
	f := strings.SplitN(cc.Args, " ", 3)
	if len(f) < 2 {
		return cc.InvalidParams()
	}
	arg := ""
	if len(f) == 3 {
		arg = f[2]
	}
	command := strings.ToUpper(f[1])
	if command == "PING" && arg == "" {
		arg = strconv.FormatInt(time.Now().Unix(), 10)
		c.pingSent = time.Now()
	}
	return sendf(cc, c, "PRIVMSG %s :%s", f[0], EncodeCTCP(command, arg))
}

func (m *Manager) cmdCTCPReply(cc *interp.CmdCall, c *Conn) error {
	f := strings.SplitN(cc.Args, " ", 3)
	if len(f) < 2 {
		return cc.InvalidParams()
	}
	arg := ""
	if len(f) == 3 {
		arg = f[2]
	}
	return sendf(cc, c, "NOTICE %s :%s", f[0], EncodeCTCP(strings.ToUpper(f[1]), arg))
}

func (m *Manager) cmdQuery(cc *interp.CmdCall, c *Conn) error {
	_, rest := cc.Switches()
	nick, text, _ := strings.Cut(rest, " ")
	if nick == "" {
		return cc.InvalidParams()
	}
	c.queries.add(c.Fold(nick), nick)
	if text != "" {
		return c.say("PRIVMSG", nick, "", text)
	}
	return nil
}

func (m *Manager) cmdRaw(cc *interp.CmdCall, c *Conn) error {
	sw, rest := cc.Switches()
	if cc.Name == "raw" && sw == "" {
		switch strings.ToLower(strings.TrimSpace(rest)) {
		case "on", "off":
			m.rt.SetEventsEnabled("raw", strings.EqualFold(strings.TrimSpace(rest), "on"))
			return nil
		}
	}
	if rest == "" {
		return cc.InvalidParams()
	}
	return sendf(cc, c, "%s", rest)
}

func (m *Manager) cmdNick(cc *interp.CmdCall, c *Conn) error {
	nick := strings.TrimSpace(cc.Args)
	if nick == "" {
		return cc.InvalidParams()
	}
	if c.conn == nil {
		c.Identity.Nick, c.Me = nick, nick
		return nil
	}
	return sendf(cc, c, "NICK %s", nick)
}

func (m *Manager) cmdJoin(cc *interp.CmdCall, c *Conn) error {
	sw, rest := cc.Switches()
	if strings.Contains(sw, "i") && rest == "" {
		rest = c.lastInvite
	}
	chans, keys, _ := strings.Cut(strings.TrimSpace(rest), " ")
	if chans == "" {
		return cc.InvalidParams()
	}
	list := strings.Split(chans, ",")
	for i, ch := range list {
		if !c.Support.IsChannel(ch) {
			list[i] = "#" + ch
		}
	}
	return sendf(cc, c, "JOIN %s %s", strings.Join(list, ","), keys)
}

func (m *Manager) cmdPart(cc *interp.CmdCall, c *Conn) error {
	channel, msg, _ := strings.Cut(strings.TrimSpace(cc.Args), " ")
	if !c.Support.IsChannel(channel) {
		msg = strings.TrimSpace(channel + " " + msg)
		channel = currentChannel(cc)
	}
	if channel == "" {
		return cc.InvalidParams()
	}
	if msg != "" {
		return sendf(cc, c, "PART %s :%s", channel, msg)
	}
	return sendf(cc, c, "PART %s", channel)
}

func (m *Manager) cmdHop(cc *interp.CmdCall, c *Conn) error {
	_, rest := cc.Switches()
	cur := currentChannel(cc)
	next, msg, _ := strings.Cut(strings.TrimSpace(rest), " ")
	if !c.Support.IsChannel(next) {
		msg, next = strings.TrimSpace(next+" "+msg), cur
	}
	if cur == "" || next == "" {
		return cc.InvalidParams()
	}
	if err := sendf(cc, c, "PART %s :%s", cur, msg); err != nil {
		return err
	}
	return sendf(cc, c, "JOIN %s", next)
}

func (m *Manager) cmdKick(cc *interp.CmdCall, c *Conn) error {
	f := strings.SplitN(strings.TrimSpace(cc.Args), " ", 3)
	if len(f) == 0 || f[0] == "" {
		return cc.InvalidParams()
	}
	channel := currentChannel(cc)
	if c.Support.IsChannel(f[0]) {
		channel, f = f[0], f[1:]
	}
	if channel == "" || len(f) == 0 {
		return cc.InvalidParams()
	}
	reason := ""
	if len(f) > 1 {
		reason = strings.Join(f[1:], " ")
	}
	return sendf(cc, c, "KICK %s %s :%s", channel, f[0], reason)
}

func (m *Manager) cmdTopic(cc *interp.CmdCall, c *Conn) error {
	channel, text, _ := strings.Cut(strings.TrimSpace(cc.Args), " ")
	if !c.Support.IsChannel(channel) {
		text, channel = strings.TrimSpace(channel+" "+text), currentChannel(cc)
	}
	if text == "" {
		return sendf(cc, c, "TOPIC %s", channel)
	}
	return sendf(cc, c, "TOPIC %s :%s", channel, text)
}

// cmdServer implements /server [-switches] [address [port] [password]]
// [-l method password] [-i nick anick email name] [-jn #chan key].
func (m *Manager) cmdServer(cc *interp.CmdCall, c *Conn) error {
	sw, rest := cc.Switches()
	words := strings.Fields(rest)
	spec := ServerSpec{Port: 6667}
	ident := c.Identity
	var plain []string
	for i := 0; i < len(words); i++ {
		w := words[i]
		switch strings.ToLower(w) {
		case "-l":
			if i+2 < len(words) {
				spec.LoginMethod, spec.LoginPass = words[i+1], words[i+2]
				i += 2
			}
			continue
		case "-lname":
			if i+1 < len(words) {
				spec.LoginName = words[i+1]
				i++
			}
			continue
		case "-i":
			for k, dst := range []*string{&ident.Nick, &ident.AltNick, &ident.Email, &ident.RealName} {
				if i+1+k < len(words) {
					*dst = words[i+1+k]
				}
			}
			if i+4 < len(words) {
				ident.RealName = strings.Join(words[i+4:], " ")
				i = len(words)
			} else {
				i += 4
			}
			continue
		case "-jn", "-j":
			if i+1 < len(words) {
				j := words[i+1]
				if i+2 < len(words) && !strings.HasPrefix(words[i+2], "-") {
					j += " " + words[i+2]
					i++
				}
				spec.Join = append(spec.Join, j)
				i++
			}
			continue
		}
		if strings.HasPrefix(w, "-") && len(w) > 1 {
			i++ // unsupported option with a value (-encoding, -key, ...)
			continue
		}
		plain = append(plain, w)
	}
	if len(plain) == 0 {
		if m.lastServer.Host == "" {
			return cc.Errorf("no server specified")
		}
		spec = m.lastServer
	} else {
		host := plain[0]
		if h, p, ok := strings.Cut(host, ":"); ok && !strings.Contains(p, ":") {
			host = h
			plain = append([]string{h, p}, plain[1:]...)
		}
		spec.Host = host
		if len(plain) > 1 {
			p := plain[1]
			if strings.HasPrefix(p, "+") {
				spec.TLS = true
			}
			if n, err := strconv.Atoi(strings.TrimLeft(p, "+*")); err == nil {
				spec.Port = n
			}
		}
		if len(plain) > 2 {
			spec.Password = plain[2]
		}
	}
	spec.TLS = spec.TLS || strings.Contains(sw, "e")
	spec.IPv6 = strings.Contains(sw, "6") && !strings.Contains(sw, "4")
	spec.NoConnectEvent = strings.Contains(sw, "c")
	spec.NoAutojoin = strings.Contains(sw, "o")
	spec.SkipSTS = strings.Contains(sw, "u")
	target := c
	if strings.ContainsAny(sw, "mn") {
		target = m.New()
		m.SetActive(target.ID)
	}
	target.Identity = ident
	if strings.ContainsAny(sw, "dn") {
		target.Spec = spec
		return nil
	}
	m.Connect(target, spec)
	return nil
}

// cmdScid implements /scid and /scon.
func (m *Manager) cmdScid(cc *interp.CmdCall, byIndex bool) error {
	sw, rest := cc.Switches()
	if strings.Contains(sw, "r") {
		cc.F.Cid = 0
		if c, ok := cc.F.Event.Conn.(*Conn); ok && cc.F.Event != nil {
			cc.F.Cid = c.ID
		}
		return nil
	}
	mask := 0
	if i := strings.IndexByte(sw, 't'); i >= 0 {
		mask, _ = strconv.Atoi(strings.TrimLeft(sw[i+1:], "abcdefghijklmnopqrsuvwxyz"))
	}
	if strings.Contains(sw, "a") {
		saved := cc.F.Cid
		defer func() { cc.F.Cid = saved }()
		for _, c := range append([]*Conn(nil), m.conns...) {
			if mask != 0 && !statusMatches(c, mask) {
				continue
			}
			cc.F.Cid = c.ID
			if err := cc.F.ExecLine(rest, 0); err != nil {
				return err
			}
		}
		return nil
	}
	n, command, _ := strings.Cut(strings.TrimSpace(rest), " ")
	id, err := strconv.Atoi(n)
	if err != nil {
		return cc.InvalidParams()
	}
	var target *Conn
	if byIndex {
		if id >= 1 && id <= len(m.conns) {
			target = m.conns[id-1]
		}
	} else {
		target = m.ByID(id)
	}
	if target == nil {
		return cc.Errorf("invalid connection id")
	}
	if command == "" {
		cc.F.Cid = target.ID
		return nil
	}
	if mask != 0 && !statusMatches(target, mask) {
		return nil
	}
	saved := cc.F.Cid
	cc.F.Cid = target.ID
	defer func() { cc.F.Cid = saved }()
	return cc.F.ExecLine(command, 0)
}

// statusMatches applies the /scid -tM filter: 1 connected, 2 not connected,
// 4 connecting, 8 not connecting.
func statusMatches(c *Conn, mask int) bool {
	connected := c.Status == Connected
	connecting := c.Status == Connecting || c.Status == LoggingIn
	return mask&1 != 0 && connected || mask&2 != 0 && !connected ||
		mask&4 != 0 && connecting || mask&8 != 0 && !connecting
}

func (m *Manager) cmdIALClear(cc *interp.CmdCall, c *Conn) error {
	arg := strings.TrimSpace(cc.Args)
	switch {
	case arg == "":
		c.IAL = map[string]*IALEntry{}
	case c.Support.IsChannel(arg):
		if ch := c.Channels[c.Fold(arg)]; ch != nil {
			for k := range ch.Members {
				delete(c.IAL, k)
			}
		}
	default:
		delete(c.IAL, c.Fold(arg))
	}
	return nil
}

func (m *Manager) cmdIALMark(cc *interp.CmdCall, c *Conn) error {
	sw, rest := cc.Switches()
	nick, mark, _ := strings.Cut(rest, " ")
	e := c.ial(nick)
	if e == nil {
		return nil
	}
	if strings.Contains(sw, "r") {
		e.Mark = ""
	} else {
		e.Mark = mark
	}
	return nil
}

func (m *Manager) cmdAutojoin(cc *interp.CmdCall, c *Conn) error {
	sw, _ := cc.Switches()
	switch {
	case strings.Contains(sw, "s"):
		c.Spec.NoAutojoin = true
	case strings.Contains(sw, "n"):
		for _, j := range c.Spec.Join {
			c.Send("JOIN " + j)
		}
		c.Spec.NoAutojoin = true
	}
	return nil
}

func (m *Manager) cmdFlood(cc *interp.CmdCall, c *Conn) error {
	_, rest := cc.Switches()
	f := strings.Fields(rest)
	m.flood.mu.Lock()
	defer m.flood.mu.Unlock()
	if len(f) == 0 {
		return nil
	}
	switch strings.ToLower(f[0]) {
	case "on":
		m.flood.on = true
	case "off":
		m.flood.on = false
	case "clear":
		m.flood.tokens = float64(m.flood.burst)
	default:
		m.flood.on = true
		if len(f) > 1 {
			m.flood.burst = max(1, interp.Int(f[1]))
		}
	}
	if m.flood.burst == 0 {
		m.flood.burst = 5
	}
	if m.flood.interval == 0 {
		m.flood.interval = 2 * time.Second
	}
	return nil
}

func (m *Manager) toggleEvents(cc *interp.CmdCall, kind string) error {
	switch strings.ToLower(strings.TrimSpace(cc.Args)) {
	case "on":
		m.rt.SetEventsEnabled(kind, true)
	case "off":
		m.rt.SetEventsEnabled(kind, false)
	case "":
	default:
		return cc.InvalidParams()
	}
	return nil
}

// cmdParseline implements /parseline -it|-ot|-iq|-oq [-b] [text]: with -t
// it replaces the line being processed by on PARSELINE; -q queues a new one.
func (m *Manager) cmdParseline(cc *interp.CmdCall, c *Conn) error {
	sw, text := cc.Switches()
	if strings.Contains(sw, "t") && cc.F.Event != nil {
		if _, ok := cc.F.Event.Values["parseline"]; ok {
			cc.F.Event.Values["parseline"] = text
			return nil
		}
	}
	if strings.Contains(sw, "q") {
		if strings.Contains(sw, "i") {
			m.rt.Post(func() { c.handleLine(text) })
			return nil
		}
		return sendf(cc, c, "%s", text)
	}
	return cc.InvalidParams()
}

// cmdBan implements /ban [-akruNbeIq] [#channel] <nick|address> [type] [reason].
func (m *Manager) cmdBan(cc *interp.CmdCall, c *Conn) error {
	sw, rest := cc.Switches()
	f := strings.Fields(rest)
	channel := currentChannel(cc)
	if len(f) > 0 && c.Support.IsChannel(f[0]) {
		channel, f = f[0], f[1:]
	}
	if channel == "" || len(f) == 0 {
		return cc.InvalidParams()
	}
	who := f[0]
	f = f[1:]
	maskType := -1
	if len(f) > 0 {
		if n, err := strconv.Atoi(f[0]); err == nil {
			maskType, f = n, f[1:]
		}
	}
	reason := strings.Join(f, " ")
	list := byte('b')
	for _, l := range "eIq" {
		if strings.ContainsRune(sw, l) {
			list = byte(l)
		}
	}
	sign := "+"
	if strings.Contains(sw, "r") {
		sign = "-"
	}
	mask := who
	if !strings.ContainsAny(who, "!@*") {
		e := c.ial(who)
		if e == nil || e.Host == "" {
			return cc.Errorf("%s not found in internal address list", who)
		}
		if maskType < 0 {
			mask = "*!*" + e.User + "@" + e.Host
		} else {
			mask = Mask(e.Address(), maskType)
		}
	}
	if err := sendf(cc, c, "MODE %s %s%s %s", channel, sign, string(list), mask); err != nil {
		return err
	}
	if strings.Contains(sw, "k") && !strings.ContainsAny(who, "!@*") {
		if err := sendf(cc, c, "KICK %s %s :%s", channel, who, reason); err != nil {
			return err
		}
	}
	if i := strings.IndexByte(sw, 'u'); i >= 0 && sign == "+" {
		secs := interp.Int(sw[i+1:])
		time.AfterFunc(time.Duration(secs)*time.Second, func() {
			m.rt.Post(func() { c.Send("MODE " + channel + " -" + string(list) + " " + mask) })
		})
	}
	return nil
}

// delayedMode implements /pop and /pvoice.
func (m *Manager) delayedMode(cc *interp.CmdCall, c *Conn, mode byte) error {
	f := cc.Fields()
	if len(f) < 2 {
		return cc.InvalidParams()
	}
	delay := interp.Int(f[0])
	channel, nick := currentChannel(cc), f[1]
	if len(f) > 2 {
		channel, nick = f[1], f[2]
	}
	apply := func() {
		if !c.hasMode(channel, nick, mode) {
			c.Send("MODE " + channel + " +" + string(mode) + " " + nick)
		}
	}
	if delay <= 0 {
		apply()
		return nil
	}
	time.AfterFunc(time.Duration(delay)*time.Second, func() { m.rt.Post(apply) })
	return nil
}

// cmdDNS resolves a host or a nick's host and fires on DNS.
func (m *Manager) cmdDNS(cc *interp.CmdCall, c *Conn) error {
	_, rest := cc.Switches()
	f := strings.Fields(rest)
	if len(f) == 0 {
		return nil
	}
	query := f[len(f)-1]
	nick := ""
	if !strings.ContainsAny(query, ".:") {
		e := c.ial(query)
		if e == nil || e.Host == "" {
			return cc.Errorf("%s not found in internal address list", query)
		}
		nick, query = query, e.Host
	}
	m.resolveAsync(query, func(addrs []string, err error) {
		ev := c.eventFor("DNS", nil)
		ev.Values["nick"] = nick
		ev.Values["naddress"] = query
		ev.Values["dns"] = strings.Join(addrs, " ")
		if len(addrs) > 0 {
			ev.Values["iaddress"] = addrs[0]
			ev.Values["raddress"] = addrs[0]
		}
		c.fire(interp.Fire{Name: "DNS", Event: ev})
	})
	return nil
}
