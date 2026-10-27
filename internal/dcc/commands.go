package dcc

import (
	"bufio"
	"net"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/ValwareIRC/msl-runtime/internal/interp"
	"github.com/ValwareIRC/msl-runtime/internal/irc"
)

func (d *DCC) installCommands() {
	rt := d.rt
	rt.RegisterCommand("dcc", interp.Command{Fn: d.cmdDCC})
	rt.RegisterCommand("fserve", interp.Command{Fn: d.cmdFserve})
	rt.RegisterCommand("dccserver", interp.Command{Fn: d.cmdDCCServer})
	rt.RegisterCommand("creq", interp.Command{Fn: func(c *interp.CmdCall) error { return setRequest(c, &d.Settings.ChatRequest) }})
	rt.RegisterCommand("sreq", interp.Command{Fn: func(c *interp.CmdCall) error { return setRequest(c, &d.Settings.SendRequest) }})
	rt.RegisterCommand("pdcc", interp.Command{Fn: func(*interp.CmdCall) error { return nil }})
	rt.RegisterCommand("close", interp.Command{Fn: d.cmdClose})
}

func setRequest(c *interp.CmdCall, dst *string) error {
	for _, w := range c.Fields() {
		switch w = strings.ToLower(w); w {
		case "ask", "auto", "ignore":
			*dst = w
		}
	}
	return nil
}

func (d *DCC) conn(f *interp.Frame) *irc.Conn { return d.irc.FrameConn(f) }

// cmdDCC implements /dcc send|chat|get|reject|trust|ignore|passive|nick|maxcps.
func (d *DCC) cmdDCC(c *interp.CmdCall) error {
	sub, rest, _ := strings.Cut(strings.TrimSpace(c.Args), " ")
	sw, rest := interp.SplitSwitches(rest)
	f := strings.Fields(rest)
	switch strings.ToLower(sub) {
	case "send":
		if len(f) < 2 {
			return c.InvalidParams()
		}
		target := f[0]
		for _, name := range splitFiles(strings.TrimSpace(strings.TrimPrefix(rest, target))) {
			path := d.path(name)
			if net.ParseIP(target) != nil {
				d.serverSend(c.F, target, path)
				continue
			}
			if err := d.offerSend(d.conn(c.F), target, path, d.Settings.Passive || strings.Contains(sw, "p")); err != nil {
				return c.Errorf("%s", err.Error())
			}
		}
	case "chat":
		if len(f) < 1 {
			return c.InvalidParams()
		}
		if net.ParseIP(f[0]) != nil {
			d.serverChat(c.F, f[0], "100", "chat")
			return nil
		}
		return d.offerChat(c, f[0], "chat", "", "", 0)
	case "fserve":
		if len(f) > 0 && net.ParseIP(f[0]) != nil {
			d.serverChat(c.F, f[0], "110", "fserve")
		}
	case "get":
		if c.F.Event == nil || len(f) == 0 {
			return c.InvalidParams()
		}
		c.F.Event.Values["dcc:get"] = strings.Trim(rest, `" `)
	case "reject":
		if c.F.Event != nil {
			c.F.Event.Values["dcc:reject"] = "1"
		}
	case "trust":
		return d.cmdTrust(c, sw, f)
	case "ignore":
		for _, w := range f {
			switch strings.ToLower(w) {
			case "on":
				d.Settings.IgnoreOn = true
			case "off":
				d.Settings.IgnoreOn = false
			case "accept", "ignore":
				d.Settings.IgnoreMode = strings.ToLower(w)
			default:
				if strings.Contains(sw, "r") {
					d.Settings.IgnoreTypes = removeFold(d.Settings.IgnoreTypes, w)
				} else {
					d.Settings.IgnoreTypes = append(d.Settings.IgnoreTypes, w)
				}
			}
		}
	case "passive":
		if len(f) > 0 {
			d.Settings.Passive = strings.EqualFold(f[0], "on")
		}
	case "nick":
		if len(f) < 2 {
			return c.InvalidParams()
		}
		for _, s := range d.sessions {
			if strings.EqualFold(s.Nick, f[0]) && (sw == "" || strings.ContainsRune(sw, rune(kindLetter(s.Kind)))) {
				s.Nick = f[1]
			}
		}
	case "maxcps":
		if len(f) > 0 {
			d.Settings.MaxCPS = interp.Int(f[0])
		}
	default:
		return c.InvalidParams()
	}
	return nil
}

