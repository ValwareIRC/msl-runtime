package irc

import (
	"bufio"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/ValwareIRC/msl-runtime/internal/interp"
)

// sendQueue is how many outgoing lines may wait for the writer.
const sendQueue = 4096

// Status is a connection's state; the values match /scid -tM bits.
type Status int

const (
	Disconnected Status = iota
	Connecting
	LoggingIn
	Connected
)

func (s Status) String() string {
	return [...]string{"disconnected", "connecting", "loggingon", "connected"}[s]
}

// ServerSpec is what /server or the configuration asks to connect to.
type ServerSpec struct {
	Host     string
	Port     int
	TLS      bool
	Password string
	// SASL method ("", "sasl"/"plain", "external") and credentials.
	LoginMethod string
	LoginName   string
	LoginPass   string
	IPv6        bool
	// Join lists channels (with optional keys) to join after connecting.
	Join []string
	// NoConnectEvent, NoPerform, NoAutojoin mirror /server -c -p -o.
	NoConnectEvent bool
	NoAutojoin     bool
	SkipSTS        bool
}

// Identity is the nick and user details for a connection.
type Identity struct {
	Nick     string
	AltNick  string
	User     string
	RealName string
	Email    string
}

// Conn is one server connection ("server window" in mIRC terms).
type Conn struct {
	ID  int
	mgr *Manager

	Spec     ServerSpec
	Identity Identity
	Status   Status

	Me        string
	ServerID  string // the server's own name from 001
	Support   Support
	Caps      map[string]string // enabled IRCv3 capabilities
	available map[string]string
	Channels  map[string]*Channel
	IAL       map[string]*IALEntry
	UserModes string
	Away      bool
	AwayMsg   string
	AwayAt    time.Time
	Lag       time.Duration

	ConnectedAt time.Time
	LastSent    time.Time
	IP          string
	LocalIP     string
	LocalHost   string

	queries    orderedSet
	lastInvite string
	links      []LinkEntry
	peerCert   []byte

	debugTarget string
	debugStamp  bool
	debugIdent  string

	conn     net.Conn
	out      chan string
	gen      int // bumps on each connect so stale reader goroutines are ignored
	nickTry  int
	quitting bool
	pingSent time.Time
}

func newConn(m *Manager, id int) *Conn {
	c := &Conn{ID: id, mgr: m, Support: defaultSupport()}
	c.reset()
	return c
}

func (c *Conn) reset() {
	c.Caps = map[string]string{}
	c.available = map[string]string{}
	c.Channels = map[string]*Channel{}
	c.IAL = map[string]*IALEntry{}
	c.Support = defaultSupport()
	c.UserModes = ""
	c.Away = false
}

func (c *Conn) rt() *interp.Runtime { return c.mgr.rt }

// Fold lowers a nick or channel with the server's case mapping.
func (c *Conn) Fold(s string) string { return c.Support.Fold(s) }

// Network returns the network name, falling back to the server name.
func (c *Conn) Network() string {
	if c.Support.Network != "" {
		return c.Support.Network
	}
	return c.ServerID
}

