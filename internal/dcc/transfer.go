package dcc

import (
	"bufio"
	"encoding/binary"
	"io"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ValwareIRC/msl-runtime/internal/interp"
	"github.com/ValwareIRC/msl-runtime/internal/irc"
	"github.com/ValwareIRC/msl-runtime/internal/script"
)

// blockSize is how many bytes a DCC send writes per packet.
const blockSize = 4096

// startGet receives a file: it resumes an existing partial file when the
// sender supports it, otherwise connects (or listens, for passive offers).
func (d *DCC) startGet(c *irc.Conn, s *Session) {
	os.MkdirAll(filepath.Dir(s.File), 0o755)
	if have := fileSize(s.File); have > 0 && have < s.Size {
		s.Resume = have
		port := strconv.Itoa(s.Port)
		arg := "RESUME " + quoteName(filepath.Base(s.File)) + " " + port + " " + strconv.FormatInt(have, 10)
		if s.Token != "" {
			arg += " " + s.Token
		}
		c.Send("PRIVMSG " + s.Nick + " :" + irc.EncodeCTCP("DCC", arg))
		return // continues on DCC ACCEPT
	}
	d.connectGet(c, s)
}

func (d *DCC) connectGet(c *irc.Conn, s *Session) {
	if s.Port == 0 && s.Token != "" {
		ln, port, err := listen()
		if err != nil {
			d.failGet(s)
			return
		}
		s.ln, s.Passive = ln, true
		arg := "SEND " + quoteName(filepath.Base(s.File)) + " " + ipArg(localIP(c)) + " " + strconv.Itoa(port) + " " +
			strconv.FormatInt(s.Size, 10) + " " + s.Token
		c.Send("PRIVMSG " + s.Nick + " :" + irc.EncodeCTCP("DCC", arg))
		d.acceptOne(s, func(conn net.Conn) { d.receive(s, conn) })
		return
	}
	gen := s.gen
	addr := net.JoinHostPort(s.IP, strconv.Itoa(s.Port))
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
				d.failGet(s)
				return
			}
			d.receive(s, conn)
		})
	}()
}

func (d *DCC) failGet(s *Session) {
	s.Status = "failed"
	d.fireFile("GETFAIL", s)
	d.remove(s)
}

// receive copies file data, acknowledging each block with the running total.
func (d *DCC) receive(s *Session, conn net.Conn) {
	s.conn, s.Status = conn, "active"
	flags := os.O_CREATE | os.O_WRONLY | os.O_TRUNC
	if s.Resume > 0 {
		flags = os.O_CREATE | os.O_WRONLY | os.O_APPEND
	}
	fh, err := os.OpenFile(s.File, flags, 0o644)
	if err != nil {
		d.failGet(s)
		return
	}
	gen := s.gen
	total := s.Resume
	go func() {
		defer fh.Close()
		buf := make([]byte, 16384)
		var ack [4]byte
		var rerr error
		for s.Size == 0 || total < s.Size {
			n, err := conn.Read(buf)
			if n > 0 {
				if _, werr := fh.Write(buf[:n]); werr != nil {
					rerr = werr
					break
				}
				total += int64(n)
				binary.BigEndian.PutUint32(ack[:], uint32(total))
				conn.Write(ack[:])
				done := total
				d.rt.Post(func() { s.Done, s.last = done, time.Now() })
			}
			if err != nil {
				if err != io.EOF || (s.Size > 0 && total < s.Size) {
					rerr = err
				}
				break
			}
		}
		conn.Close()
		d.rt.Post(func() {
			if gen != s.gen {
				return
			}
			s.Done = total
			if rerr != nil {
				d.failGet(s)
				return
			}
			s.Status, s.success = "sent", true
			d.fireFile("FILERCVD", s)
			d.remove(s)
		})
	}()
}

// offerSend sends a DCC SEND offer and waits for the receiver.
func (d *DCC) offerSend(c *irc.Conn, nick, path string, passive bool) error {
	size := fileSize(path)
	s := d.add(&Session{Kind: "send", Nick: nick, File: path, Size: size, Status: "waiting", cid: c.ID})
	name := quoteName(filepath.Base(path))
	if passive {
		s.Passive, s.Token = true, strconv.FormatInt(time.Now().UnixNano()%1e9, 10)
		return c.Send("PRIVMSG " + nick + " :" + irc.EncodeCTCP("DCC", "SEND "+name+" "+ipArg(localIP(c))+" 0 "+
			strconv.FormatInt(size, 10)+" "+s.Token))
	}
	ln, port, err := listen()
	if err != nil {
		d.remove(s)
		return err
	}
	s.ln, s.Port = ln, port
	d.acceptOne(s, func(conn net.Conn) { d.transmit(s, conn) })
	return c.Send("PRIVMSG " + nick + " :" + irc.EncodeCTCP("DCC", "SEND "+name+" "+ipArg(localIP(c))+" "+
		strconv.Itoa(port)+" "+strconv.FormatInt(size, 10)))
}