func kindLetter(kind string) byte {
	switch kind {
	case "send":
		return 's'
	case "get":
		return 'g'
	case "chat":
		return 'c'
	}
	return 'f'
}

// splitFiles splits /dcc send filenames, keeping quoted names whole.
func splitFiles(s string) []string {
	var out []string
	for _, f := range splitDCC(s) {
		out = append(out, strings.Trim(f, `"`))
	}
	return out
}

func removeFold(list []string, v string) []string {
	kept := list[:0]
	for _, x := range list {
		if !strings.EqualFold(x, v) {
			kept = append(kept, x)
		}
	}
	return kept
}

func (d *DCC) cmdTrust(c *interp.CmdCall, sw string, f []string) error {
	if len(f) == 0 {
		return nil
	}
	item := f[0]
	switch strings.ToLower(item) {
	case "on", "off":
		return nil
	}
	if strings.Contains(sw, "r") {
		d.Settings.Trust = removeFold(d.Settings.Trust, item)
		return nil
	}
	d.Settings.Trust = append(d.Settings.Trust, item)
	return nil
}

// offerChat sends a DCC CHAT offer (also used for /fserve) and waits.
func (d *DCC) offerChat(c *interp.CmdCall, nick, kind, root, welcome string, maxGets int) error {
	conn := d.conn(c.F)
	s := d.add(&Session{Kind: kind, Nick: nick, Status: "waiting", cid: conn.ID, Root: root, File: welcome, maxGets: maxGets})
	if d.Settings.Passive {
		s.Passive, s.Token = true, strconv.FormatInt(time.Now().UnixNano()%1e9, 10)
		return conn.Send("PRIVMSG " + nick + " :" + irc.EncodeCTCP("DCC", "CHAT chat "+ipArg(localIP(conn))+" 0 "+s.Token))
	}
	ln, port, err := listen()
	if err != nil {
		d.remove(s)
		return c.Errorf("%s", err.Error())
	}
	s.ln, s.Port = ln, port
	d.acceptOne(s, func(nc net.Conn) { d.startChat(s, nc) })
	return conn.Send("PRIVMSG " + nick + " :" + irc.EncodeCTCP("DCC", "CHAT chat "+ipArg(localIP(conn))+" "+strconv.Itoa(port)))
}

// cmdFserve implements /fserve <nick> <maxgets> <homedir> [textfile].
func (d *DCC) cmdFserve(c *interp.CmdCall) error {
	f := c.Fields()
	if len(f) < 3 {
		return c.InvalidParams()
	}
	welcome := ""
	if len(f) > 3 {
		welcome = d.path(f[3])
	}
	return d.offerChat(c, f[0], "fserve", d.path(f[2]), welcome, interp.Int(f[1]))
}

// cmdDCCServer implements /dccserver [+|-scf] [on|off] [port].
func (d *DCC) cmdDCCServer(c *interp.CmdCall) error {
	for _, w := range c.Fields() {
		switch {
		case strings.HasPrefix(w, "+"):
			for _, k := range w[1:] {
				if !strings.ContainsRune(d.Settings.ServerKinds, k) {
					d.Settings.ServerKinds += string(k)
				}
			}
		case strings.HasPrefix(w, "-"):
			for _, k := range w[1:] {
				d.Settings.ServerKinds = strings.ReplaceAll(d.Settings.ServerKinds, string(k), "")
			}
		case strings.EqualFold(w, "on"):
			d.Settings.ServerOn = true
		case strings.EqualFold(w, "off"):
			d.Settings.ServerOn = false
		default:
			if n, err := strconv.Atoi(w); err == nil {
				d.Settings.ServerPort = n
			}
		}
	}
	if !d.Settings.ServerOn {
		if d.server != nil {
			d.server.Close()
			d.server = nil
		}
		return nil
	}
	if err := d.startServer(); err != nil {
		return c.Errorf("unable to listen on port %d", d.Settings.ServerPort)
	}
	return nil
}

