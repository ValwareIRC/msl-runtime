// Package control provides the runtime's own control features: the users
// list and access levels, auto-op/voice/protect lists, timers, signals,
// groups, script loading, persistence, and system identifiers.
package control

import (
	"os"
	"strconv"
	"strings"

	"github.com/ValwareIRC/msl-runtime/internal/interp"
	"github.com/ValwareIRC/msl-runtime/internal/irc"
	"github.com/ValwareIRC/msl-runtime/internal/storage"
)

// User is an entry in the users list: levels for a nick or address mask.
type User struct {
	Levels []string
	Mask   string
	Info   string
}

// autoEntry is an auto-op, auto-voice or protect list entry.
type autoEntry struct {
	Mask     string
	Channels string
	Network  string
}

// Control is the per-runtime state of this package.
type Control struct {
	rt  *interp.Runtime
	irc *irc.Manager
	st  *storage.Storage

	Users    []*User
	lists    map[string]*autoList // aop, avoice, protect
	timers   []*timer
	ltimer   string
	urls     []*urlJob
	nextURL  int
	starting bool
	exiting  int
	settings settings
}

type autoList struct {
	on      bool
	entries []*autoEntry
}

type controlKey struct{}

// Install registers the package on rt.
func Install(rt *interp.Runtime, ircm *irc.Manager) *Control {
	c := &Control{rt: rt, irc: ircm, st: storage.From(rt), lists: map[string]*autoList{
		"aop": {on: true}, "avoice": {on: true}, "protect": {on: true},
	}, settings: defaultSettings()}
	interp.Ext(rt, controlKey{}, func() *Control { return c })
	rt.SetLevels(c.levels)
	c.installUsers()
	c.installTimers()
	c.installScripts()
	c.installSystem()
	c.installURL()
	if ircm != nil {
		ircm.OnJoin = append(ircm.OnJoin, c.autoOnJoin)
		ircm.OnDisconnect = append(ircm.OnDisconnect, c.stopOnline)
	}
	return c
}

// maskFor completes a users-list mask: a bare nick matches nick!*@*.
func maskFor(m string) string {
	if !strings.ContainsAny(m, "!@") {
		return m + "!*@*"
	}
	return m
}

// levels returns a user's levels for event dispatch.
func (c *Control) levels(address string) ([]string, string) {
	for _, u := range c.Users {
		if interp.WildMatch(maskFor(u.Mask), address, false) {
			return u.Levels, u.Mask
		}
	}
	return nil, ""
}

func (c *Control) findUser(mask string) *User {
	for _, u := range c.Users {
		if strings.EqualFold(u.Mask, mask) {
			return u
		}
	}
	return nil
}

func splitLevels(s string) []string {
	var out []string
	for _, l := range strings.Split(s, ",") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return out
}

func hasLevel(levels []string, l string) bool {
	for _, x := range levels {
		if strings.EqualFold(strings.TrimPrefix(x, "="), strings.TrimPrefix(l, "=")) {
			return true
		}
	}
	return false
}

// addressOf returns a nick's address from the IAL, masked by type when given.
func (c *Control) addressOf(f *interp.Frame, nick string, typ int) (string, bool) {
	if c.irc == nil {
		return "", false
	}
	matches, _ := f.Eval("$address(" + nick + ",5)")
	if matches == "" {
		return "", false
	}
	if typ < 0 {
		return matches, true
	}
	return irc.Mask(matches, typ), true
}

