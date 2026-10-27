package irc

import (
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ValwareIRC/msl-runtime/internal/interp"
)

func (m *Manager) installIdents() {
	id := func(name string, fn func(*interp.Call, *Conn) (string, error)) {
		m.rt.RegisterIdent(name, func(c *interp.Call) (string, error) { return fn(c, m.FrameConn(c.F)) })
	}
	// Values that only exist inside events.
	for _, n := range []string{
		"fulladdress", "site", "wildsite", "target", "rawmsg", "rawbytes", "msgtags",
		"numeric", "knick", "newnick", "opnick", "vnick", "hnick", "bnick", "banmask",
		"event", "clevel", "maddress", "matchkey", "parseline", "parsetype", "parseutf",
		"stripped", "iaddress", "naddress", "raddress",
	} {
		name := n
		m.rt.RegisterIdent(name, func(c *interp.Call) (string, error) {
			v, _ := c.F.Event.Get(name)
			return v, nil
		})
	}

	id("me", func(c *interp.Call, conn *Conn) (string, error) { return conn.Me, nil })
	id("anick", func(c *interp.Call, conn *Conn) (string, error) { return conn.Identity.AltNick, nil })
	id("mnick", func(c *interp.Call, conn *Conn) (string, error) { return conn.Identity.Nick, nil })
	id("nick", m.identNick)
	id("chan", m.identChan)
	id("address", m.identAddress)
	id("mask", func(c *interp.Call, conn *Conn) (string, error) { return Mask(c.Arg(0), interp.Int(c.Arg(1))), nil })
	id("comchan", m.identComchan)
	id("ial", m.identIAL)
	id("ialchan", m.identIALChan)
	for name, mode := range map[string]byte{"ibl": 'b', "iel": 'e', "iil": 'I', "iql": 'q'} {
		mode := mode
		id(name, func(c *interp.Call, conn *Conn) (string, error) { return identList(c, conn, mode) })
	}
	id("cid", func(c *interp.Call, conn *Conn) (string, error) { return strconv.Itoa(conn.ID), nil })
	id("scid", func(c *interp.Call, conn *Conn) (string, error) { return m.identScid(c, false) })
	id("scon", func(c *interp.Call, conn *Conn) (string, error) { return m.identScid(c, true) })
	id("server", func(c *interp.Call, conn *Conn) (string, error) {
		if conn.Status == Disconnected {
			return "", nil
		}
		return conn.ServerID, nil
	})
	id("serverip", func(c *interp.Call, conn *Conn) (string, error) { return conn.IP, nil })
	id("servertarget", func(c *interp.Call, conn *Conn) (string, error) { return conn.Spec.Host, nil })
	id("port", func(c *interp.Call, conn *Conn) (string, error) { return strconv.Itoa(conn.Spec.Port), nil })
	id("network", func(c *interp.Call, conn *Conn) (string, error) { return conn.Network(), nil })
	id("status", func(c *interp.Call, conn *Conn) (string, error) { return conn.Status.String(), nil })
	id("ssl", func(c *interp.Call, conn *Conn) (string, error) { return interp.Bool(conn.Spec.TLS), nil })
	id("ip", func(c *interp.Call, conn *Conn) (string, error) { return conn.LocalIP, nil })
	id("host", func(c *interp.Call, conn *Conn) (string, error) {
		if conn.LocalHost != "" {
			return conn.LocalHost, nil
		}
		return conn.LocalIP, nil
	})
	id("usermode", func(c *interp.Call, conn *Conn) (string, error) {
		if conn.UserModes == "" {
			return "", nil
		}
		return "+" + conn.UserModes, nil
	})
	id("chantypes", func(c *interp.Call, conn *Conn) (string, error) { return conn.Support.ChanTypes, nil })
	id("chanmodes", func(c *interp.Call, conn *Conn) (string, error) {
		return strings.Join(conn.Support.ChanModes[:], ","), nil
	})
	id("nickmode", func(c *interp.Call, conn *Conn) (string, error) { return conn.Support.PrefixModes, nil })
	id("prefix", func(c *interp.Call, conn *Conn) (string, error) { return conn.Support.Prefixes, nil })
	id("modespl", func(c *interp.Call, conn *Conn) (string, error) { return strconv.Itoa(conn.Support.ModesPerMsg), nil })
	id("away", func(c *interp.Call, conn *Conn) (string, error) { return interp.Bool(conn.Away), nil })
	id("awaymsg", func(c *interp.Call, conn *Conn) (string, error) { return conn.AwayMsg, nil })
	id("awaytime", func(c *interp.Call, conn *Conn) (string, error) {
		if !conn.Away {
			return "0", nil
		}
		return strconv.Itoa(int(time.Since(conn.AwayAt).Seconds())), nil
	})
	id("idle", func(c *interp.Call, conn *Conn) (string, error) {
		if conn.LastSent.IsZero() {
			return "0", nil
		}
		return strconv.Itoa(int(time.Since(conn.LastSent).Seconds())), nil
	})
	online := func(c *interp.Call, conn *Conn) (string, error) {
		if conn.Status != Connected {
			return "0", nil
		}
		return strconv.Itoa(int(time.Since(conn.ConnectedAt).Seconds())), nil
	}
	id("online", online)
	id("onlineserver", online)
	id("onlinetotal", func(c *interp.Call, conn *Conn) (string, error) {
		total := m.onlineTotal
		for _, x := range m.conns {
			if x.Status == Connected {
				total += time.Since(x.ConnectedAt)
			}
		}
		return strconv.Itoa(int(total.Seconds())), nil
	})
	id("mode", m.identMode)
	id("query", func(c *interp.Call, conn *Conn) (string, error) { return nthName(c, conn.queries.names()) })
	id("notify", m.identNotify)
	id("ignore", m.identIgnore)
	id("comchar", func(*interp.Call, *Conn) (string, error) { return "/", nil })
	id("dns", func(c *interp.Call, conn *Conn) (string, error) {
		v, _ := c.F.Event.Get("dns")
		addrs := strings.Fields(v)
		if !c.Paren {
			return v, nil
		}
		return nthName(c, addrs)
	})
}

