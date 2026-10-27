// Package sockets implements mIRC's /sockopen, /socklisten and /sockudp
// families: TCP and UDP sockets driven by script events.
package sockets

import (
	"bytes"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/ValwareIRC/msl-runtime/internal/data"
	"github.com/ValwareIRC/msl-runtime/internal/interp"
	"github.com/ValwareIRC/msl-runtime/internal/script"
)

// maxSendQueue is how many bytes may wait to be sent on a socket.
const maxSendQueue = 16384

// Socket is one named socket.
type Socket struct {
	Name     string
	Kind     string // TCP or UDP
	Listen   bool
	Addr     string // address as given
	IP       string
	Port     int
	Status   string // active, listening, connecting
	Sent     int64
	Rcvd     int64
	Mark     string
	SAddr    string // last UDP sender
	SPort    int
	SSL      bool
	Paused   bool
	BindIP   string
	BindPort int
	WSErr    int
	WSMsg    string

	opened   time.Time
	lastSent time.Time
	lastRecv time.Time
	rq       []byte // receive queue
	sq       int    // bytes waiting to be written
	conn     net.Conn
	udp      *net.UDPConn
	ln       net.Listener
	pending  net.Conn // connection waiting for /sockaccept
	out      chan []byte
	gen      int
	read     readState
	closed   bool
	udpKeep  bool
}

// readState tracks /sockread calls during one read event.
type readState struct {
	called   bool
	binvar   bool
	progress bool
}

// Sockets is the per-runtime socket table.
type Sockets struct {
	rt      *interp.Runtime
	data    *data.Data
	list    []*Socket
	sockerr int
	sockbr  int
	bindIP  string
}

type socketsKey struct{}

// Install registers the socket commands, identifiers and events.
func Install(rt *interp.Runtime) *Sockets {
	s := &Sockets{rt: rt, data: data.From(rt)}
	interp.Ext(rt, socketsKey{}, func() *Sockets { return s })
	s.installCommands()
	s.installIdents()
	rt.DeclareEvents("SOCKOPEN", "SOCKCLOSE", "SOCKREAD", "SOCKWRITE", "SOCKLISTEN", "UDPREAD")
	return s
}

// Get returns a socket by exact name.
func (s *Sockets) Get(name string) *Socket {
	for _, k := range s.list {
		if strings.EqualFold(k.Name, name) {
			return k
		}
	}
	return nil
}

// Match returns sockets whose names match a wildcard.
func (s *Sockets) Match(pattern string) []*Socket {
	var out []*Socket
	for _, k := range s.list {
		if interp.WildMatch(pattern, k.Name, false) {
			out = append(out, k)
		}
	}
	return out
}

func (s *Sockets) add(k *Socket) { k.opened = time.Now(); s.list = append(s.list, k) }

// remove closes a socket and drops it from the table.
func (s *Sockets) remove(k *Socket) {
	if k.closed {
		return
	}
	k.closed = true
	k.gen++
	if k.out != nil {
		close(k.out)
		k.out = nil
	} else if k.conn != nil {
		k.conn.Close()
	}
	if k.udp != nil {
		k.udp.Close()
	}
	if k.ln != nil {
		k.ln.Close()
	}
	if k.pending != nil {
		k.pending.Close()
	}
	for i, x := range s.list {
		if x == k {
			s.list = append(s.list[:i], s.list[i+1:]...)
			return
		}
	}
}

// winsockError maps a Go network error to a Winsock error number and
// message, which scripts check through $sock().wserr.
func winsockError(err error) (int, string) {
	switch {
	case err == nil:
		return 0, ""
	case errors.Is(err, syscall.ECONNREFUSED):
		return 10061, "Connection refused"
	case errors.Is(err, syscall.ECONNRESET):
		return 10054, "Connection reset by peer"
	case errors.Is(err, os.ErrDeadlineExceeded):
		return 10060, "Connection timed out"
	case errors.Is(err, syscall.EADDRINUSE):
		return 10048, "Address already in use"
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return 11001, "Host not found"
	}
	var nerr net.Error
	if errors.As(err, &nerr) && nerr.Timeout() {
		return 10060, "Connection timed out"
	}
	return 10050, err.Error()
}