func (c *Control) installUsers() {
	rt := c.rt
	rt.RegisterCommand("auser", interp.Command{Fn: func(cc *interp.CmdCall) error { return c.cmdAuser(cc, false) }})
	rt.RegisterCommand("guser", interp.Command{Fn: func(cc *interp.CmdCall) error { return c.cmdAuser(cc, true) }})
	rt.RegisterCommand("iuser", interp.Command{Fn: func(cc *interp.CmdCall) error {
		mask, info, _ := strings.Cut(strings.TrimSpace(cc.Args), " ")
		if u := c.findUser(mask); u != nil {
			u.Info = info
		}
		return nil
	}})
	rt.RegisterCommand("ruser", interp.Command{Fn: c.cmdRuser})
	rt.RegisterCommand("rlevel", interp.Command{Fn: c.cmdRlevel})
	rt.RegisterCommand("ulist", interp.Command{Fn: c.cmdUlist})
	rt.RegisterCommand("flush", interp.Command{Fn: c.cmdFlush})
	rt.RegisterCommand("dlevel", interp.Command{Fn: func(cc *interp.CmdCall) error {
		if l := strings.TrimSpace(cc.Args); l != "" {
			rt.SetDefaultLevel(l)
		}
		return nil
	}})
	rt.RegisterIdent("dlevel", func(*interp.Call) (string, error) { return rt.DefaultLevel(), nil })
	rt.RegisterIdent("ulevel", func(cl *interp.Call) (string, error) { v, _ := cl.F.Event.Get("ulevel"); return v, nil })
	rt.RegisterIdent("level", func(cl *interp.Call) (string, error) {
		ls, _ := c.levels(maskFor(cl.Arg(0)))
		if ls == nil {
			if u := c.findUser(cl.Arg(0)); u != nil {
				ls = u.Levels
			}
		}
		return strings.Join(ls, ","), nil
	})
	rt.RegisterIdent("ulist", c.identUlist)
	for _, name := range []string{"aop", "avoice", "protect"} {
		name := name
		rt.RegisterCommand(name, interp.Command{Fn: func(cc *interp.CmdCall) error { return c.cmdAutoList(cc, name) }})
		rt.RegisterIdent(name, func(cl *interp.Call) (string, error) { return c.identAutoList(cl, name) })
		rt.RegisterOperator("is"+name, func(f *interp.Frame, v1, v2 string) (bool, error) {
			addr := v1
			if !strings.ContainsAny(v1, "!@") {
				if a, ok := c.addressOf(f, v1, -1); ok {
					addr = a
				} else {
					addr = v1 + "!*@*"
				}
			}
			return c.autoMatch(name, addr, v2, "") != nil, nil
		})
	}
}

// cmdAuser implements /auser and /guser [-a] <levels> <nick|address> [type] [info].
func (c *Control) cmdAuser(cc *interp.CmdCall, lookup bool) error {
	sw, rest := cc.Switches()
	f := strings.Fields(rest)
	if len(f) < 2 {
		return cc.InvalidParams()
	}
	levels, mask := splitLevels(f[0]), f[1]
	info := strings.Join(f[2:], " ")
	if lookup {
		typ := -1
		if len(f) > 2 {
			if n, err := strconv.Atoi(f[2]); err == nil {
				typ, info = n, strings.Join(f[3:], " ")
			}
		}
		if typ < 0 {
			typ = 2
		}
		addr, ok := c.addressOf(cc.F, mask, typ)
		if !ok {
			return cc.Errorf("%s not found in internal address list", mask)
		}
		mask = addr
	}
	u := c.findUser(mask)
	if u != nil && strings.Contains(sw, "a") {
		for _, l := range levels {
			if strings.HasPrefix(l, "=") && len(u.Levels) > 0 {
				continue // =N keeps the existing general level
			}
			if !hasLevel(u.Levels, l) {
				u.Levels = append(u.Levels, l)
			}
		}
	} else if u != nil {
		u.Levels, u.Info = levels, info
	} else {
		c.Users = append(c.Users, &User{Levels: levels, Mask: mask, Info: info})
	}
	return nil
}

// cmdRuser implements /ruser [levels] <nick|address> [type].
func (c *Control) cmdRuser(cc *interp.CmdCall) error {
	f := cc.Fields()
	if len(f) == 0 {
		return cc.InvalidParams()
	}
	var levels []string
	if len(f) > 1 && (strings.Contains(f[0], ",") || isNumberish(f[0])) {
		levels, f = splitLevels(f[0]), f[1:]
	}
	mask := f[0]
	if len(f) > 1 {
		if addr, ok := c.addressOf(cc.F, mask, interp.Int(f[1])); ok {
			mask = addr
		}
	}
	kept := c.Users[:0]
	for _, u := range c.Users {
		match := strings.EqualFold(u.Mask, mask) ||
			strings.HasSuffix(mask, "!") && strings.HasPrefix(strings.ToLower(u.Mask), strings.ToLower(mask))
		if match && levels != nil {
			var left []string
			for _, l := range u.Levels {
				if !hasLevel(levels, l) {
					left = append(left, l)
				}
			}
			u.Levels = left
			match = len(left) == 0
		}
		if !match {
			kept = append(kept, u)
		}
	}
	c.Users = kept
	return nil
}

