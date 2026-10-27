package irc

import (
	"net/netip"
	"strings"
	"time"
	"unicode"

	"github.com/ValwareIRC/msl-runtime/internal/interp"
)

// orderedSet is a small insertion-ordered set of names keyed by folded name.
type orderedSet struct {
	keys []string
	vals map[string]string
}

func (s *orderedSet) add(key, val string) {
	if s.vals == nil {
		s.vals = map[string]string{}
	}
	if _, ok := s.vals[key]; !ok {
		s.keys = append(s.keys, key)
	}
	s.vals[key] = val
}

func (s *orderedSet) has(key string) bool { _, ok := s.vals[key]; return ok }

func (s *orderedSet) remove(key string) bool {
	if _, ok := s.vals[key]; !ok {
		return false
	}
	delete(s.vals, key)
	for i, k := range s.keys {
		if k == key {
			s.keys = append(s.keys[:i], s.keys[i+1:]...)
			break
		}
	}
	return true
}

func (s *orderedSet) names() []string {
	out := make([]string, 0, len(s.keys))
	for _, k := range s.keys {
		out = append(out, s.vals[k])
	}
	return out
}

// Mask applies one of mIRC's address mask types (0-19) to nick!user@host.
func Mask(addr string, typ int) string {
	nick, user, host := SplitAddress(addr)
	digits := typ >= 10
	typ %= 10
	user = strings.TrimLeft(user, "~")
	wildHost := host
	if typ == 3 || typ == 4 || typ == 8 || typ == 9 {
		wildHost = wildcardHost(host)
	}
	if digits {
		wildHost = strings.Map(func(r rune) rune {
			if unicode.IsDigit(r) {
				return '?'
			}
			return r
		}, wildHost)
	}
	switch typ {
	case 0:
		return "*!" + user + "@" + wildHost
	case 1, 3:
		return "*!*" + user + "@" + wildHost
	case 2, 4:
		return "*!*@" + wildHost
	case 5:
		return nick + "!" + user + "@" + wildHost
	case 6, 8:
		return nick + "!*" + user + "@" + wildHost
	}
	return nick + "!*@" + wildHost
}

// wildcardHost replaces the most specific part of a host with *: the first
// label of a name, or the last part of an IP address.
func wildcardHost(host string) string {
	if a, err := netip.ParseAddr(host); err == nil {
		if a.Is4() {
			return host[:strings.LastIndexByte(host, '.')+1] + "*"
		}
		return host[:strings.LastIndexByte(host, ':')+1] + "*"
	}
	if i := strings.IndexByte(host, '.'); i >= 0 && strings.Count(host, ".") > 1 {
		return "*" + host[i:]
	}
	return host
}

// notifyEntry is a nick in the notify list.
type notifyEntry struct {
	Nick    string
	Note    string
	Network string
	On      map[int]bool
}

// ignoreEntry is a mask in the ignore list with its type flags.
type ignoreEntry struct {
	Mask    string
	Flags   string // p c n t i k d, x for exclude
	Network string
	Until   time.Time
}

// Lists are user lists shared by all connections.
type Lists struct {
	Notify    []*notifyEntry
	NotifyOn  bool
	Ignore    []*ignoreEntry
	IgnoreOn  bool
	IdentUser string
}

func (m *Manager) notifyState(c *Conn, nick string, on bool) {
	for _, n := range m.Lists.Notify {
		if strings.EqualFold(n.Nick, nick) {
			if n.On == nil {
				n.On = map[int]bool{}
			}
			n.On[c.ID] = on
		}
	}
}

// cmdNotify implements /notify [-shr] [on|off|nick] [network] [note].
func (m *Manager) cmdNotify(cc *interp.CmdCall, c *Conn) error {
	sw, rest := cc.Switches()
	f := strings.Fields(rest)
	if len(f) == 0 {
		return nil
	}
	switch strings.ToLower(f[0]) {
	case "on":
		m.Lists.NotifyOn = true
		return nil
	case "off":
		m.Lists.NotifyOn = false
		return nil
	}
	nick := f[0]
	if strings.Contains(sw, "r") {
		kept := m.Lists.Notify[:0]
		for _, n := range m.Lists.Notify {
			if !strings.EqualFold(n.Nick, nick) {
				kept = append(kept, n)
			}
		}
		m.Lists.Notify = kept
		if _, ok := c.Support.Tokens["MONITOR"]; ok {
			c.Send("MONITOR - " + nick)
		}
		return nil
	}
	e := &notifyEntry{Nick: nick, On: map[int]bool{}}
	if len(f) > 1 {
		e.Network = f[1]
		e.Note = strings.Join(f[2:], " ")
	}
	m.Lists.Notify = append(m.Lists.Notify, e)
	if _, ok := c.Support.Tokens["MONITOR"]; ok && c.Status == Connected {
		c.Send("MONITOR + " + nick)
	}
	return nil
}

// cmdIgnore implements /ignore [-lpcntikdwxu#r] <nick|address> [type] [network].
func (m *Manager) cmdIgnore(cc *interp.CmdCall, c *Conn) error {
	sw, rest := cc.Switches()
	f := strings.Fields(rest)
	if len(f) == 0 {
		return cc.InvalidParams()
	}
	switch strings.ToLower(f[0]) {
	case "on", "off":
		m.Lists.IgnoreOn = strings.EqualFold(f[0], "on")
		return nil
	}
	mask := f[0]
	if len(f) > 1 {
		if n, ok := interp.ParseNumber(f[1]); ok {
			if e := c.ial(mask); e != nil {
				mask = Mask(e.Address(), int(n))
			}
		}
	}
	if !strings.ContainsAny(mask, "!@*") {
		mask += "!*@*"
	}
	if strings.Contains(sw, "r") {
		kept := m.Lists.Ignore[:0]
		for _, e := range m.Lists.Ignore {
			if !strings.EqualFold(e.Mask, mask) {
				kept = append(kept, e)
			}
		}
		m.Lists.Ignore = kept
		return nil
	}
	e := &ignoreEntry{Mask: mask, Flags: strings.Trim(sw, "lwu0123456789")}
	if i := strings.IndexByte(sw, 'u'); i >= 0 {
		e.Until = time.Now().Add(time.Duration(interp.Int(sw[i+1:])) * time.Second)
	}
	m.Lists.Ignore = append(m.Lists.Ignore, e)
	return nil
}

// ignored reports whether an address is ignored for a kind of message
// (p private, c channel, n notice, t ctcp, i invite, d dcc).
func (m *Manager) ignored(addr string, kind byte) bool {
	if !m.Lists.IgnoreOn {
		return false
	}
	now := time.Now()
	hit := false
	for _, e := range m.Lists.Ignore {
		if !e.Until.IsZero() && now.After(e.Until) {
			continue
		}
		if !interp.WildMatch(e.Mask, addr, false) {
			continue
		}
		flags := strings.Trim(e.Flags, "x")
		if flags != "" && strings.IndexByte(flags, kind) < 0 {
			continue
		}
		if strings.Contains(e.Flags, "x") {
			return false
		}
		hit = true
	}
	return hit
}