func quoteName(n string) string {
	if strings.ContainsRune(n, ' ') {
		return `"` + n + `"`
	}
	return n
}

// transmit writes the file and reads acknowledgements until all bytes are acked.
func (d *DCC) transmit(s *Session, conn net.Conn) {
	s.conn, s.Status = conn, "active"
	if ra, ok := conn.RemoteAddr().(*net.TCPAddr); ok {
		s.IP = ra.IP.String()
	}
	fh, err := os.Open(s.File)
	if err != nil {
		d.failSend(s)
		return
	}
	fh.Seek(s.Resume, io.SeekStart)
	gen := s.gen
	acked := make(chan int64, 64)
	go func() {
		var ack [4]byte
		for {
			if _, err := io.ReadFull(conn, ack[:]); err != nil {
				close(acked)
				return
			}
			acked <- int64(binary.BigEndian.Uint32(ack[:]))
		}
	}()
	go func() {
		defer fh.Close()
		buf := make([]byte, blockSize)
		sent := s.Resume
		var werr error
		for {
			n, err := fh.Read(buf)
			if n > 0 {
				if _, werr = conn.Write(buf[:n]); werr != nil {
					break
				}
				sent += int64(n)
				done := sent
				d.rt.Post(func() { s.Done, s.last = done, time.Now() })
			}
			if err != nil {
				break
			}
		}
		// The transfer succeeded once the receiver acknowledged every byte
		// (acknowledgements are 32-bit, so compare modulo 2^32).
		ok := false
		if werr == nil {
			var last int64
			deadline := time.After(60 * time.Second)
		wait:
			for {
				select {
				case a, more := <-acked:
					if !more {
						break wait
					}
					last = a
					d.rt.Post(func() { s.Acked = a })
					if uint32(a) == uint32(sent) {
						break wait
					}
				case <-deadline:
					break wait
				}
			}
			ok = uint32(last) == uint32(sent)
		}
		conn.Close()
		d.rt.Post(func() {
			if gen != s.gen {
				return
			}
			s.Done = sent
			if !ok {
				d.failSend(s)
				return
			}
			s.Status, s.success = "sent", true
			d.fireFile("FILESENT", s)
			d.remove(s)
		})
	}()
}

func (d *DCC) failSend(s *Session) {
	s.Status = "failed"
	d.fireFile("SENDFAIL", s)
	d.remove(s)
}

// resumeReply handles DCC RESUME (as sender) and DCC ACCEPT (as receiver).
func (d *DCC) resumeReply(c *irc.Conn, kind, nick string, f []string) {
	port := f[1]
	pos, _ := strconv.ParseInt(f[2], 10, 64)
	token := ""
	if len(f) > 3 {
		token = f[3]
	}
	for _, s := range d.sessions {
		if !strings.EqualFold(s.Nick, nick) {
			continue
		}
		switch {
		case kind == "RESUME" && s.Kind == "send" && (strconv.Itoa(s.Port) == port || s.Token == token && token != ""):
			s.Resume = pos
			c.Send("PRIVMSG " + nick + " :" + irc.EncodeCTCP("DCC", "ACCEPT "+f[0]+" "+port+" "+f[2]+" "+token))
			return
		case kind == "ACCEPT" && s.Kind == "get" && (strconv.Itoa(s.Port) == port || s.Token == token && token != ""):
			s.Resume = pos
			d.connectGet(c, s)
			return
		}
	}
}

// fserveWelcome sends the fileserver welcome text.
func (d *DCC) fserveWelcome(s *Session) {
	if s.Cd == "" {
		s.Cd = "\\"
	}
	if s.File != "" {
		if data, err := os.ReadFile(s.File); err == nil {
			for _, l := range strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n") {
				s.conn.Write([]byte(l + "\n"))
			}
		}
	}
	s.conn.Write([]byte("[\\]\n"))
}

