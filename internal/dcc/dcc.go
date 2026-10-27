// Package dcc implements DCC chat, file send/get, the fileserver and the
// DCC Server, with the events and identifiers scripts use.
package dcc

import (
	"bufio"
	"encoding/binary"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/ValwareIRC/msl-runtime/internal/interp"
	"github.com/ValwareIRC/msl-runtime/internal/irc"
	"github.com/ValwareIRC/msl-runtime/internal/script"
	"github.com/ValwareIRC/msl-runtime/internal/storage"
)

// Session is one DCC chat, fserve, send or get.
type Session struct {
	Kind    string // chat, fserve, send, get
	Nick    string
	IP      string
	Port    int
	Status  string // waiting, active, inactive, sent, failed
	File    string // full path for sends and gets
	Size    int64
	Done    int64 // bytes sent or received
	Acked   int64 // last acknowledgement received (sends)
	Resume  int64
	Cd      string // fserve current directory
	Root    string // fserve root
	Passive bool
	Token   string

	conn    net.Conn
	ln      net.Listener
	cid     int
	opened  time.Time
	last    time.Time
	maxGets int
	gen     int
	success bool
}

// Settings are the DCC options scripts change with /creq, /sreq and /dcc.
type Settings struct {
	ChatRequest string // ask, auto, ignore
	SendRequest string
	Passive     bool
	IgnoreOn    bool
	IgnoreMode  string // accept or ignore
	IgnoreTypes []string
	Trust       []string
	MaxCPS      int
	ServerPort  int
	ServerOn    bool
	ServerKinds string // s c f
}

// DCC is the per-runtime DCC state.
type DCC struct {
	rt       *interp.Runtime
	irc      *irc.Manager
	st       *storage.Storage
	Settings Settings
	sessions []*Session
	server   net.Listener

	// CloseWindows handles /close -@ for custom windows; the window package sets it.
	CloseWindows func(c *interp.CmdCall, names string) error
}

type dccKey struct{}

// Install registers DCC on rt and hooks it into the IRC manager.
func Install(rt *interp.Runtime, m *irc.Manager) *DCC {
	d := &DCC{rt: rt, irc: m, st: storage.From(rt), Settings: Settings{
		ChatRequest: "ask", SendRequest: "ask", IgnoreMode: "ignore",
		IgnoreTypes: []string{"*.exe", "*.com", "*.bat", "*.dll", "*.ini", "*.mrc", "*.vbs", "*.js", "*.pif", "*.scr", "*.lnk", "*.pl", "*.shs", "*.htm", "*.html"},
		ServerPort:  59, ServerKinds: "scf",
	}}
	interp.Ext(rt, dccKey{}, func() *DCC { return d })
	m.DCC = d.handleRequest
	m.ChatSend = d.chatSend
	d.installCommands()
	d.installIdents()
	rt.DeclareEvents("CHAT", "SERV", "OPEN", "CLOSE", "FILESENT", "FILERCVD", "SENDFAIL", "GETFAIL", "DCCSERVER")
	return d
}

func (d *DCC) getDir() string {
	if d.st != nil {
		return filepath.Join(d.st.Dir, "downloads")
	}
	return "downloads"
}

func (d *DCC) path(p string) string {
	if d.st != nil {
		return d.st.Path(p)
	}
	return p
}

func (d *DCC) add(s *Session) *Session {
	s.opened, s.last = time.Now(), time.Now()
	d.sessions = append(d.sessions, s)
	return s
}

func (d *DCC) remove(s *Session) {
	s.gen++
	if s.conn != nil {
		s.conn.Close()
	}
	if s.ln != nil {
		s.ln.Close()
	}
	for i, x := range d.sessions {
		if x == s {
			d.sessions = append(d.sessions[:i], d.sessions[i+1:]...)
			return
		}
	}
}

func (d *DCC) ofKind(kind string) []*Session {
	var out []*Session
	for _, s := range d.sessions {
		if s.Kind == kind {
			out = append(out, s)
		}
	}
	return out
}

func (d *DCC) chat(nick string) *Session {
	for _, s := range d.sessions {
		if (s.Kind == "chat" || s.Kind == "fserve") && strings.EqualFold(s.Nick, nick) && s.conn != nil {
			return s
		}
	}
	return nil
}

// event builds identifier values for a DCC event.
func (d *DCC) event(name string, s *Session) *interp.Event {
	ev := &interp.Event{Name: name, Values: map[string]string{"nick": s.Nick, "target": "=" + s.Nick}}
	if s.File != "" {
		ev.Values["filename"] = s.File
	}
	if s.Kind == "fserve" {
		ev.Values["cd"] = s.Cd
	}
	if s.IP != "" {
		ev.Values["address"] = s.IP
		ev.Values["site"] = s.IP
	}
	return ev
}

