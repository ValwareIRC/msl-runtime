package sockets

import (
	"bytes"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/ValwareIRC/msl-runtime/internal/interp"
)

func (s *Sockets) installCommands() {
	rt := s.rt
	rt.RegisterCommand("sockopen", interp.Command{Fn: s.cmdSockopen})
	rt.RegisterCommand("socklisten", interp.Command{Fn: s.cmdSocklisten})
	rt.RegisterCommand("sockaccept", interp.Command{Fn: s.cmdSockaccept})
	rt.RegisterCommand("sockclose", interp.Command{Fn: func(c *interp.CmdCall) error {
		for _, k := range s.Match(strings.TrimSpace(c.Args)) {
			s.remove(k)
		}
		return nil
	}})
	rt.RegisterCommand("sockrename", interp.Command{Fn: func(c *interp.CmdCall) error {
		f := c.Fields()
		if len(f) < 2 {
			return c.InvalidParams()
		}
		k := s.Get(f[0])
		if k == nil {
			return c.Errorf("'%s' no such socket", f[0])
		}
		if s.Get(f[1]) != nil {
			return c.Errorf("'%s' name in use", f[1])
		}
		k.Name = f[1]
		return nil
	}})
	rt.RegisterCommand("sockmark", interp.Command{Fn: func(c *interp.CmdCall) error {
		name, text, _ := strings.Cut(strings.TrimSpace(c.Args), " ")
		for _, k := range s.Match(name) {
			k.Mark = text
		}
		return nil
	}})
	rt.RegisterCommand("sockpause", interp.Command{Fn: func(c *interp.CmdCall) error {
		sw, rest := c.Switches()
		for _, k := range s.Match(strings.TrimSpace(rest)) {
			k.Paused = !strings.Contains(sw, "r")
			if !k.Paused && len(k.rq) > 0 {
				kk := k
				rt.Post(func() { s.deliver(kk, readEvent(kk)) })
			}
		}
		return nil
	}})
	rt.RegisterCommand("socklist", interp.Command{Fn: func(c *interp.CmdCall) error {
		sw, rest := c.Switches()
		pattern := strings.TrimSpace(rest)
		if pattern == "" {
			pattern = "*"
		}
		for _, k := range s.Match(pattern) {
			if sw != "" && !(strings.Contains(sw, "t") && k.Kind == "TCP" && !k.Listen ||
				strings.Contains(sw, "u") && k.Kind == "UDP" || strings.Contains(sw, "l") && k.Listen) {
				continue
			}
			rt.Out.Echo(interp.StatusWindow, "* "+k.Name+" "+k.Kind+" "+k.Status+" "+k.IP+" "+strconv.Itoa(k.Port))
		}
		return nil
	}})
	rt.RegisterCommand("sockwrite", interp.Command{Fn: s.cmdSockwrite, Raw: true})
	rt.RegisterCommand("sockread", interp.Command{Fn: s.cmdSockread, Raw: true})
	rt.RegisterCommand("sockudp", interp.Command{Fn: s.cmdSockudp, Raw: true})
	rt.RegisterCommand("bindip", interp.Command{Fn: func(c *interp.CmdCall) error {
		f := c.Fields()
		switch {
		case len(f) > 0 && strings.EqualFold(f[0], "off"):
			s.bindIP = ""
		case len(f) > 1:
			s.bindIP = f[1]
		case len(f) == 1 && !strings.EqualFold(f[0], "on"):
			s.bindIP = f[0]
		}
		return nil
	}})
}

func readEvent(k *Socket) string {
	if k.Kind == "UDP" {
		return "UDPREAD"
	}
	return "SOCKREAD"
}