// Connect dials the server in the background. Results arrive on the runtime
// goroutine through Post.
func (c *Conn) Connect(spec ServerSpec) {
	if c.conn != nil {
		c.Quit("")
		c.closeNet()
	}
	c.Spec = spec
	c.reset()
	c.gen++
	gen := c.gen
	c.Status = Connecting
	c.nickTry = 0
	c.quitting = false
	if c.Identity.Nick == "" {
		c.Identity.Nick = "mslrt"
	}
	addr := net.JoinHostPort(spec.Host, strconv.Itoa(spec.Port))
	go func() {
		d := &net.Dialer{Timeout: 30 * time.Second}
		network := "tcp"
		if spec.IPv6 {
			network = "tcp6"
		}
		var nc net.Conn
		var err error
		nc, err = c.mgr.dialServer(network, addr, d)
		var cert []byte
		if err == nil && spec.TLS {
			tc := tls.Client(nc, &tls.Config{ServerName: spec.Host})
			if err = tc.Handshake(); err == nil {
				if certs := tc.ConnectionState().PeerCertificates; len(certs) > 0 {
					cert = certs[0].Raw
				}
				nc = tc
			}
		}
		c.mgr.rt.Post(func() {
			if gen != c.gen {
				if nc != nil {
					nc.Close()
				}
				return
			}
			if err != nil {
				c.Status = Disconnected
				c.mgr.fireConnectFail(c, err)
				return
			}
			c.conn = nc
			c.peerCert = cert
			c.out = make(chan string, sendQueue)
			go c.writeLoop(nc, c.out)
			c.IP, _, _ = net.SplitHostPort(nc.RemoteAddr().String())
			c.LocalIP, _, _ = net.SplitHostPort(nc.LocalAddr().String())
			c.Status = LoggingIn
			c.register()
			go c.readLoop(nc, gen)
		})
	}()
}

func (c *Conn) readLoop(nc net.Conn, gen int) {
	r := bufio.NewReaderSize(nc, 64*1024)
	for {
		line, err := r.ReadString('\n')
		if line != "" {
			l := line
			c.mgr.rt.Post(func() {
				if gen == c.gen {
					c.handleLine(strings.TrimRight(l, "\r\n"))
				}
			})
		}
		if err != nil {
			c.mgr.rt.Post(func() {
				if gen == c.gen {
					c.disconnected(err)
				}
			})
			return
		}
	}
}

func (c *Conn) register() {
	c.Send("CAP LS 302")
	if c.Spec.Password != "" {
		c.Send("PASS " + c.Spec.Password)
	}
	c.Send("NICK " + c.Identity.Nick)
	user := c.Identity.User
	if user == "" {
		user = c.Identity.Nick
	}
	real := c.Identity.RealName
	if real == "" {
		real = user
	}
	c.Send("USER " + user + " 0 * :" + real)
	c.Me = c.Identity.Nick
}

// wantedCaps are the IRCv3 capabilities the runtime requests when offered.
var wantedCaps = []string{
	"multi-prefix", "away-notify", "account-notify", "extended-join", "chghost",
	"userhost-in-names", "message-tags", "server-time", "cap-notify",
	"invite-notify", "setname", "account-tag", "batch",
}

func (c *Conn) handleCap(m Message) {
	sub := strings.ToUpper(m.Param(1))
	list := m.Last()
	switch sub {
	case "LS", "NEW":
		for _, tok := range strings.Fields(list) {
			k, v, _ := strings.Cut(tok, "=")
			c.available[k] = v
		}
		if sub == "LS" && m.Param(2) == "*" {
			return // more LS lines follow
		}
		if v, ok := c.available["sts"]; ok && !c.Spec.TLS && !c.Spec.SkipSTS {
			if port := stsPort(v); port > 0 {
				spec := c.Spec
				spec.TLS, spec.Port = true, port
				c.mgr.rt.Out.Echo(interp.StatusWindow, fmt.Sprintf("* STS policy: reconnecting to %s on secure port %d", spec.Host, port))
				c.Connect(spec)
				return
			}
		}
		var req []string
		for _, w := range wantedCaps {
			if _, ok := c.available[w]; ok {
				if _, on := c.Caps[w]; !on {
					req = append(req, w)
				}
			}
		}
		if c.wantsSASL() {
			if _, ok := c.available["sasl"]; ok {
				req = append(req, "sasl")
			}
		}
		if len(req) > 0 {
			c.Send("CAP REQ :" + strings.Join(req, " "))
		} else if sub == "LS" {
			c.Send("CAP END")
		}
	case "ACK":
		for _, k := range strings.Fields(list) {
			if strings.HasPrefix(k, "-") {
				delete(c.Caps, k[1:])
				continue
			}
			c.Caps[k] = c.available[k]
		}
		if _, ok := c.Caps["sasl"]; ok && c.Status == LoggingIn && c.wantsSASL() {
			c.Send("AUTHENTICATE " + c.saslMechanism())
			return
		}
		if c.Status == LoggingIn {
			c.Send("CAP END")
		}
	case "NAK":
		if c.Status == LoggingIn {
			c.Send("CAP END")
		}
	case "DEL":
		for _, k := range strings.Fields(list) {
			delete(c.Caps, k)
			delete(c.available, k)
		}
	}
}