// fire dispatches a socket event to definitions whose name field matches.
func (s *Sockets) fire(name string, k *Socket, errCode int) {
	s.sockerr = errCode
	ev := &interp.Event{Name: name, Values: map[string]string{"sockname": k.Name, "sockerr": strconv.Itoa(errCode)}}
	sockName := k.Name
	s.rt.Dispatch(interp.Fire{Kind: "on", Name: name, Event: ev,
		Match: func(f *interp.Frame, e *script.Event) (bool, error) {
			return len(e.Fields) > 0 && interp.WildMatch(e.Fields[0], sockName, false), nil
		}})
}

// startIO begins reading and writing on a connected TCP socket.
func (s *Sockets) startIO(k *Socket, c net.Conn) {
	k.conn = c
	k.Status = "active"
	if ra, ok := c.RemoteAddr().(*net.TCPAddr); ok {
		k.IP, k.Port = ra.IP.String(), ra.Port
	}
	if la, ok := c.LocalAddr().(*net.TCPAddr); ok {
		k.BindIP, k.BindPort = la.IP.String(), la.Port
	}
	k.out = make(chan []byte, 256)
	gen := k.gen
	go s.writeLoop(k, c, k.out, gen)
	go s.readLoop(k, c, gen)
}

func (s *Sockets) writeLoop(k *Socket, c net.Conn, out <-chan []byte, gen int) {
	defer c.Close()
	for b := range out {
		_, err := c.Write(b)
		n := len(b)
		s.rt.Post(func() {
			if gen != k.gen {
				return
			}
			k.sq -= n
			k.Sent += int64(n)
			k.lastSent = time.Now()
			if err != nil {
				k.WSErr, k.WSMsg = winsockError(err)
				s.fire("SOCKWRITE", k, 3)
				return
			}
			if k.sq <= 0 {
				k.sq = 0
				s.fire("SOCKWRITE", k, 0)
			}
		})
		if err != nil {
			return
		}
	}
}

func (s *Sockets) readLoop(k *Socket, c net.Conn, gen int) {
	buf := make([]byte, 16384)
	for {
		n, err := c.Read(buf)
		if n > 0 {
			chunk := append([]byte(nil), buf[:n]...)
			s.rt.Post(func() {
				if gen != k.gen {
					return
				}
				k.rq = append(k.rq, chunk...)
				k.Rcvd += int64(len(chunk))
				k.lastRecv = time.Now()
				s.deliver(k, "SOCKREAD")
			})
		}
		if err != nil {
			s.rt.Post(func() {
				if gen != k.gen {
					return
				}
				code := 0
				if !errors.Is(err, io.EOF) {
					k.WSErr, k.WSMsg = winsockError(err)
					code = 3
				}
				if len(k.rq) > 0 {
					s.deliver(k, "SOCKREAD")
				}
				s.fire("SOCKCLOSE", k, code)
				s.remove(k)
			})
			return
		}
	}
}

// deliver fires a read event and applies mIRC's buffer rules: if no script
// read anything the buffer is discarded; if a script read some data, the
// event fires again while complete lines (or binary data) remain.
func (s *Sockets) deliver(k *Socket, event string) {
	for guard := 0; guard < 10000 && len(k.rq) > 0 && !k.Paused && !k.closed; guard++ {
		k.read = readState{}
		s.fire(event, k, 0)
		if !k.read.called {
			k.rq = nil
			return
		}
		again := k.read.progress && (k.read.binvar || bytes.IndexByte(k.rq, '\n') >= 0)
		if !again || event == "UDPREAD" {
			return
		}
	}
}

// dialTLS opens a TCP or TLS connection.
func dial(network, addr string, useTLS, insecure bool, bind string) (net.Conn, error) {
	d := &net.Dialer{Timeout: 30 * time.Second}
	if bind != "" {
		d.LocalAddr = &net.TCPAddr{IP: net.ParseIP(bind)}
	}
	if useTLS {
		host, _, _ := net.SplitHostPort(addr)
		return tls.DialWithDialer(d, network, addr, &tls.Config{ServerName: host, InsecureSkipVerify: insecure})
	}
	return d.Dial(network, addr)
}
