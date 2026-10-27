package irc

import (
	"strconv"
	"strings"
	"time"

	"github.com/ValwareIRC/msl-runtime/internal/interp"
)

// numeric handles numeric replies: state first, then raw events.
func (c *Conn) numeric(m Message) {
	switch m.Command {
	case "001":
		c.Me = m.Param(0)
		c.ServerID = m.Source
		c.Status = Connected
		c.ConnectedAt = time.Now()
		c.learn(c.Me)
	case "005":
		for _, tok := range m.Params[1:max(1, len(m.Params)-1)] {
			c.Support.apply(tok)
		}
	case "376", "422":
		c.loggedOn()
	case "432", "433", "437":
		if c.Status == LoggingIn {
			c.nextNick()
		}
	case "353":
		c.names(m)
	case "366":
		if ch := c.Channels[c.Fold(m.Param(1))]; ch != nil {
			ch.Synced = true
		}
	case "332":
		if ch := c.Channels[c.Fold(m.Param(1))]; ch != nil {
			ch.Topic = m.Param(2)
		}
	case "333":
		if ch := c.Channels[c.Fold(m.Param(1))]; ch != nil {
			ch.TopicBy = m.Param(2)
			ch.TopicAt = unixTime(m.Param(3))
		}
	case "324":
		if ch := c.Channels[c.Fold(m.Param(1))]; ch != nil {
			ch.Modes, ch.Key, ch.Limit = "", "", ""
			c.applyChannelModes(ch, m.Params[2:], "", nil)
		}
	case "329":
		if ch := c.Channels[c.Fold(m.Param(1))]; ch != nil {
			ch.Created = unixTime(m.Param(2))
		}
	case "367", "348", "346":
		mode := map[string]byte{"367": 'b', "348": 'e', "346": 'I'}[m.Command]
		c.listEntry(mode, m.Param(1), m.Param(2), m.Param(3), m.Param(4))
	case "728":
		c.listEntry('q', m.Param(1), m.Param(3), m.Param(4), m.Param(5))
	case "352":
		if e := c.learn(m.Param(5) + "!" + m.Param(2) + "@" + m.Param(3)); e != nil {
			e.Away = strings.HasPrefix(m.Param(6), "G")
		}
	case "311":
		c.learn(m.Param(1) + "!" + m.Param(2) + "@" + m.Param(3))
	case "305":
		c.Away = false
	case "306":
		c.Away, c.AwayAt = true, time.Now()
	case "221":
		c.UserModes = strings.TrimPrefix(m.Param(1), "+")
	case "730", "731":
		c.monitor(m)
	case "364":
		hops, info, _ := strings.Cut(m.Last(), " ")
		c.links = append(c.links, LinkEntry{Addr: m.Param(1), Hub: m.Param(2), Hops: interp.Int(hops), Info: info})
	case "365":
		// end of LINKS; keep the list until the next /links
	case "900":
		if e := c.ial(c.Me); e != nil {
			e.Account = m.Param(2)
		}
	case "903", "904", "905", "906", "907":
		if c.Status == LoggingIn {
			c.Send("CAP END")
		}
	}
	c.raw(m)
}

func unixTime(s string) time.Time {
	n, _ := strconv.ParseInt(s, 10, 64)
	if n == 0 {
		return time.Time{}
	}
	return time.Unix(n, 0)
}

// loggedOn runs once the MOTD has ended: on CONNECT and autojoin.
func (c *Conn) loggedOn() {
	if c.Status != Connected {
		c.Status = Connected
	}
	if c.ConnectedAt.IsZero() {
		c.ConnectedAt = time.Now()
	}
	if _, ok := c.Support.Tokens["MONITOR"]; ok && c.mgr.Lists.NotifyOn && len(c.mgr.Lists.Notify) > 0 {
		var nicks []string
		for _, n := range c.mgr.Lists.Notify {
			if n.Network == "" || strings.EqualFold(n.Network, c.Network()) {
				nicks = append(nicks, n.Nick)
			}
		}
		if len(nicks) > 0 {
			c.Send("MONITOR + " + strings.Join(nicks, ","))
		}
	}
	if !c.Spec.NoConnectEvent {
		c.fire(interp.Fire{Name: "CONNECT", Event: c.eventFor("CONNECT", nil)})
	}
	if c.mgr.Autojoin && !c.Spec.NoAutojoin {
		for _, j := range c.Spec.Join {
			c.Send("JOIN " + j)
		}
	}
	c.Spec.NoConnectEvent = true // only once per connection
}