// fserveCommand runs a fileserver command: dir/ls, cd, get, read, help, quit.
func (d *DCC) fserveCommand(s *Session, line string) {
	cmd, arg, _ := strings.Cut(strings.TrimSpace(line), " ")
	say := func(t string) { s.conn.Write([]byte(t + "\n")) }
	cur := filepath.Join(s.Root, filepath.FromSlash(strings.ReplaceAll(s.Cd, "\\", "/")))
	resolve := func(p string) (string, bool) {
		full := filepath.Clean(filepath.Join(cur, filepath.FromSlash(strings.ReplaceAll(p, "\\", "/"))))
		root := filepath.Clean(s.Root)
		return full, full == root || strings.HasPrefix(full, root+string(filepath.Separator))
	}
	switch strings.ToLower(cmd) {
	case "dir", "ls":
		entries, _ := os.ReadDir(cur)
		sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
		for _, e := range entries {
			if e.IsDir() {
				say(e.Name() + "\\")
			} else if info, err := e.Info(); err == nil {
				say(e.Name() + " " + strconv.FormatInt(info.Size(), 10))
			}
		}
		say("End of list.")
	case "cd":
		target, ok := resolve(arg)
		if st, err := os.Stat(target); !ok || err != nil || !st.IsDir() {
			say("Invalid directory.")
			return
		}
		rel, _ := filepath.Rel(s.Root, target)
		s.Cd = "\\" + strings.ReplaceAll(strings.TrimPrefix(filepath.ToSlash(rel), "."), "/", "\\")
		if data, err := os.ReadFile(filepath.Join(target, "dirinfo.srv")); err == nil {
			say(strings.TrimRight(string(data), "\r\n"))
		}
		say("[" + s.Cd + "]")
	case "get":
		target, ok := resolve(arg)
		if st, err := os.Stat(target); !ok || err != nil || st.IsDir() {
			say("Invalid file.")
			return
		}
		if c := d.irc.ByID(s.cid); c != nil {
			say("Sending " + filepath.Base(target) + ".")
			d.offerSend(c, s.Nick, target, d.Settings.Passive)
		}
	case "read", "type":
		target, ok := resolve(arg)
		data, err := os.ReadFile(target)
		if !ok || err != nil {
			say("Invalid file.")
			return
		}
		for _, l := range strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n") {
			say(l)
		}
	case "help":
		say("Commands: cd, dir, get, help, ls, quit, read")
	case "quit", "bye", "exit":
		say("Goodbye.")
		s.conn.Close()
	}
}

// startServer runs the DCC Server listener.
func (d *DCC) startServer() error {
	if d.server != nil {
		d.server.Close()
	}
	ln, err := net.Listen("tcp", ":"+strconv.Itoa(d.Settings.ServerPort))
	if err != nil {
		return err
	}
	d.server = ln
	d.Settings.ServerPort = ln.Addr().(*net.TCPAddr).Port
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go d.serverHandshake(conn)
		}
	}()
	return nil
}

// serverHandshake reads a DCC Server request line: 100 nick (chat),
// 110 nick (fserve) or 120 nick size filename (send).
func (d *DCC) serverHandshake(conn net.Conn) {
	conn.SetReadDeadline(time.Now().Add(30 * time.Second))
	r := bufio.NewReader(conn)
	line, err := r.ReadString('\n')
	conn.SetReadDeadline(time.Time{})
	if err != nil {
		conn.Close()
		return
	}
	f := strings.Fields(strings.TrimSpace(line))
	d.rt.Post(func() {
		if len(f) < 2 {
			conn.Close()
			return
		}
		ip := ""
		if ra, ok := conn.RemoteAddr().(*net.TCPAddr); ok {
			ip = ra.IP.String()
		}
		me := d.irc.Active().Me
		kind := map[string]string{"100": "Chat", "110": "Fserve", "120": "Send"}[f[0]]
		if kind == "" || !strings.ContainsRune(d.Settings.ServerKinds, rune(strings.ToLower(kind)[0])) {
			conn.Close()
			return
		}
		s := &Session{Nick: f[1], IP: ip, Status: "active", cid: d.irc.Active().ID}
		if kind == "Send" && len(f) >= 4 {
			s.Kind = "get"
			s.Size, _ = strconv.ParseInt(f[2], 10, 64)
			s.File = filepath.Join(d.getDir(), filepath.Base(strings.Join(f[3:], " ")))
		}
		ev := d.event("DCCSERVER", s)
		halted := d.rt.Dispatch(interp.Fire{Kind: "on", Name: "DCCSERVER", Event: ev,
			Match: func(fr *interp.Frame, e *script.Event) (bool, error) {
				return len(e.Fields) > 0 && strings.EqualFold(e.Fields[0], kind), nil
			}})
		if halted {
			conn.Close()
			return
		}
		switch kind {
		case "Chat":
			conn.Write([]byte("101 " + me + "\n"))
			s.Kind = "chat"
			d.add(s)
			d.startChat(s, wrapReader(conn, r))
		case "Fserve":
			conn.Write([]byte("111 " + me + "\n"))
			s.Kind, s.Root, s.Cd = "fserve", d.path(""), "\\"
			d.add(s)
			d.startChat(s, wrapReader(conn, r))
		case "Send":
			os.MkdirAll(filepath.Dir(s.File), 0o755)
			s.Resume = fileSize(s.File)
			if s.Resume >= s.Size {
				s.Resume = 0
			}
			conn.Write([]byte("121 " + me + " " + strconv.FormatInt(s.Resume, 10) + "\n"))
			d.add(s)
			d.receive(s, wrapReader(conn, r))
		}
	})
}

// bufferedConn keeps bytes already buffered during the handshake.
type bufferedConn struct {
	net.Conn
	r *bufio.Reader
}

func (b bufferedConn) Read(p []byte) (int, error) { return b.r.Read(p) }

func wrapReader(c net.Conn, r *bufio.Reader) net.Conn { return bufferedConn{c, r} }