// fireOpenClose fires on OPEN or on CLOSE for a chat (=) or fserve (!).
func (d *DCC) fireOpenClose(name string, s *Session) bool {
	loc := "="
	if s.Kind == "fserve" {
		loc = "!"
	}
	return d.rt.Dispatch(interp.Fire{Kind: "on", Name: name, Event: d.event(name, s), Cid: s.cid,
		Match: func(f *interp.Frame, e *script.Event) (bool, error) {
			return len(e.Fields) > 0 && (e.Fields[0] == loc || e.Fields[0] == "*"), nil
		}})
}

// fireFile fires FILESENT, FILERCVD, SENDFAIL or GETFAIL, matching the
// filename field against the file's name.
func (d *DCC) fireFile(name string, s *Session) {
	base := filepath.Base(s.File)
	d.rt.Dispatch(interp.Fire{Kind: "on", Name: name, Event: d.event(name, s), Cid: s.cid,
		Match: func(f *interp.Frame, e *script.Event) (bool, error) {
			if len(e.Fields) == 0 {
				return true, nil
			}
			for _, p := range strings.Split(e.Fields[0], ",") {
				if interp.WildMatch(p, base, false) || interp.WildMatch(p, s.File, false) {
					return true, nil
				}
			}
			return false, nil
		}})
}

// localIP returns the address to advertise in DCC offers: the local
// address of the IRC connection, or the /localinfo address.
func localIP(c *irc.Conn) net.IP {
	for _, s := range []string{c.LocalHost, c.LocalIP} {
		if ip := net.ParseIP(s); ip != nil {
			return ip
		}
	}
	return net.IPv4(127, 0, 0, 1)
}

// ipArg encodes an IP for a DCC offer: IPv4 as a 32-bit integer.
func ipArg(ip net.IP) string {
	if v4 := ip.To4(); v4 != nil {
		return strconv.FormatUint(uint64(binary.BigEndian.Uint32(v4)), 10)
	}
	return ip.String()
}

// parseIP decodes a DCC offer address.
func parseIP(s string) string {
	if n, err := strconv.ParseUint(s, 10, 32); err == nil {
		var b [4]byte
		binary.BigEndian.PutUint32(b[:], uint32(n))
		return net.IP(b[:]).String()
	}
	return s
}

// listen opens a listening socket for an outgoing offer.
func listen() (net.Listener, int, error) {
	ln, err := net.Listen("tcp", ":0")
	if err != nil {
		return nil, 0, err
	}
	return ln, ln.Addr().(*net.TCPAddr).Port, nil
}

// trusted reports whether an address is in the trust list.
func (d *DCC) trusted(c *irc.Conn, address string) bool {
	for _, t := range d.Settings.Trust {
		mask, network, _ := strings.Cut(t, ":")
		if network != "" && !strings.EqualFold(network, c.Network()) {
			continue
		}
		if !strings.ContainsAny(mask, "!@") {
			mask += "!*@*"
		}
		if interp.WildMatch(mask, address, false) {
			return true
		}
	}
	return false
}

// ignoredFile applies the DCC ignore types to an incoming filename.
func (d *DCC) ignoredFile(name string) bool {
	if !d.Settings.IgnoreOn {
		return false
	}
	match := false
	for _, p := range d.Settings.IgnoreTypes {
		if interp.WildMatch(p, name, false) {
			match = true
		}
	}
	if d.Settings.IgnoreMode == "accept" {
		return !match
	}
	return match
}

// handleRequest processes an incoming CTCP DCC request after ctcp events ran.
func (d *DCC) handleRequest(c *irc.Conn, m irc.Message, arg string, ev *interp.Event) {
	f := splitDCC(arg)
	if len(f) == 0 {
		return
	}
	nick := m.Nick()
	rejected := ev.Values["dcc:reject"] != ""
	getDir := ev.Values["dcc:get"]
	switch strings.ToUpper(f[0]) {
	case "CHAT":
		if len(f) < 4 || rejected {
			return
		}
		if d.Settings.ChatRequest != "auto" && !d.trusted(c, m.Source) && getDir == "" {
			return
		}
		ip, port := parseIP(f[2]), f[3]
		s := d.add(&Session{Kind: "chat", Nick: nick, IP: ip, Status: "waiting", cid: c.ID})
		if port == "0" && len(f) > 4 {
			d.passiveChatReply(c, s, f[4])
			return
		}
		d.connectChat(s, net.JoinHostPort(ip, port))
	case "SEND":
		if len(f) < 4 || rejected {
			return
		}
		name := strings.Trim(f[1], `"`)
		if d.ignoredFile(name) && getDir == "" {
			return
		}
		if d.Settings.SendRequest != "auto" && !d.trusted(c, m.Source) && getDir == "" {
			return
		}
		dir := d.getDir()
		if getDir != "" {
			dir = d.path(getDir)
		}
		size := int64(0)
		if len(f) > 4 {
			size, _ = strconv.ParseInt(f[4], 10, 64)
		}
		s := d.add(&Session{Kind: "get", Nick: nick, IP: parseIP(f[2]), File: filepath.Join(dir, filepath.Base(name)),
			Size: size, Status: "waiting", cid: c.ID})
		s.Port, _ = strconv.Atoi(f[3])
		if len(f) > 5 {
			s.Token = f[5]
		}
		d.startGet(c, s)
	case "RESUME", "ACCEPT":
		if len(f) >= 4 {
			d.resumeReply(c, strings.ToUpper(f[0]), nick, f[1:])
		}
	}
}

