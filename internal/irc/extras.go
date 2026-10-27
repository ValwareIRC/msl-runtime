package irc

import (
	"bufio"
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/proxy"

	"github.com/ValwareIRC/msl-runtime/internal/interp"
)

// Proxy is the /proxy (and /firewall) setting used for server connections.
type Proxy struct {
	On   bool
	Type string // 4, 5 or p (HTTP CONNECT)
	Host string
	Port string
	User string
	Pass string
	DCC  bool
}

// LinkEntry is one server from a LINKS reply ($link).
type LinkEntry struct {
	Addr, Hub, Info string
	Hops            int
}

func (m *Manager) installExtras() {
	cmd := func(name string, fn func(*interp.CmdCall, *Conn) error) {
		m.rt.RegisterCommand(name, interp.Command{Fn: func(cc *interp.CmdCall) error { return fn(cc, m.FrameConn(cc.F)) }})
	}
	id := func(name string, fn func(*interp.Call, *Conn) (string, error)) {
		m.rt.RegisterIdent(name, func(c *interp.Call) (string, error) { return fn(c, m.FrameConn(c.F)) })
	}
	cmd("debug", m.cmdDebug)
	id("debug", func(c *interp.Call, conn *Conn) (string, error) { return conn.debugTarget, nil })
	cmd("proxy", m.cmdProxy)
	cmd("firewall", m.cmdProxy)
	cmd("perform", func(cc *interp.CmdCall, c *Conn) error { return onOff(cc, &m.PerformOn) })
	cmd("nickserv", func(cc *interp.CmdCall, c *Conn) error {
		_, rest := cc.Switches()
		return sendf(cc, c, "NICKSERV %s", rest)
	})
	id("link", func(c *interp.Call, conn *Conn) (string, error) {
		n := interp.Int(c.Arg(0))
		if n == 0 {
			return strconv.Itoa(len(conn.links)), nil
		}
		if n < 1 || n > len(conn.links) {
			return "", nil
		}
		l := conn.links[n-1]
		switch strings.ToLower(c.Prop) {
		case "level":
			return strconv.Itoa(l.Hops), nil
		case "info":
			return l.Info, nil
		case "ip":
			if addrs, err := net.LookupHost(l.Addr); err == nil && len(addrs) > 0 {
				return addrs[0], nil
			}
			return "", nil
		}
		return l.Addr, nil
	})
	id("ialmark", func(c *interp.Call, conn *Conn) (string, error) {
		e := conn.ial(c.Arg(0))
		if e == nil || e.Mark == "" {
			return "", nil
		}
		if strings.EqualFold(c.Prop, "name") {
			return e.Nick, nil
		}
		return e.Mark, nil
	})
	id("msgstamp", func(c *interp.Call, conn *Conn) (string, error) { v, _ := c.F.Event.Get("msgstamp"); return v, nil })
	id("msgtags", identMsgtags)
	id("parseem", func(c *interp.Call, conn *Conn) (string, error) {
		v, ok := c.F.Event.Get("parseem")
		if !ok {
			return interp.Bool(false), nil
		}
		return v, nil
	})
	for _, n := range []string{"modefirst", "modelast"} {
		key := n
		id(n, func(c *interp.Call, conn *Conn) (string, error) {
			v, ok := c.F.Event.Get(key)
			if !ok {
				return interp.Bool(false), nil
			}
			return v, nil
		})
	}
	id("sslhash", identSSLHash)
}