// eventOr returns an event value when the identifier is used without
// parentheses, else calls fn.
func eventOr(c *interp.Call, name string, fn func() (string, error)) (string, error) {
	if !c.Paren {
		v, _ := c.F.Event.Get(name)
		return v, nil
	}
	return fn()
}

// nthName returns $x(N) from a list: N=0 is the count, else the Nth name;
// a name returns its position.
func nthName(c *interp.Call, names []string) (string, error) {
	arg := c.Arg(0)
	if n, ok := interp.ParseNumber(arg); ok {
		i := int(n)
		if i == 0 {
			return strconv.Itoa(len(names)), nil
		}
		if i < 1 || i > len(names) {
			return "", nil
		}
		return names[i-1], nil
	}
	for i, n := range names {
		if strings.EqualFold(n, arg) {
			return strconv.Itoa(i + 1), nil
		}
	}
	return "", nil
}

// identNick implements $nick and $nick(#,N|nick,include,exclude).
func (m *Manager) identNick(c *interp.Call, conn *Conn) (string, error) {
	return eventOr(c, "nick", func() (string, error) {
		ch := conn.Channels[conn.Fold(c.Arg(0))]
		if ch == nil {
			return "", nil
		}
		include, exclude := c.Arg(2), c.Arg(3)
		var list []*Member
		for _, mem := range ch.Nicks() {
			if memberIn(mem, include, true) && !memberIn(mem, exclude, false) {
				list = append(list, mem)
			}
		}
		arg := c.Arg(1)
		var mem *Member
		if n, ok := interp.ParseNumber(arg); ok {
			i := int(n)
			if i == 0 {
				return strconv.Itoa(len(list)), nil
			}
			if i < 1 || i > len(list) {
				return "", nil
			}
			mem = list[i-1]
		} else {
			for i, x := range list {
				if conn.Fold(x.Nick) == conn.Fold(arg) {
					mem = x
					if c.Prop == "" {
						return strconv.Itoa(i + 1), nil
					}
				}
			}
		}
		if mem == nil {
			return "", nil
		}
		switch strings.ToLower(c.Prop) {
		case "pnick":
			return conn.prefixesOf(mem) + mem.Nick, nil
		case "idle":
			return strconv.Itoa(int(time.Since(mem.LastSeen).Seconds())), nil
		case "color":
			return "", nil
		}
		return mem.Nick, nil
	})
}