// splitDCC splits a DCC argument, keeping a quoted filename together.
func splitDCC(arg string) []string {
	var out []string
	for arg = strings.TrimSpace(arg); arg != ""; arg = strings.TrimSpace(arg) {
		if arg[0] == '"' {
			if end := strings.IndexByte(arg[1:], '"'); end >= 0 {
				out = append(out, arg[:end+2])
				arg = arg[end+2:]
				continue
			}
		}
		w, rest, _ := strings.Cut(arg, " ")
		out = append(out, w)
		arg = rest
	}
	return out
}

// connectChat connects to a chat offer.
func (d *DCC) connectChat(s *Session, addr string) {
	gen := s.gen
	go func() {
		conn, err := net.DialTimeout("tcp", addr, 30*time.Second)
		d.rt.Post(func() {
			if gen != s.gen {
				if conn != nil {
					conn.Close()
				}
				return
			}
			if err != nil {
				d.remove(s)
				return
			}
			d.startChat(s, conn)
		})
	}()
}

// passiveChatReply answers a passive chat offer by listening ourselves.
func (d *DCC) passiveChatReply(c *irc.Conn, s *Session, token string) {
	ln, port, err := listen()
	if err != nil {
		d.remove(s)
		return
	}
	s.ln = ln
	c.Send("PRIVMSG " + s.Nick + " :" + irc.EncodeCTCP("DCC", "CHAT chat "+ipArg(localIP(c))+" "+strconv.Itoa(port)+" "+token))
	d.acceptOne(s, func(conn net.Conn) { d.startChat(s, conn) })
}

// acceptOne waits for one connection on a session's listener.
func (d *DCC) acceptOne(s *Session, then func(net.Conn)) {
	gen := s.gen
	ln := s.ln
	go func() {
		ln.(*net.TCPListener).SetDeadline(time.Now().Add(3 * time.Minute))
		conn, err := ln.Accept()
		ln.Close()
		d.rt.Post(func() {
			if gen != s.gen {
				if conn != nil {
					conn.Close()
				}
				return
			}
			s.ln = nil
			if err != nil {
				if s.Kind == "send" {
					s.Status = "failed"
					d.fireFile("SENDFAIL", s)
				}
				d.remove(s)
				return
			}
			then(conn)
		})
	}()
}

// startChat runs a chat or fserve connection.
func (d *DCC) startChat(s *Session, conn net.Conn) {
	s.conn, s.Status, s.last = conn, "active", time.Now()
	if ra, ok := conn.RemoteAddr().(*net.TCPAddr); ok {
		s.IP = ra.IP.String()
	}
	if d.fireOpenClose("OPEN", s) && s.Kind == "chat" {
		d.remove(s)
		return
	}
	if s.Kind == "fserve" {
		d.fserveWelcome(s)
	}
	gen := s.gen
	go func() {
		sc := bufio.NewScanner(conn)
		sc.Buffer(make([]byte, 8192), 1<<20)
		for sc.Scan() {
			line := strings.TrimRight(sc.Text(), "\r")
			d.rt.Post(func() {
				if gen == s.gen {
					d.chatLine(s, line)
				}
			})
		}
		d.rt.Post(func() {
			if gen != s.gen {
				return
			}
			s.Status = "inactive"
			d.fireOpenClose("CLOSE", s)
			d.remove(s)
		})
	}()
}

// chatLine handles one line received on a chat or fserve.
func (d *DCC) chatLine(s *Session, line string) {
	s.last = time.Now()
	name := "CHAT"
	if s.Kind == "fserve" {
		name = "SERV"
	}
	text := line
	if cmd, arg, ok := irc.CTCP(line); ok && cmd == "ACTION" {
		text = arg
	}
	halted := d.rt.Dispatch(interp.Fire{Kind: "on", Name: name, Event: d.event(name, s), Params: strings.Fields(text), Cid: s.cid,
		Match: func(f *interp.Frame, e *script.Event) (bool, error) {
			if len(e.Fields) == 0 {
				return true, nil
			}
			return irc.MatchText(f, e, e.Fields[0], text)
		}})
	if s.Kind == "fserve" && !halted {
		d.fserveCommand(s, line)
	}
}

// chatSend writes to an open chat for /msg =nick and /describe =nick.
func (d *DCC) chatSend(nick, text string, action bool) error {
	s := d.chat(nick)
	if s == nil {
		return errNoChat(nick)
	}
	if action {
		text = irc.EncodeCTCP("ACTION", text)
	}
	_, err := s.conn.Write([]byte(text + "\n"))
	return err
}

type errNoChat string

func (e errNoChat) Error() string { return "no dcc chat with " + string(e) }

func fileSize(p string) int64 {
	st, err := os.Stat(p)
	if err != nil {
		return 0
	}
	return st.Size()
}
