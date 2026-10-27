package irc_test

import (
	"bufio"
	"context"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ValwareIRC/msl-runtime/internal/engine"
	"github.com/ValwareIRC/msl-runtime/internal/irc"
	"github.com/ValwareIRC/msl-runtime/internal/script"
)

// fakeServer is a scripted IRC server for one client.
type fakeServer struct {
	t    *testing.T
	ln   net.Listener
	conn net.Conn
	r    *bufio.Reader
}

func newFakeServer(t *testing.T) *fakeServer {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	return &fakeServer{t: t, ln: ln}
}

func (s *fakeServer) port() int { return s.ln.Addr().(*net.TCPAddr).Port }

func (s *fakeServer) accept() {
	c, err := s.ln.Accept()
	if err != nil {
		s.t.Fatal(err)
	}
	s.conn = c
	s.r = bufio.NewReader(c)
	s.t.Cleanup(func() { c.Close() })
}

func (s *fakeServer) send(lines ...string) {
	for _, l := range lines {
		if _, err := s.conn.Write([]byte(l + "\r\n")); err != nil {
			s.t.Fatal(err)
		}
	}
}

// expect reads client lines until one starts with prefix.
func (s *fakeServer) expect(prefix string) string {
	s.t.Helper()
	s.conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	for {
		line, err := s.r.ReadString('\n')
		if err != nil {
			s.t.Fatalf("waiting for %q: %v", prefix, err)
		}
		line = strings.TrimRight(line, "\r\n")
		if strings.HasPrefix(line, prefix) {
			return line
		}
	}
}

type output struct {
	mu    sync.Mutex
	lines []string
}

func (o *output) Echo(_, text string) { o.mu.Lock(); o.lines = append(o.lines, text); o.mu.Unlock() }
func (o *output) Error(text string)   { o.Echo("", text) }

func TestSession(t *testing.T) {
	srv := newFakeServer(t)
	out := &output{}
	eng := engine.New(out, t.TempDir())
	_, errs := eng.RT.LoadSource("bot.mrc", `
on *:CONNECT:{ join #test }
on *:JOIN:#test:{ if ($nick == $me) msg $chan hello $chan | else msg $chan welcome $nick }
on *:TEXT:!add & &:#:{ msg $chan $calc($2 + $3) }
on $*:TEXT:/^!rev (\w+)$/i:#:{ msg # reversed }
on *:TEXT:*:?:{ notice $nick private from $nick ( $+ $address $+ ) }
ctcp *:PING:*:{ ctcpreply $nick PING custom | halt }
on *:NICK:{ echo -s nick $nick is now $newnick }
on *:OP:#:{ echo -s $opnick opped by $nick $+ , isop: $iif($opnick isop #,yes,no) }
raw 332:*:{ echo -s topic for $2 is $3- }
alias -l who { return $nick(#test,0) users on #test, ial: $ial(*,0) }
on *:TEXT:!who:#:{ msg # $who }
`, script.Remote)
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	eng.IRC.Identity = irc.Identity{Nick: "bot", User: "bot", RealName: "Test Bot"}
	c := eng.IRC.Active()
	c.Identity = eng.IRC.Identity
	eng.IRC.Connect(c, irc.ServerSpec{Host: "127.0.0.1", Port: srv.port()})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { eng.Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()

	srv.accept()
	srv.expect("CAP LS")
	srv.expect("NICK bot")
	srv.expect("USER bot")
	srv.send(":irc.test CAP * LS :multi-prefix")
	srv.expect("CAP REQ :multi-prefix")
	srv.send(":irc.test CAP * ACK :multi-prefix")
	srv.expect("CAP END")
	srv.send(
		":irc.test 001 bot :Welcome",
		":irc.test 005 bot PREFIX=(ov)@+ CHANTYPES=# NETWORK=TestNet :are supported",
		":irc.test 376 bot :End of MOTD",
	)
	srv.expect("JOIN #test")
	srv.send(
		":bot!bot@host JOIN #test",
		":irc.test 332 bot #test :the topic",
		":irc.test 353 bot = #test :bot @alice +carol",
		":irc.test 366 bot #test :End",
	)
	srv.expect("PRIVMSG #test :hello #test")
	srv.send(":dave!d@dave.host JOIN #test")
	srv.expect("PRIVMSG #test :welcome dave")
	srv.send(":alice!a@alice.host PRIVMSG #test :!add 2 40")
	srv.expect("PRIVMSG #test :42")
	srv.send(":alice!a@alice.host PRIVMSG #test :!REV word")
	srv.expect("PRIVMSG #test :reversed")
	srv.send(":alice!a@alice.host PRIVMSG bot :hi there")
	srv.expect("NOTICE alice :private from alice (a@alice.host)")
	srv.send(":alice!a@alice.host PRIVMSG bot :\x01PING 123\x01")
	srv.expect("NOTICE alice :\x01PING custom\x01")
	srv.send(":alice!a@alice.host PRIVMSG bot :\x01VERSION\x01")
	srv.expect("NOTICE alice :\x01VERSION " + irc.Version + "\x01")
	srv.send(":alice!a@alice.host MODE #test +o dave")
	srv.send(":carol!c@carol.host NICK carol2")
	srv.send(":alice!a@alice.host PRIVMSG #test :!who")
	srv.expect("PRIVMSG #test :4 users on #test, ial: 4")
	srv.send("PING :xyz")
	srv.expect("PONG :xyz")

	want := []string{
		"topic for #test is the topic",
		"dave opped by alice, isop: yes",
		"nick carol is now carol2",
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		out.mu.Lock()
		got := strings.Join(out.lines, "\n")
		out.mu.Unlock()
		ok := true
		for _, w := range want {
			if !strings.Contains(got, w) {
				ok = false
			}
		}
		if ok {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	out.mu.Lock()
	defer out.mu.Unlock()
	t.Fatalf("echo output missing lines; got:\n%s", strings.Join(out.lines, "\n"))
}

func TestMask(t *testing.T) {
	addr := "nick!~user@ppp1.dialup.mirc.com"
	for typ, want := range map[int]string{
		0: "*!user@ppp1.dialup.mirc.com", 1: "*!*user@ppp1.dialup.mirc.com",
		2: "*!*@ppp1.dialup.mirc.com", 3: "*!*user@*.dialup.mirc.com", 4: "*!*@*.dialup.mirc.com",
		5: "nick!user@ppp1.dialup.mirc.com", 9: "nick!*@*.dialup.mirc.com",
		12: "*!*@ppp?.dialup.mirc.com",
	} {
		if got := irc.Mask(addr, typ); got != want {
			t.Errorf("Mask(%d) = %s, want %s", typ, got, want)
		}
	}
	if got := irc.Mask("n!u@1.2.3.4", 2+2); got != "*!*@1.2.3.*" {
		t.Errorf("ip mask = %s", got)
	}
	_ = strconv.Itoa
}