// serverChat connects to another client's DCC Server for a chat or fserve.
func (d *DCC) serverChat(f *interp.Frame, ip, code, kind string) {
	conn := d.conn(f)
	s := d.add(&Session{Kind: kind, Nick: ip, IP: ip, Status: "waiting", cid: conn.ID})
	me := conn.Me
	gen := s.gen
	go func() {
		nc, err := net.DialTimeout("tcp", net.JoinHostPort(ip, strconv.Itoa(d.Settings.ServerPort)), 30*time.Second)
		var nick string
		var r *bufio.Reader
		if err == nil {
			nc.Write([]byte(code + " " + me + "\n"))
			r = bufio.NewReader(nc)
			var line string
			line, err = r.ReadString('\n')
			if f := strings.Fields(line); err == nil && len(f) >= 2 {
				nick = f[1]
			}
		}
		d.rt.Post(func() {
			if gen != s.gen || err != nil {
				if nc != nil {
					nc.Close()
				}
				d.remove(s)
				return
			}
			if nick != "" {
				s.Nick = nick
			}
			d.startChat(s, wrapReader(nc, r))
		})
	}()
}

// serverSend sends a file to another client's DCC Server.
func (d *DCC) serverSend(f *interp.Frame, ip, path string) {
	conn := d.conn(f)
	s := d.add(&Session{Kind: "send", Nick: ip, IP: ip, File: path, Size: fileSize(path), Status: "waiting", cid: conn.ID})
	me := conn.Me
	gen := s.gen
	go func() {
		nc, err := net.DialTimeout("tcp", net.JoinHostPort(ip, strconv.Itoa(d.Settings.ServerPort)), 30*time.Second)
		var nick string
		var resume int64
		if err == nil {
			nc.Write([]byte("120 " + me + " " + strconv.FormatInt(s.Size, 10) + " " + filepath.Base(path) + "\n"))
			var line string
			line, err = bufio.NewReader(nc).ReadString('\n')
			if f := strings.Fields(line); err == nil && len(f) >= 3 {
				nick = f[1]
				resume, _ = strconv.ParseInt(f[2], 10, 64)
			}
		}
		d.rt.Post(func() {
			if gen != s.gen || err != nil {
				if nc != nil {
					nc.Close()
				}
				d.failSend(s)
				return
			}
			s.Nick, s.Resume = nick, resume
			d.transmit(s, nc)
		})
	}()
}

// cmdClose implements /close for DCC windows and queries:
// -c chats, -f fserves, -g gets, -s sends, -m queries, with optional nicks.
func (d *DCC) cmdClose(c *interp.CmdCall) error {
	sw, rest := c.Switches()
	nicks := strings.Fields(rest)
	match := func(nick string) bool {
		if len(nicks) == 0 {
			return true
		}
		for _, n := range nicks {
			if strings.EqualFold(n, nick) {
				return true
			}
		}
		return false
	}
	for _, s := range append([]*Session(nil), d.sessions...) {
		if strings.IndexByte(sw, kindLetter(s.Kind)) >= 0 && match(s.Nick) {
			if s.Kind == "chat" || s.Kind == "fserve" {
				d.fireOpenClose("CLOSE", s)
			}
			d.remove(s)
		}
	}
	if strings.Contains(sw, "m") {
		for _, n := range nicks {
			_ = c.F.ExecLine("!queryrn "+n+" "+n, 0)
		}
	}
	if d.CloseWindows != nil && strings.Contains(sw, "@") {
		return d.CloseWindows(c, rest)
	}
	return nil
}