// cmdSockopen implements /sockopen [-deswap64nt] [bindip] <name> <address> <port>.
func (s *Sockets) cmdSockopen(c *interp.CmdCall) error {
	sw, rest := c.Switches()
	f := strings.Fields(rest)
	bind := s.bindIP
	if strings.Contains(sw, "d") && len(f) > 0 {
		bind, f = f[0], f[1:]
	}
	if len(f) < 3 {
		return c.InvalidParams()
	}
	name, host, port := f[0], f[1], f[2]
	if s.Get(name) != nil {
		return c.Errorf("'%s' name in use", name)
	}
	k := &Socket{Name: name, Kind: "TCP", Addr: host, Status: "connecting", SSL: strings.Contains(sw, "e")}
	k.Port, _ = strconv.Atoi(port)
	s.add(k)
	network := "tcp"
	switch {
	case strings.Contains(sw, "6") && !strings.Contains(sw, "4"):
		network = "tcp6"
	case strings.Contains(sw, "4") && !strings.Contains(sw, "6"):
		network = "tcp4"
	}
	insecure := strings.ContainsAny(sw, "sa")
	gen := k.gen
	go func() {
		conn, err := dial(network, net.JoinHostPort(host, port), k.SSL, insecure, bind)
		s.rt.Post(func() {
			if gen != k.gen {
				if conn != nil {
					conn.Close()
				}
				return
			}
			if err != nil {
				k.WSErr, k.WSMsg = winsockError(err)
				s.fire("SOCKOPEN", k, 3)
				s.remove(k)
				return
			}
			s.startIO(k, conn)
			s.fire("SOCKOPEN", k, 0)
		})
	}()
	return nil
}

// cmdSocklisten implements /socklisten [-dpu] [bindip] <name> [port].
func (s *Sockets) cmdSocklisten(c *interp.CmdCall) error {
	sw, rest := c.Switches()
	f := strings.Fields(rest)
	bind := s.bindIP
	if strings.Contains(sw, "d") && len(f) > 0 {
		bind, f = f[0], f[1:]
	}
	if len(f) == 0 {
		return c.InvalidParams()
	}
	name := f[0]
	if s.Get(name) != nil {
		return c.Errorf("'%s' name in use", name)
	}
	port := "0"
	if len(f) > 1 {
		port = f[1]
	}
	ln, err := net.Listen("tcp", net.JoinHostPort(bind, port))
	if err != nil {
		code, msg := winsockError(err)
		return c.Errorf("unable to open port %s (%d %s)", port, code, msg)
	}
	k := &Socket{Name: name, Kind: "TCP", Listen: true, Status: "listening", ln: ln}
	if a, ok := ln.Addr().(*net.TCPAddr); ok {
		k.IP, k.Port, k.BindIP, k.BindPort = a.IP.String(), a.Port, a.IP.String(), a.Port
	}
	s.add(k)
	gen := k.gen
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			s.rt.Post(func() {
				if gen != k.gen {
					conn.Close()
					return
				}
				k.pending = conn
				s.fire("SOCKLISTEN", k, 0)
				if k.pending != nil {
					k.pending.Close() // not accepted
					k.pending = nil
				}
			})
		}
	}()
	return nil
}

// cmdSockaccept implements /sockaccept [-n] <name> inside on SOCKLISTEN.
func (s *Sockets) cmdSockaccept(c *interp.CmdCall) error {
	_, rest := c.Switches()
	name := strings.TrimSpace(rest)
	listenName, _ := c.F.Event.Get("sockname")
	l := s.Get(listenName)
	if l == nil || l.pending == nil {
		return c.Errorf("no pending connection")
	}
	if s.Get(name) != nil {
		return c.Errorf("'%s' name in use", name)
	}
	k := &Socket{Name: name, Kind: "TCP"}
	s.add(k)
	s.startIO(k, l.pending)
	l.pending = nil
	return nil
}

// payload resolves the data argument of /sockwrite and /sockudp: a
// &binvar, a %var read directly (keeping spaces), or evaluated text.
func (s *Sockets) payload(c *interp.CmdCall, ws []interp.Word, sw string) ([]byte, error) {
	if len(ws) == 1 && !ws[0].Final {
		w := ws[0].Text
		if strings.HasPrefix(w, "&") && !strings.Contains(sw, "t") && s.data != nil {
			b, _ := s.data.Bin(w)
			return b, nil
		}
		if strings.HasPrefix(w, "%") {
			if v, ok := c.F.GetVar(w); ok {
				return textBytes(v.Value, sw), nil
			}
			return nil, nil
		}
	}
	text, err := c.F.EvalWords(ws)
	if err != nil {
		return nil, err
	}
	return textBytes(text, sw), nil
}