// nextNick tries the alternate nick, then numbered variants.
func (c *Conn) nextNick() {
	c.nickTry++
	nick := c.Identity.AltNick
	if c.nickTry > 1 || nick == "" {
		base := c.Identity.Nick
		if len(base) > 7 {
			base = base[:7]
		}
		nick = base + strconv.Itoa(c.nickTry)
	}
	c.Me = nick
	c.Send("NICK " + nick)
}

// names records a 353 RPL_NAMREPLY.
func (c *Conn) names(m Message) {
	ch := c.Channels[c.Fold(m.Param(2))]
	if ch == nil {
		return
	}
	for _, item := range strings.Fields(m.Last()) {
		modes := ""
		for item != "" && strings.IndexByte(c.Support.Prefixes, item[0]) >= 0 {
			i := strings.IndexByte(c.Support.Prefixes, item[0])
			if i < len(c.Support.PrefixModes) {
				modes += string(c.Support.PrefixModes[i])
			}
			item = item[1:]
		}
		e := c.learn(item)
		if e == nil {
			continue
		}
		ch.add(c.Fold(e.Nick), e.Nick, modes)
	}
}

func (c *Conn) listEntry(mode byte, channel, mask, by, at string) {
	ch := c.Channels[c.Fold(channel)]
	if ch == nil {
		return
	}
	for _, e := range ch.Lists[mode] {
		if strings.EqualFold(e.Mask, mask) {
			return
		}
	}
	ch.Lists[mode] = append(ch.Lists[mode], ListEntry{Mask: mask, By: by, At: unixTime(at)})
}

// onMode handles channel and user mode changes.
func (c *Conn) onMode(m Message) {
	target := m.Param(0)
	if !c.Support.IsChannel(target) {
		if c.isMe(target) {
			c.UserModes = applyFlags(c.UserModes, m.Param(1))
			ev := c.eventFor("USERMODE", &m)
			c.fire(interp.Fire{Name: "USERMODE", Event: ev, Params: m.Params[1:], Address: m.Source})
		}
		return
	}
	ch := c.Channels[c.Fold(target)]
	if ch == nil {
		ch = newChannel(target)
	}
	modeText := strings.Join(m.Params[1:], " ")
	server := !strings.Contains(m.Source, "!")
	ev := c.eventFor("RAWMODE", &m)
	ev.Values["chan"] = target
	ev.Values["target"] = target
	fireOn := func(name string, extra map[string]string, targetAddr string) {
		e := c.eventFor(name, &m)
		e.Values["chan"] = target
		e.Values["target"] = target
		for k, v := range extra {
			e.Values[k] = v
		}
		e.Values["mode"] = modeText
		c.fire(interp.Fire{Name: name, Event: e, Params: strings.Fields(modeText), Address: m.Source, TargetAddress: targetAddr,
			Me: c.isMe(m.Nick()), IsOp: func() bool { return c.IsOp(target) }, Match: c.chanMatcher(target)})
	}
	type pendingMode struct {
		name   string
		extra  map[string]string
		target string
	}
	var pending []pendingMode
	changes := map[string][]string{}
	c.applyChannelModes(ch, m.Params[1:], m.Source, func(adding bool, mode byte, arg string) {
		var name, key string
		switch mode {
		case 'o':
			name, key = pick(adding, "OP", "DEOP"), "opnick"
			if server && adding {
				name = "SERVEROP"
			}
		case 'v':
			name, key = pick(adding, "VOICE", "DEVOICE"), "vnick"
		case 'h':
			name, key = pick(adding, "HELP", "DEHELP"), "hnick"
		case 'b':
			name, key = pick(adding, "BAN", "UNBAN"), "banmask"
		}
		if name == "" {
			return
		}
		changes[strings.ToLower(name)] = append(changes[strings.ToLower(name)], arg)
		extra := map[string]string{key: arg}
		targetAddr := ""
		if mode == 'b' {
			extra["bnick"] = c.nickForMask(arg)
		} else if e := c.ial(arg); e != nil {
			targetAddr = e.Address()
		} else {
			targetAddr = arg + "!*@*"
		}
		pending = append(pending, pendingMode{name, extra, targetAddr})
	})
	for i, p := range pending {
		p.extra["modefirst"] = interp.Bool(i == 0)
		p.extra["modelast"] = interp.Bool(i == len(pending)-1)
		fireOn(p.name, p.extra, p.target)
	}
	ev.Values["mode"] = modeText
	for k, v := range changes {
		ev.Values["mode:"+k] = strings.Join(v, " ")
	}
	c.fire(interp.Fire{Name: "RAWMODE", Event: ev, Params: strings.Fields(modeText), Address: m.Source,
		Me: c.isMe(m.Nick()), IsOp: func() bool { return c.IsOp(target) }, Match: c.chanMatcher(target)})
	if server {
		fireOn("SERVERMODE", nil, "")
	} else {
		fireOn("MODE", nil, "")
	}
}