func (d *DCC) installIdents() {
	rt := d.rt
	rt.RegisterIdent("chat", func(c *interp.Call) (string, error) { return d.identSession(c, "chat") })
	rt.RegisterIdent("send", func(c *interp.Call) (string, error) { return d.identSession(c, "send") })
	rt.RegisterIdent("get", func(c *interp.Call) (string, error) { return d.identSession(c, "get") })
	rt.RegisterIdent("fserve", func(c *interp.Call) (string, error) { return d.identSession(c, "fserve") })
	rt.RegisterIdent("filename", func(c *interp.Call) (string, error) { v, _ := c.F.Event.Get("filename"); return v, nil })
	rt.RegisterIdent("cd", func(c *interp.Call) (string, error) { v, _ := c.F.Event.Get("cd"); return v, nil })
	rt.RegisterIdent("trust", func(c *interp.Call) (string, error) { return nth(c.Arg(0), d.Settings.Trust), nil })
	rt.RegisterIdent("dccport", func(*interp.Call) (string, error) { return strconv.Itoa(d.Settings.ServerPort), nil })
	rt.RegisterIdent("passivedcc", func(*interp.Call) (string, error) { return interp.Bool(d.Settings.Passive), nil })
	rt.RegisterIdent("creq", func(*interp.Call) (string, error) { return d.Settings.ChatRequest, nil })
	rt.RegisterIdent("sreq", func(*interp.Call) (string, error) { return d.Settings.SendRequest, nil })
	rt.RegisterIdent("dccignore", func(c *interp.Call) (string, error) {
		if !c.Paren {
			return interp.Bool(d.Settings.IgnoreOn), nil
		}
		if _, ok := interp.ParseNumber(c.Arg(0)); ok {
			return nth(c.Arg(0), d.Settings.IgnoreTypes), nil
		}
		for _, p := range d.Settings.IgnoreTypes {
			if interp.WildMatch(p, c.Arg(0), false) {
				return interp.Bool(true), nil
			}
		}
		return interp.Bool(false), nil
	})
}

func nth(arg string, list []string) string {
	n := interp.Int(arg)
	if n == 0 {
		return strconv.Itoa(len(list))
	}
	if n < 1 || n > len(list) {
		return ""
	}
	return list[n-1]
}

// identSession implements $chat, $send, $get and $fserve (N|nick, N).prop.
func (d *DCC) identSession(c *interp.Call, kind string) (string, error) {
	list := d.ofKind(kind)
	arg := c.Arg(0)
	var s *Session
	if n, ok := interp.ParseNumber(arg); ok {
		i := int(n)
		if i == 0 {
			return strconv.Itoa(len(list)), nil
		}
		if i == -1 && len(list) > 0 {
			s = list[len(list)-1]
		} else if i >= 1 && i <= len(list) {
			s = list[i-1]
		}
	} else {
		var mine []*Session
		for _, x := range list {
			if strings.EqualFold(x.Nick, arg) {
				mine = append(mine, x)
			}
		}
		n := 1
		if len(c.Args) > 1 {
			n = interp.Int(c.Arg(1))
		}
		if n == 0 {
			return strconv.Itoa(len(mine)), nil
		}
		if n >= 1 && n <= len(mine) {
			s = mine[n-1]
		}
	}
	if s == nil {
		return "", nil
	}
	secs := time.Since(s.opened).Seconds()
	switch strings.ToLower(c.Prop) {
	case "ip":
		return s.IP, nil
	case "status":
		return s.Status, nil
	case "file":
		return filepath.Base(s.File), nil
	case "path":
		return filepath.Dir(s.File) + string(filepath.Separator), nil
	case "size":
		return strconv.FormatInt(s.Size, 10), nil
	case "sent", "rcvd":
		return strconv.FormatInt(s.Done, 10), nil
	case "lra":
		return strconv.FormatInt(s.Acked, 10), nil
	case "cps":
		if secs < 1 {
			return "0", nil
		}
		return strconv.Itoa(int(float64(s.Done-s.Resume) / secs)), nil
	case "pc":
		if s.Size == 0 {
			return "0", nil
		}
		return strconv.Itoa(int(s.Done * 100 / s.Size)), nil
	case "secs":
		return strconv.Itoa(int(secs)), nil
	case "done":
		return interp.Bool(s.success), nil
	case "resume":
		return strconv.FormatInt(s.Resume, 10), nil
	case "cd":
		return s.Cd, nil
	case "idle":
		return strconv.Itoa(int(time.Since(s.last).Seconds())), nil
	case "cid":
		return strconv.Itoa(s.cid), nil
	case "wid", "hwnd", "logfile", "stamp":
		return "", nil
	}
	if kind == "send" || kind == "get" {
		return s.Nick + " " + filepath.Base(s.File), nil
	}
	return s.Nick, nil
}
