package client_test

import (
	"bufio"
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ValwareIRC/msl-runtime/internal/engine"
	"github.com/ValwareIRC/msl-runtime/internal/irc"
	"github.com/ValwareIRC/msl-runtime/internal/script"
)

type output struct{}

func (output) Echo(string, string) {}
func (output) Error(text string)   { println("script error:", text) }

// server is a scripted IRC server for one client.
type server struct {
	t    *testing.T
	ln   net.Listener
	conn net.Conn
	r    *bufio.Reader
}

func (s *server) send(lines ...string) {
	for _, l := range lines {
		s.conn.Write([]byte(l + "\r\n"))
	}
}

func (s *server) expect(prefix string) string {
	s.t.Helper()
	s.conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	for {
		line, err := s.r.ReadString('\n')
		if err != nil {
			s.t.Fatalf("waiting for %q: %v", prefix, err)
		}
		if line = strings.TrimRight(line, "\r\n"); strings.HasPrefix(line, prefix) {
			return line
		}
	}
}

// eval evaluates an identifier on the runtime goroutine.
func eval(eng *engine.Engine, text string) string {
	res := make(chan string, 1)
	eng.RT.Post(func() {
		v, _ := eng.RT.NewFrame(nil, nil).Eval(text)
		res <- v
	})
	return <-res
}

func TestChatClient(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	dir := t.TempDir()
	eng := engine.New(output{}, dir)
	src, err := os.ReadFile("chat.mrc")
	if err != nil {
		t.Fatal(err)
	}
	if _, errs := eng.RT.LoadSource("chat.mrc", string(src), script.Remote); len(errs) > 0 {
		t.Fatal(errs)
	}
	eng.IRC.Identity = irc.Identity{Nick: "me", User: "me", RealName: "Me"}
	c := eng.IRC.Active()
	c.Identity = eng.IRC.Identity
	eng.Start()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { eng.Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()

	eng.RT.Post(func() { eng.UI.Input("@chat", "/server 127.0.0.1 "+strings.Split(ln.Addr().String(), ":")[1]) })
	conn, err := ln.Accept()
	if err != nil {
		t.Fatal(err)
	}
	s := &server{t: t, ln: ln, conn: conn, r: bufio.NewReader(conn)}
	s.expect("CAP LS")
	s.send(":srv CAP * LS :message-tags echo-message server-time")
	s.expect("CAP REQ :message-tags server-time")
	s.send(":srv CAP * ACK :message-tags server-time")
	s.expect("CAP END")
	s.send(":srv 001 me :Welcome", ":srv 005 me NETWORK=TestNet CHANTYPES=# PREFIX=(ov)@+ :ok", ":srv 376 me :End")
	s.expect("CAP REQ :echo-message")
	s.send(":srv CAP me ACK :echo-message")

	eng.RT.Post(func() { eng.UI.Input("@chat", "/join #chat") })
	s.expect("JOIN #chat")
	s.send(":me!me@h JOIN #chat", ":srv 353 me = #chat :me alice bob", ":srv 366 me #chat :End")
	s.send(
		"@msgid=m1;time=2026-10-24T10:00:00.000Z :alice!a@h PRIVMSG #chat :hello everyone, welcome!",
		"@+typing=active :bob!b@h TAGMSG #chat",
		"@+draft/react=👍;+draft/reply=m1 :bob!b@h TAGMSG #chat",
		"@msgid=m2;+draft/reply=m1 :bob!b@h PRIVMSG #chat :thanks alice",
	)
	waitFor(t, func() bool { return eval(eng, "$hget(chat:1:#chat,n)") == "3" }, func() string { return eval(eng, "n= $hget(chat:1:#chat,n) bufs= %chat.bufs cur= %chat.cur me= $me") })
	if got := eval(eng, "$hget(chat:1:#chat,3.reply) $hget(chat:1:#chat,2.r.👍)"); got != "m1 bob" {
		t.Errorf("reply and reaction = %q", got)
	}

	// Typing in the editbox sends a typing notice; Enter sends a reply.
	eng.RT.Post(func() {
		eng.UI.EditboxChanged("@chat", "h")
	})
	s.expect("@+typing=active TAGMSG #chat")
	eng.RT.Post(func() {
		eng.RT.Exec("set %chat.reply 1:#chat 2")
		eng.UI.EditboxChanged("@chat", "")
		eng.UI.Input("@chat", "hi alice, glad to be here")
	})
	s.expect("@+draft/reply=m1 PRIVMSG #chat :hi alice, glad to be here")
	// The echo gives my pending message its msgid instead of adding a copy.
	s.send("@msgid=m3;+draft/reply=m1 :me!me@h PRIVMSG #chat :hi alice, glad to be here")
	waitFor(t, func() bool { return eval(eng, "$hget(chat:1:#chat,4.id)") == "m3" }, func() string {
		return eval(eng, "n= $hget(chat:1:#chat,n) 6= $hget(chat:1:#chat,6.text) 6p= $hget(chat:1:#chat,6.pending) 7= $hget(chat:1:#chat,7.text)")
	})
	if n := eval(eng, "$hget(chat:1:#chat,n)"); n != "4" {
		t.Errorf("messages = %s, want 4 (no duplicate echo)", n)
	}

	// Reacting with /react sends a TAGMSG and shows my reaction.
	eng.RT.Post(func() {
		eng.RT.Exec("set %chat.reply 1:#chat 3")
		eng.UI.Input("@chat", "/react 🎉")
	})
	s.expect("@+draft/react=🎉;+draft/reply=m2 TAGMSG #chat")

	shot := filepath.Join(dir, "chat.png")
	eng.RT.Post(func() { eng.RT.Exec("drawsave @chat " + shot) })
	waitFor(t, func() bool { _, err := os.Stat(shot); return err == nil }, func() string { return "no screenshot" })
	if out := os.Getenv("CHAT_SHOT"); out != "" {
		data, _ := os.ReadFile(shot)
		os.WriteFile(out, data, 0o644)
	}
}

func waitFor(t *testing.T, ok func() bool, state func() string) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if ok() {
			return
		}
	}
	t.Fatal("timed out: ", state())
}