// identMsgtags implements $msgtags (all tags) and $msgtags(tag|N).key|value.
func identMsgtags(c *interp.Call, conn *Conn) (string, error) {
	raw, _ := c.F.Event.Get("msgtags")
	if !c.Paren {
		return raw, nil
	}
	var tags [][2]string
	for _, kv := range strings.Split(raw, ";") {
		if kv == "" {
			continue
		}
		k, v, _ := strings.Cut(kv, "=")
		tags = append(tags, [2]string{k, unescapeTag(v)})
	}
	var tag *[2]string
	if n, ok := interp.ParseNumber(c.Arg(0)); ok {
		i := int(n)
		if i == 0 {
			return strconv.Itoa(len(tags)), nil
		}
		if i >= 1 && i <= len(tags) {
			tag = &tags[i-1]
		}
	} else {
		for i := range tags {
			if tags[i][0] == c.Arg(0) {
				tag = &tags[i]
			}
		}
	}
	if tag == nil {
		return "", nil
	}
	switch strings.ToLower(c.Prop) {
	case "key":
		return tag[0], nil
	case "value", "":
		if c.Prop == "" && !strings.Contains(c.Arg(0), "") {
			return tag[0], nil
		}
		return tag[1], nil
	}
	return tag[1], nil
}

// identSSLHash implements $sslhash(method, type).colons for the server certificate.
func identSSLHash(c *interp.Call, conn *Conn) (string, error) {
	if len(conn.peerCert) == 0 || strings.EqualFold(c.Arg(1), "p") {
		return "", nil
	}
	var h hash.Hash
	switch strings.ToLower(c.Arg(0)) {
	case "md5":
		h = md5.New()
	case "sha1":
		h = sha1.New()
	case "sha512":
		h = sha512.New()
	default:
		h = sha256.New()
	}
	h.Write(conn.peerCert)
	sum := hex.EncodeToString(h.Sum(nil))
	if strings.EqualFold(c.Prop, "colons") {
		var parts []string
		for i := 0; i < len(sum); i += 2 {
			parts = append(parts, sum[i:i+2])
		}
		return strings.Join(parts, ":"), nil
	}
	return sum, nil
}

// cmdDebug implements /debug [-cinptrNoN] [N] [on|off|@window|filename] [identifier].
func (m *Manager) cmdDebug(cc *interp.CmdCall, c *Conn) error {
	sw, rest := cc.Switches()
	f := strings.Fields(rest)
	if len(f) > 0 {
		if _, ok := interp.ParseNumber(f[0]); ok {
			f = f[1:]
		}
	}
	if len(f) == 0 {
		return nil
	}
	switch strings.ToLower(f[0]) {
	case "off":
		if strings.Contains(sw, "c") && strings.HasPrefix(c.debugTarget, "@") {
			cc.F.ExecLine("!window -c "+c.debugTarget, 0)
		}
		c.debugTarget = ""
		return nil
	case "on":
		c.debugTarget = "debug.log"
	default:
		c.debugTarget = f[0]
	}
	c.debugStamp = strings.Contains(sw, "t")
	c.debugIdent = ""
	if strings.Contains(sw, "i") && len(f) > 1 {
		c.debugIdent = f[1]
	}
	if strings.HasPrefix(c.debugTarget, "@") {
		return cc.F.ExecLine("!window -n "+c.debugTarget, 0)
	}
	return nil
}

// debugLine records a raw line for /debug.
func (c *Conn) debugLine(dir, line string) {
	if c.debugTarget == "" {
		return
	}
	text := dir + " " + line
	if c.debugIdent != "" {
		f := c.rt().NewFrame(nil, nil)
		f.Params = strings.Fields(text)
		if v, err := f.Eval("$" + c.debugIdent + "(" + text + ")"); err == nil {
			text = v
		}
		if text == "" {
			return
		}
	}
	if c.debugStamp {
		text = time.Now().Format("[15:04:05] ") + text
	}
	if strings.HasPrefix(c.debugTarget, "@") {
		c.rt().Out.Echo(c.debugTarget, text)
		return
	}
	path := c.debugTarget
	if c.mgr.ResolvePath != nil {
		path = c.mgr.ResolvePath(path)
	}
	if fh, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644); err == nil {
		fh.WriteString(text + "\r\n")
		fh.Close()
	}
}