// memberIn applies a $nick() aohvr filter.
func memberIn(mem *Member, filter string, emptyMeansAll bool) bool {
	if filter == "" {
		return emptyMeansAll
	}
	for _, f := range filter {
		switch f {
		case 'a':
			return true
		case 'r':
			if mem.Modes == "" {
				return true
			}
		default:
			if strings.ContainsRune(mem.Modes, f) {
				return true
			}
		}
	}
	return false
}

func (c *Conn) prefixesOf(mem *Member) string {
	var b strings.Builder
	for i := 0; i < len(c.Support.PrefixModes) && i < len(c.Support.Prefixes); i++ {
		if strings.IndexByte(mem.Modes, c.Support.PrefixModes[i]) >= 0 {
			b.WriteByte(c.Support.Prefixes[i])
		}
	}
	return b.String()
}

// identChan implements $chan and $chan(N|#).prop.
func (m *Manager) identChan(c *interp.Call, conn *Conn) (string, error) {
	return eventOr(c, "chan", func() (string, error) {
		names := make([]string, 0, len(conn.Channels))
		for _, ch := range conn.Channels {
			names = append(names, ch.Name)
		}
		sort.Strings(names)
		arg := c.Arg(0)
		var ch *Channel
		if n, ok := interp.ParseNumber(arg); ok {
			i := int(n)
			if i == 0 {
				return strconv.Itoa(len(names)), nil
			}
			if i < 1 || i > len(names) {
				return "", nil
			}
			ch = conn.Channels[conn.Fold(names[i-1])]
		} else {
			ch = conn.Channels[conn.Fold(arg)]
		}
		if ch == nil {
			return "", nil
		}
		switch strings.ToLower(c.Prop) {
		case "topic":
			return ch.Topic, nil
		case "mode":
			return ch.ModeString(), nil
		case "key":
			return ch.Key, nil
		case "limit":
			return ch.Limit, nil
		case "ial", "inwho":
			return interp.Bool(ch.Synced), nil
		case "status":
			return "joined", nil
		case "cid":
			return strconv.Itoa(conn.ID), nil
		case "logfile":
			return "", nil
		}
		return ch.Name, nil
	})
}

// identAddress implements $address and $address(nick,type).
func (m *Manager) identAddress(c *interp.Call, conn *Conn) (string, error) {
	return eventOr(c, "address", func() (string, error) {
		e := conn.ial(c.Arg(0))
		if e == nil || e.Host == "" {
			return "", nil
		}
		if len(c.Args) < 2 {
			return e.User + "@" + e.Host, nil
		}
		return Mask(e.Address(), interp.Int(c.Arg(1))), nil
	})
}

func (m *Manager) identComchan(c *interp.Call, conn *Conn) (string, error) {
	k := conn.Fold(c.Arg(0))
	var chans []*Channel
	for _, ch := range conn.Channels {
		if _, ok := ch.Members[k]; ok {
			chans = append(chans, ch)
		}
	}
	sort.Slice(chans, func(i, j int) bool { return chans[i].Name < chans[j].Name })
	n := interp.Int(c.Arg(1))
	if n == 0 {
		return strconv.Itoa(len(chans)), nil
	}
	if n < 1 || n > len(chans) {
		return "", nil
	}
	ch := chans[n-1]
	switch strings.ToLower(c.Prop) {
	case "op":
		return interp.Bool(conn.IsOp(ch.Name)), nil
	case "help":
		return interp.Bool(conn.hasMode(ch.Name, conn.Me, 'h')), nil
	case "voice":
		return interp.Bool(conn.hasMode(ch.Name, conn.Me, 'v')), nil
	}
	return ch.Name, nil
}

// ialMatches returns IAL entries whose address matches a nick or mask, in
// nick order.
func (c *Conn) ialMatches(pattern string) []*IALEntry {
	if !strings.ContainsAny(pattern, "!@*?") {
		if e := c.ial(pattern); e != nil {
			return []*IALEntry{e}
		}
		return nil
	}
	var out []*IALEntry
	for _, e := range c.IAL {
		if interp.WildMatch(pattern, e.Address(), false) {
			out = append(out, e)
		}
	}
	sort.Slice(out, func(i, j int) bool { return c.Fold(out[i].Nick) < c.Fold(out[j].Nick) })
	return out
}