func textBytes(text, sw string) []byte {
	if strings.Contains(sw, "a") {
		out := make([]byte, 0, len(text))
		for _, r := range text {
			if r > 0xff {
				return []byte(text)
			}
			out = append(out, byte(r))
		}
		return out
	}
	return []byte(text)
}

func leadingSwitches(ws []interp.Word) (string, []interp.Word) {
	if len(ws) > 0 && !ws[0].Final && len(ws[0].Text) > 1 && ws[0].Text[0] == '-' {
		return ws[0].Text[1:], ws[1:]
	}
	return "", ws
}

func evalWord(c *interp.CmdCall, w interp.Word) (string, error) {
	if w.Final {
		return w.Text, nil
	}
	return c.F.EvalWords([]interp.Word{w})
}

// cmdSockwrite implements /sockwrite [-tnba] <name> [numbytes] <text|%var|&binvar>.
func (s *Sockets) cmdSockwrite(c *interp.CmdCall) error {
	sw, ws := leadingSwitches(c.Words)
	if len(ws) < 2 {
		return c.InvalidParams()
	}
	name, err := evalWord(c, ws[0])
	if err != nil {
		return err
	}
	ws = ws[1:]
	limit := -1
	if strings.Contains(sw, "b") && len(ws) > 1 {
		v, err := evalWord(c, ws[0])
		if err != nil {
			return err
		}
		limit, ws = interp.Int(v), ws[1:]
	}
	b, err := s.payload(c, ws, sw)
	if err != nil {
		return err
	}
	if limit >= 0 && limit < len(b) {
		b = b[:limit]
	}
	isBin := len(ws) == 1 && strings.HasPrefix(ws[0].Text, "&") && !strings.Contains(sw, "t")
	if strings.Contains(sw, "n") && !isBin && !bytes.HasSuffix(b, []byte("\r\n")) {
		b = append(b, '\r', '\n')
	}
	targets := s.Match(name)
	if len(targets) == 0 {
		return c.Errorf("'%s' no such socket", name)
	}
	for _, k := range targets {
		if k.out == nil || k.Listen {
			return c.Errorf("'%s' not connected", k.Name)
		}
		if k.sq+len(b) > maxSendQueue {
			return c.Errorf("'%s' queue would exceed %d bytes", k.Name, maxSendQueue)
		}
		k.sq += len(b)
		k.out <- append([]byte(nil), b...)
	}
	return nil
}

// cmdSockread implements /sockread [-fn] [numbytes] <%var|&binvar> for the
// socket of the current event.
func (s *Sockets) cmdSockread(c *interp.CmdCall) error {
	sw, ws := leadingSwitches(c.Words)
	if len(ws) == 0 {
		return c.InvalidParams()
	}
	name, _ := c.F.Event.Get("sockname")
	k := s.Get(name)
	if k == nil {
		return c.Errorf("no socket to read from")
	}
	n := 4096
	if len(ws) > 1 {
		v, err := evalWord(c, ws[0])
		if err != nil {
			return err
		}
		n, ws = interp.Int(v), ws[1:]
	}
	target := ws[0].Text
	k.read.called = true
	s.sockbr = 0
	var chunk []byte
	switch {
	case strings.HasPrefix(target, "&") && !strings.Contains(sw, "n"):
		k.read.binvar = true
		take := min(max(n, 0), len(k.rq))
		chunk, k.rq = k.rq[:take], k.rq[take:]
	default:
		i := bytes.IndexByte(k.rq, '\n')
		switch {
		case i >= 0:
			chunk, k.rq = k.rq[:i+1], k.rq[i+1:]
		case strings.Contains(sw, "f"):
			chunk, k.rq = k.rq, nil
		}
		s.sockbr = len(chunk)
		chunk = bytes.TrimRight(chunk, "\r\n")
		if strings.HasPrefix(target, "%") {
			if z := bytes.IndexByte(chunk, 0); z >= 0 {
				chunk = chunk[:z]
			}
		}
	}
	if s.sockbr == 0 {
		s.sockbr = len(chunk)
	}
	if s.sockbr > 0 {
		k.read.progress = true
	}
	switch {
	case strings.HasPrefix(target, "&"):
		if s.data != nil {
			s.data.SetBin(target, append([]byte(nil), chunk...))
		}
	case strings.HasPrefix(target, "%"):
		c.F.SetVar(target, string(chunk), false)
	default:
		return c.InvalidParams()
	}
	return nil
}