// cmdProxy implements /proxy and /firewall [-cmN[+|-]d] [on|off] <server> <port> <userid> <password>.
func (m *Manager) cmdProxy(cc *interp.CmdCall, c *Conn) error {
	text := strings.TrimSpace(cc.Args)
	for strings.HasPrefix(text, "-") || strings.HasPrefix(text, "+") {
		word, rest, _ := strings.Cut(text, " ")
		switch {
		case word == "+d":
			m.Proxy.DCC = true
		case word == "-d":
			m.Proxy.DCC = false
		default:
			for i := 1; i < len(word); i++ {
				switch word[i] {
				case 'c':
					m.Proxy.User, m.Proxy.Pass = "", ""
				case 'm':
					if i+1 < len(word) {
						m.Proxy.Type = string(word[i+1])
						i++
					}
				case 'd':
					m.Proxy.DCC = word[0] == '+'
				}
			}
		}
		text = strings.TrimSpace(rest)
	}
	f := strings.Fields(text)
	if len(f) > 0 {
		switch strings.ToLower(f[0]) {
		case "on":
			m.Proxy.On, f = true, f[1:]
		case "off":
			m.Proxy.On, f = false, f[1:]
		}
	}
	for i, dst := range []*string{&m.Proxy.Host, &m.Proxy.Port, &m.Proxy.User, &m.Proxy.Pass} {
		if i < len(f) {
			*dst = f[i]
		}
	}
	if m.Proxy.Type == "" {
		m.Proxy.Type = "5"
	}
	return nil
}

// dialServer connects to addr directly or through the configured proxy.
func (m *Manager) dialServer(network, addr string, d *net.Dialer) (net.Conn, error) {
	p := m.Proxy
	if !p.On || p.Host == "" {
		return d.Dial(network, addr)
	}
	paddr := net.JoinHostPort(p.Host, p.Port)
	switch p.Type {
	case "5":
		var auth *proxy.Auth
		if p.User != "" {
			auth = &proxy.Auth{User: p.User, Password: p.Pass}
		}
		dialer, err := proxy.SOCKS5("tcp", paddr, auth, d)
		if err != nil {
			return nil, err
		}
		return dialer.Dial("tcp", addr)
	case "4":
		return socks4(d, paddr, addr, p.User)
	}
	return httpConnect(d, paddr, addr, p.User, p.Pass)
}

// socks4 connects through a SOCKS4a proxy.
func socks4(d *net.Dialer, proxyAddr, addr, user string) (net.Conn, error) {
	host, portText, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	port, _ := strconv.Atoi(portText)
	conn, err := d.Dial("tcp", proxyAddr)
	if err != nil {
		return nil, err
	}
	req := []byte{4, 1, 0, 0, 0, 0, 0, 1}
	binary.BigEndian.PutUint16(req[2:], uint16(port))
	if ip := net.ParseIP(host).To4(); ip != nil {
		copy(req[4:], ip)
		req = append(append(req, user...), 0)
	} else {
		req = append(append(append(append(req, user...), 0), host...), 0)
	}
	if _, err := conn.Write(req); err != nil {
		conn.Close()
		return nil, err
	}
	reply := make([]byte, 8)
	if _, err := conn.Read(reply); err != nil || reply[1] != 90 {
		conn.Close()
		return nil, errors.New("socks4 proxy refused the connection")
	}
	return conn, nil
}

// httpConnect tunnels through an HTTP proxy with CONNECT.
func httpConnect(d *net.Dialer, proxyAddr, addr, user, pass string) (net.Conn, error) {
	conn, err := d.Dial("tcp", proxyAddr)
	if err != nil {
		return nil, err
	}
	req := "CONNECT " + addr + " HTTP/1.1\r\nHost: " + addr + "\r\n"
	if user != "" {
		req += "Proxy-Authorization: Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"+pass)) + "\r\n"
	}
	if _, err := conn.Write([]byte(req + "\r\n")); err != nil {
		conn.Close()
		return nil, err
	}
	r := bufio.NewReader(conn)
	status, err := r.ReadString('\n')
	if err != nil || !strings.Contains(status, " 200") {
		conn.Close()
		return nil, fmt.Errorf("proxy refused: %s", strings.TrimSpace(status))
	}
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			conn.Close()
			return nil, err
		}
		if strings.TrimSpace(line) == "" {
			break
		}
	}
	return conn, nil
}