func ialProp(c *interp.Call, e *IALEntry) string {
	switch strings.ToLower(c.Prop) {
	case "nick":
		return e.Nick
	case "user":
		return e.User
	case "host":
		return e.Host
	case "addr":
		return e.User + "@" + e.Host
	case "account":
		return e.Account
	case "away":
		return interp.Bool(e.Away)
	case "mark":
		return e.Mark
	}
	return e.Address()
}

func (m *Manager) identIAL(c *interp.Call, conn *Conn) (string, error) {
	if !c.Paren {
		return interp.Bool(true), nil
	}
	list := conn.ialMatches(c.Arg(0))
	n := 1
	if len(c.Args) > 1 {
		n = interp.Int(c.Arg(1))
	}
	if n == 0 {
		return strconv.Itoa(len(list)), nil
	}
	if n < 1 || n > len(list) {
		return "", nil
	}
	return ialProp(c, list[n-1]), nil
}

func (m *Manager) identIALChan(c *interp.Call, conn *Conn) (string, error) {
	ch := conn.Channels[conn.Fold(c.Arg(1))]
	if ch == nil {
		return "", nil
	}
	var list []*IALEntry
	for _, e := range conn.ialMatches(c.Arg(0)) {
		if _, ok := ch.Members[conn.Fold(e.Nick)]; ok {
			list = append(list, e)
		}
	}
	n := interp.Int(c.Arg(2))
	if n == 0 {
		return strconv.Itoa(len(list)), nil
	}
	if n < 1 || n > len(list) {
		return "", nil
	}
	e := list[n-1]
	if strings.EqualFold(c.Prop, "pnick") {
		return conn.prefixesOf(ch.Members[conn.Fold(e.Nick)]) + e.Nick, nil
	}
	return ialProp(c, e), nil
}

// identList implements $ibl, $iel, $iil and $iql.
func identList(c *interp.Call, conn *Conn, mode byte) (string, error) {
	ch := conn.Channels[conn.Fold(c.Arg(0))]
	if ch == nil {
		return "", nil
	}
	list := ch.Lists[mode]
	n := interp.Int(c.Arg(1))
	if n == 0 {
		return strconv.Itoa(len(list)), nil
	}
	if n < 1 || n > len(list) {
		return "", nil
	}
	e := list[n-1]
	switch strings.ToLower(c.Prop) {
	case "by":
		return e.By, nil
	case "date":
		return e.At.Format("02/01/2006 15:04:05"), nil
	case "ctime":
		return strconv.FormatInt(e.At.Unix(), 10), nil
	case "mask":
		if _, mask, ok := strings.Cut(e.Mask, ":"); ok && strings.HasPrefix(e.Mask, "$") {
			return mask, nil
		}
		return e.Mask, nil
	case "prefix":
		if p, _, ok := strings.Cut(e.Mask, ":"); ok && strings.HasPrefix(e.Mask, "$") {
			return p + ":", nil
		}
		return "", nil
	}
	return e.Mask, nil
}

// identScid implements $scid(N)[.ident] and $scon(N)[.ident].
func (m *Manager) identScid(c *interp.Call, byIndex bool) (string, error) {
	if !c.Paren {
		return strconv.Itoa(m.FrameConn(c.F).ID), nil
	}
	n := interp.Int(c.Arg(0))
	if n == 0 {
		return strconv.Itoa(len(m.conns)), nil
	}
	var target *Conn
	switch {
	case n == -1:
		target = m.Active()
	case byIndex:
		if n >= 1 && n <= len(m.conns) {
			target = m.conns[n-1]
		}
	default:
		target = m.ByID(n)
	}
	if target == nil {
		return "", nil
	}
	if c.Prop == "" {
		return strconv.Itoa(target.ID), nil
	}
	saved := c.F.Cid
	c.F.Cid = target.ID
	defer func() { c.F.Cid = saved }()
	return c.F.Eval("$" + c.Prop)
}

// identMode implements $mode(N).op|deop|ban|unban|voice|devoice|help|dehelp.
func (m *Manager) identMode(c *interp.Call, conn *Conn) (string, error) {
	if !c.Paren {
		v, _ := c.F.Event.Get("mode")
		return v, nil
	}
	prop := strings.ToLower(c.Prop)
	var list []string
	if prop == "" {
		for _, p := range []string{"op", "deop", "voice", "devoice", "help", "dehelp", "ban", "unban"} {
			v, _ := c.F.Event.Get("mode:" + p)
			list = append(list, strings.Fields(v)...)
		}
	} else {
		v, _ := c.F.Event.Get("mode:" + prop)
		list = strings.Fields(v)
	}
	return nthName(c, list)
}