// cmdSockudp implements
// /sockudp [-bntkdu] [bindip] <name> [port] <ipaddress> <port> [numbytes] [text|%var|&binvar].
func (s *Sockets) cmdSockudp(c *interp.CmdCall) error {
	sw, ws := leadingSwitches(c.Words)
	vals := make([]string, 0, 5)
	// Evaluate the leading address words; the rest is the payload.
	need := 3
	if strings.Contains(sw, "d") {
		need++
	}
	for len(vals) < need+1 && len(ws) > 0 {
		v, err := evalWord(c, ws[0])
		if err != nil {
			return err
		}
		vals = append(vals, v)
		ws = ws[1:]
	}
	bind := s.bindIP
	if strings.Contains(sw, "d") && len(vals) > 0 {
		bind, vals = vals[0], vals[1:]
	}
	if len(vals) < 3 {
		return c.InvalidParams()
	}
	name := vals[0]
	localPort := 0
	var ip, port string
	// [port] is present when four address values precede the payload.
	if len(vals) >= 4 && net.ParseIP(vals[2]) != nil {
		localPort, ip, port = interp.Int(vals[1]), vals[2], vals[3]
	} else {
		ip, port = vals[1], vals[2]
		if len(vals) == 4 {
			ws = append([]interp.Word{{Text: vals[3], Final: true}}, ws...)
		}
	}
	limit := -1
	if strings.Contains(sw, "b") && len(ws) > 1 {
		v, err := evalWord(c, ws[0])
		if err != nil {
			return err
		}
		limit, ws = interp.Int(v), ws[1:]
	}
	var payload []byte
	if len(ws) > 0 {
		var err error
		if payload, err = s.payload(c, ws, sw); err != nil {
			return err
		}
	}
	if limit >= 0 && limit < len(payload) {
		payload = payload[:limit]
	}
	if strings.Contains(sw, "n") && !bytes.HasSuffix(payload, []byte("\r\n")) {
		payload = append(payload, '\r', '\n')
	}
	k := s.Get(name)
	if k == nil {
		udp, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP(bind), Port: localPort})
		if err != nil {
			code, msg := winsockError(err)
			return c.Errorf("unable to open udp socket (%d %s)", code, msg)
		}
		k = &Socket{Name: name, Kind: "UDP", Status: "active", udp: udp, udpKeep: strings.Contains(sw, "k")}
		if a, ok := udp.LocalAddr().(*net.UDPAddr); ok {
			k.BindIP, k.BindPort, k.Port = a.IP.String(), a.Port, a.Port
		}
		s.add(k)
		if strings.Contains(sw, "k") {
			go s.udpLoop(k, udp, k.gen)
		}
	}
	dst, err := net.ResolveUDPAddr("udp", net.JoinHostPort(ip, port))
	if err == nil && len(payload) > 0 {
		var n int
		n, err = k.udp.WriteToUDP(payload, dst)
		k.Sent += int64(n)
		k.lastSent = time.Now()
	}
	if err != nil {
		k.WSErr, k.WSMsg = winsockError(err)
		s.fire("SOCKWRITE", k, 3)
	}
	if !strings.Contains(sw, "k") && s.Get(name) == k && !k.keep() {
		s.remove(k)
	}
	return nil
}

// keep reports whether a UDP socket was opened with -k and stays open.
func (k *Socket) keep() bool { return k.udpKeep }

func (s *Sockets) udpLoop(k *Socket, udp *net.UDPConn, gen int) {
	buf := make([]byte, 65536)
	for {
		n, from, err := udp.ReadFromUDP(buf)
		if err != nil {
			return
		}
		pkt := append([]byte(nil), buf[:n]...)
		s.rt.Post(func() {
			if gen != k.gen {
				return
			}
			k.SAddr, k.SPort = from.IP.String(), from.Port
			k.rq = append(k.rq, pkt...)
			k.Rcvd += int64(n)
			k.lastRecv = time.Now()
			s.deliver(k, "UDPREAD")
		})
	}
}