func isNumberish(s string) bool {
	_, ok := interp.ParseNumber(strings.TrimPrefix(s, "="))
	return ok
}

// cmdRlevel implements /rlevel [-r] <levels>.
func (c *Control) cmdRlevel(cc *interp.CmdCall) error {
	sw, rest := cc.Switches()
	levels := splitLevels(strings.TrimSpace(rest))
	kept := c.Users[:0]
	for _, u := range c.Users {
		if strings.Contains(sw, "r") {
			var left []string
			for _, l := range u.Levels {
				if !hasLevel(levels, l) {
					left = append(left, l)
				}
			}
			u.Levels = left
			if len(left) > 0 {
				kept = append(kept, u)
			}
			continue
		}
		if len(u.Levels) > 0 && hasLevel(levels, u.Levels[0]) {
			continue
		}
		kept = append(kept, u)
	}
	c.Users = kept
	return nil
}

// cmdUlist implements /ulist [<|>]<level>: matching entries go to the status window.
func (c *Control) cmdUlist(cc *interp.CmdCall) error {
	arg := strings.TrimSpace(cc.Args)
	for _, u := range c.Users {
		if ulistMatch(u, arg) {
			c.rt.Out.Echo(interp.StatusWindow, strings.Join(u.Levels, ",")+":"+u.Mask+" "+u.Info)
		}
	}
	return nil
}

func ulistMatch(u *User, arg string) bool {
	if arg == "" || len(u.Levels) == 0 {
		return arg == ""
	}
	general := interp.Num(strings.TrimPrefix(u.Levels[0], "="))
	switch {
	case strings.HasPrefix(arg, "<"):
		return general <= interp.Num(arg[1:])
	case strings.HasPrefix(arg, ">"):
		return general >= interp.Num(arg[1:])
	}
	return hasLevel(u.Levels, arg)
}

// cmdFlush implements /flush [-l] [levels]: drops nick-only users who share
// no channel with the runtime's user.
func (c *Control) cmdFlush(cc *interp.CmdCall) error {
	sw, rest := cc.Switches()
	levels := splitLevels(strings.TrimSpace(rest))
	kept := c.Users[:0]
	for _, u := range c.Users {
		if strings.ContainsAny(u.Mask, "!@*?") || (levels != nil && !anyLevel(u.Levels, levels)) {
			kept = append(kept, u)
			continue
		}
		if on, _ := cc.F.Eval("$comchan(" + u.Mask + ",0)"); interp.Int(on) > 0 {
			kept = append(kept, u)
			continue
		}
		if strings.Contains(sw, "l") && levels != nil {
			var left []string
			for _, l := range u.Levels {
				if !hasLevel(levels, l) {
					left = append(left, l)
				}
			}
			if u.Levels = left; len(left) > 0 {
				kept = append(kept, u)
			}
		}
	}
	c.Users = kept
	return nil
}

func anyLevel(have, want []string) bool {
	for _, w := range want {
		if hasLevel(have, w) {
			return true
		}
	}
	return false
}

// identUlist implements $ulist(address, L, N).info.
func (c *Control) identUlist(cl *interp.Call) (string, error) {
	pattern := maskFor(cl.Arg(0))
	level := cl.Arg(1)
	n := 1
	if len(cl.Args) > 2 {
		n = interp.Int(cl.Arg(2))
	}
	var list []*User
	for _, u := range c.Users {
		if (interp.WildMatch(pattern, maskFor(u.Mask), false) || interp.WildMatch(maskFor(u.Mask), pattern, false)) &&
			(level == "" || hasLevel(u.Levels, level)) {
			list = append(list, u)
		}
	}
	if n == 0 {
		return strconv.Itoa(len(list)), nil
	}
	if n < 1 || n > len(list) {
		return "", nil
	}
	if strings.EqualFold(cl.Prop, "info") {
		return list[n-1].Info, nil
	}
	return list[n-1].Mask, nil
}