func stsPort(v string) int {
	for _, kv := range strings.Split(v, ",") {
		if k, val, ok := strings.Cut(kv, "="); ok && k == "port" {
			n, _ := strconv.Atoi(val)
			return n
		}
	}
	return 0
}

func (c *Conn) wantsSASL() bool {
	switch strings.ToLower(c.Spec.LoginMethod) {
	case "sasl", "plain", "external":
		return true
	}
	return false
}

func (c *Conn) saslMechanism() string {
	if strings.EqualFold(c.Spec.LoginMethod, "external") {
		return "EXTERNAL"
	}
	return "PLAIN"
}

func (c *Conn) handleAuthenticate(m Message) {
	if m.Param(0) != "+" {
		return
	}
	if c.saslMechanism() == "EXTERNAL" {
		c.Send("AUTHENTICATE +")
		return
	}
	name := c.Spec.LoginName
	if name == "" {
		name = c.Identity.Nick
	}
	payload := base64.StdEncoding.EncodeToString([]byte(name + "\x00" + name + "\x00" + c.Spec.LoginPass))
	for len(payload) >= 400 {
		c.Send("AUTHENTICATE " + payload[:400])
		payload = payload[400:]
	}
	if payload == "" {
		payload = "+"
	}
	c.Send("AUTHENTICATE " + payload)
}

// Send queues one protocol line for the writer. It reports an error when
// not connected. Outgoing lines pass through on PARSELINE first, which may
// rewrite them.
func (c *Conn) Send(line string) error {
	if c.conn == nil || c.out == nil {
		return fmt.Errorf("not connected to server")
	}
	line = strings.NewReplacer("\r", "", "\n", "").Replace(line)
	if line = c.mgr.fireParseline(c, "out", line); line == "" {
		return nil
	}
	c.LastSent = time.Now()
	c.debugLine("->", line)
	select {
	case c.out <- line:
	default:
		return fmt.Errorf("send queue full")
	}
	return nil
}

// writeLoop sends queued lines, pacing them when flood protection is on.
func (c *Conn) writeLoop(nc net.Conn, out <-chan string) {
	defer nc.Close()
	for line := range out {
		if wait := c.mgr.flood.delay(len(line)); wait > 0 {
			time.Sleep(wait)
		}
		if _, err := nc.Write([]byte(line + "\r\n")); err != nil {
			return
		}
	}
}

// Quit sends QUIT and marks the connection as closing.
func (c *Conn) Quit(msg string) {
	if c.conn == nil {
		return
	}
	c.quitting = true
	if msg == "" {
		msg = c.mgr.QuitMessage
	}
	c.Send("QUIT :" + msg)
}

// closeNet stops the connection; the writer sends what is queued (such as
// a QUIT) and then closes the socket.
func (c *Conn) closeNet() {
	if c.out != nil {
		close(c.out)
		c.out = nil
	} else if c.conn != nil {
		c.conn.Close()
	}
	c.conn = nil
}

// Disconnect closes the connection immediately.
func (c *Conn) Disconnect() {
	if c.conn == nil {
		c.Status = Disconnected
		return
	}
	c.gen++
	c.closeNet()
	c.disconnected(nil)
}

func (c *Conn) disconnected(err error) {
	was := c.Status
	c.closeNet()
	c.Status = Disconnected
	if was == Connected || was == LoggingIn {
		c.mgr.fireDisconnect(c, err)
	}
}