func pick(b bool, yes, no string) string {
	if b {
		return yes
	}
	return no
}

// applyChannelModes updates channel state from a mode string and its
// arguments, calling each for every parameterised change.
func (c *Conn) applyChannelModes(ch *Channel, params []string, by string, each func(adding bool, mode byte, arg string)) {
	if len(params) == 0 {
		return
	}
	modes, args := params[0], params[1:]
	adding := true
	for i := 0; i < len(modes); i++ {
		mode := modes[i]
		switch mode {
		case '+':
			adding = true
			continue
		case '-':
			adding = false
			continue
		}
		arg := ""
		if c.Support.modeArg(mode, adding) && len(args) > 0 {
			arg, args = args[0], args[1:]
		}
		switch {
		case strings.IndexByte(c.Support.PrefixModes, mode) >= 0:
			if mem := ch.Members[c.Fold(arg)]; mem != nil {
				mem.Modes = applyFlag(mem.Modes, mode, adding)
			}
		case strings.IndexByte(c.Support.ChanModes[0], mode) >= 0:
			if adding {
				ch.Lists[mode] = append(ch.Lists[mode], ListEntry{Mask: arg, By: by, At: time.Now()})
			} else {
				kept := ch.Lists[mode][:0]
				for _, e := range ch.Lists[mode] {
					if !strings.EqualFold(e.Mask, arg) {
						kept = append(kept, e)
					}
				}
				ch.Lists[mode] = kept
			}
		case mode == 'k':
			ch.Key = pick(adding, arg, "")
		case mode == 'l':
			ch.Limit = pick(adding, arg, "")
		default:
			ch.Modes = applyFlag(ch.Modes, mode, adding)
		}
		if each != nil {
			each(adding, mode, arg)
		}
	}
}

func applyFlag(set string, mode byte, adding bool) string {
	has := strings.IndexByte(set, mode) >= 0
	switch {
	case adding && !has:
		return set + string(mode)
	case !adding && has:
		return strings.ReplaceAll(set, string(mode), "")
	}
	return set
}

// applyFlags applies a +abc-d style string to a flag set.
func applyFlags(set, change string) string {
	adding := true
	for i := 0; i < len(change); i++ {
		switch change[i] {
		case '+':
			adding = true
		case '-':
			adding = false
		default:
			set = applyFlag(set, change[i], adding)
		}
	}
	return set
}

// nickForMask returns the first IAL nick matching a ban mask.
func (c *Conn) nickForMask(mask string) string {
	for _, e := range c.IAL {
		if interp.WildMatch(mask, e.Address(), false) {
			return e.Nick
		}
	}
	return ""
}

// monitor handles MONITOR online/offline replies as notify events.
func (c *Conn) monitor(m Message) {
	name := "NOTIFY"
	if m.Command == "731" {
		name = "UNOTIFY"
	}
	for _, target := range strings.Split(m.Last(), ",") {
		if target == "" {
			continue
		}
		c.mgr.notifyState(c, NickOf(target), m.Command == "730")
		ev := c.eventFor(name, &Message{Source: target, Raw: m.Raw})
		c.fire(interp.Fire{Name: name, Event: ev, Address: target})
	}
}