// cmdAutoList implements /aop, /avoice and /protect
// [-lrw] <on|off|nick|address> [#channels] [type] [network].
func (c *Control) cmdAutoList(cc *interp.CmdCall, name string) error {
	sw, rest := cc.Switches()
	f := strings.Fields(rest)
	list := c.lists[name]
	if len(f) == 0 {
		return nil
	}
	switch strings.ToLower(f[0]) {
	case "on":
		list.on = true
		return nil
	case "off":
		list.on = false
		return nil
	}
	e := &autoEntry{Mask: f[0]}
	for _, a := range f[1:] {
		switch {
		case strings.HasPrefix(a, "#") || strings.Contains(a, ",#"):
			e.Channels = a
		case isNumberish(a):
			if addr, ok := c.addressOf(cc.F, e.Mask, interp.Int(a)); ok {
				e.Mask = addr
			}
		default:
			e.Network = a
		}
	}
	e.Mask = maskFor(e.Mask)
	if strings.Contains(sw, "w") {
		e.Network = ""
	}
	if strings.Contains(sw, "l") {
		for _, x := range list.entries {
			c.rt.Out.Echo(interp.StatusWindow, x.Mask+" "+x.Channels+" "+x.Network)
		}
		return nil
	}
	kept := list.entries[:0]
	for _, x := range list.entries {
		if !strings.EqualFold(x.Mask, e.Mask) {
			kept = append(kept, x)
		}
	}
	list.entries = kept
	if !strings.Contains(sw, "r") {
		list.entries = append(list.entries, e)
	}
	return nil
}

func (c *Control) autoMatch(name, address, channel, network string) *autoEntry {
	list := c.lists[name]
	for _, e := range list.entries {
		if !interp.WildMatch(e.Mask, address, false) {
			continue
		}
		if channel != "" && e.Channels != "" && !matchChannelList(e.Channels, channel) {
			continue
		}
		if network != "" && e.Network != "" && !strings.EqualFold(e.Network, network) {
			continue
		}
		return e
	}
	return nil
}

func matchChannelList(list, channel string) bool {
	for _, ch := range strings.Split(list, ",") {
		if interp.WildMatch(ch, channel, false) {
			return true
		}
	}
	return false
}

func (c *Control) identAutoList(cl *interp.Call, name string) (string, error) {
	list := c.lists[name]
	if !cl.Paren {
		return interp.Bool(list.on), nil
	}
	var e *autoEntry
	if n, ok := interp.ParseNumber(cl.Arg(0)); ok {
		i := int(n)
		if i == 0 {
			return strconv.Itoa(len(list.entries)), nil
		}
		if i < 1 || i > len(list.entries) {
			return "", nil
		}
		e = list.entries[i-1]
	} else {
		e = c.autoMatch(name, maskFor(cl.Arg(0)), cl.Arg(1), "")
	}
	if e == nil {
		return "", nil
	}
	switch strings.ToLower(cl.Prop) {
	case "type":
		return e.Channels, nil
	case "network":
		return e.Network, nil
	}
	return e.Mask, nil
}

// autoOnJoin applies the auto-op and auto-voice lists when someone joins.
func (c *Control) autoOnJoin(conn *irc.Conn, nick, address, channel string) {
	if strings.EqualFold(nick, conn.Me) || !conn.IsOp(channel) {
		return
	}
	for _, kind := range []struct {
		list string
		mode string
	}{{"aop", "+o"}, {"avoice", "+v"}} {
		if c.lists[kind.list].on && c.autoMatch(kind.list, address, channel, conn.Network()) != nil {
			conn.Send("MODE " + channel + " " + kind.mode + " " + nick)
		}
	}
}

// saveUsers writes the users list in mIRC's users.ini layout.
func (c *Control) saveUsers(path string) error {
	var b strings.Builder
	b.WriteString("[users]\r\n")
	for i, u := range c.Users {
		line := strings.Join(u.Levels, ",") + ":" + u.Mask
		if u.Info != "" {
			line += " " + u.Info
		}
		b.WriteString("n" + strconv.Itoa(i) + "=" + line + "\r\n")
	}
	return os.WriteFile(path, []byte(b.String()), 0o644)
}

// loadUsers reads a users.ini file, replacing the users list.
func (c *Control) loadUsers(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	c.Users = nil
	for _, line := range strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n") {
		_, val, ok := strings.Cut(line, "=")
		if !ok || strings.HasPrefix(strings.TrimSpace(line), "[") {
			continue
		}
		levels, rest, ok := strings.Cut(val, ":")
		if !ok {
			continue
		}
		mask, info, _ := strings.Cut(rest, " ")
		c.Users = append(c.Users, &User{Levels: splitLevels(levels), Mask: mask, Info: info})
	}
	return nil
}