func (m *Manager) identNotify(c *interp.Call, conn *Conn) (string, error) {
	if !c.Paren {
		return interp.Bool(m.Lists.NotifyOn), nil
	}
	names := make([]string, len(m.Lists.Notify))
	for i, n := range m.Lists.Notify {
		names[i] = n.Nick
	}
	v, err := nthName(c, names)
	if c.Prop == "" || err != nil {
		return v, err
	}
	i := interp.Int(c.Arg(0))
	if _, ok := interp.ParseNumber(c.Arg(0)); !ok {
		i = interp.Int(v)
	}
	if i < 1 || i > len(m.Lists.Notify) {
		return "", nil
	}
	e := m.Lists.Notify[i-1]
	switch strings.ToLower(c.Prop) {
	case "ison":
		return interp.Bool(e.On[conn.ID]), nil
	case "note":
		return e.Note, nil
	case "network":
		return e.Network, nil
	case "addr":
		if x := conn.ial(e.Nick); x != nil {
			return x.User + "@" + x.Host, nil
		}
	}
	return "", nil
}

func (m *Manager) identIgnore(c *interp.Call, conn *Conn) (string, error) {
	if !c.Paren {
		return interp.Bool(m.Lists.IgnoreOn), nil
	}
	masks := make([]string, len(m.Lists.Ignore))
	for i, e := range m.Lists.Ignore {
		masks[i] = e.Mask
	}
	v, err := nthName(c, masks)
	if err != nil || c.Prop == "" {
		return v, err
	}
	i := interp.Int(c.Arg(0))
	if i < 1 || i > len(m.Lists.Ignore) {
		return "", nil
	}
	e := m.Lists.Ignore[i-1]
	switch strings.ToLower(c.Prop) {
	case "type":
		return e.Flags, nil
	case "secs":
		if e.Until.IsZero() {
			return "0", nil
		}
		return strconv.Itoa(int(time.Until(e.Until).Seconds())), nil
	case "network":
		return e.Network, nil
	}
	return "", nil
}

func (m *Manager) installOperators() {
	op := func(name string, fn func(conn *Conn, v1, v2 string) bool) {
		m.rt.RegisterOperator(name, func(f *interp.Frame, v1, v2 string) (bool, error) {
			return fn(m.FrameConn(f), v1, v2), nil
		})
	}
	member := func(mode byte) func(*Conn, string, string) bool {
		return func(conn *Conn, nick, channel string) bool {
			if mode == 0 {
				ch := conn.Channels[conn.Fold(channel)]
				return ch != nil && ch.Members[conn.Fold(nick)] != nil
			}
			return conn.hasMode(channel, nick, mode)
		}
	}
	op("ison", member(0))
	op("isop", member('o'))
	op("ishop", member('h'))
	op("isvoice", member('v'))
	op("isreg", func(conn *Conn, nick, channel string) bool {
		ch := conn.Channels[conn.Fold(channel)]
		if ch == nil {
			return false
		}
		mem := ch.Members[conn.Fold(nick)]
		return mem != nil && mem.Modes == ""
	})
	op("ischan", func(conn *Conn, v1, _ string) bool { return conn.Channels[conn.Fold(v1)] != nil })
	list := func(mode byte) func(*Conn, string, string) bool {
		return func(conn *Conn, mask, channel string) bool {
			ch := conn.Channels[conn.Fold(channel)]
			if ch == nil {
				return false
			}
			for _, e := range ch.Lists[mode] {
				if strings.EqualFold(e.Mask, mask) {
					return true
				}
			}
			return false
		}
	}
	op("isban", list('b'))
	op("isexcept", list('e'))
	op("isinvite", list('I'))
	op("isquiet", list('q'))
	op("isignore", func(conn *Conn, v1, v2 string) bool {
		addr := v1
		if e := conn.ial(v1); e != nil {
			addr = e.Address()
		}
		kind := byte('p')
		if v2 != "" {
			kind = v2[0]
		}
		return m.ignored(addr, kind)
	})
	op("isnotify", func(conn *Conn, v1, _ string) bool {
		for _, n := range m.Lists.Notify {
			if strings.EqualFold(n.Nick, v1) {
				return true
			}
		}
		return false
	})
}
