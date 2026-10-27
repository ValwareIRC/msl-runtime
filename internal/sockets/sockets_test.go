package sockets_test

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ValwareIRC/msl-runtime/internal/engine"
	"github.com/ValwareIRC/msl-runtime/internal/script"
)

type output struct {
	mu    sync.Mutex
	lines []string
}

func (o *output) Echo(_, text string) { o.mu.Lock(); o.lines = append(o.lines, text); o.mu.Unlock() }
func (o *output) Error(text string)   { o.Echo("", text) }

func TestLoopback(t *testing.T) {
	out := &output{}
	eng := engine.New(out, t.TempDir())
	_, errs := eng.RT.LoadSource("s.mrc", `
alias go {
  socklisten srv
  sockopen cli 127.0.0.1 $sock(srv).port
  sockudp -k u1 0 127.0.0.1 9 x
  sockudp u2 127.0.0.1 $sock(u1).port hello udp
}
on *:SOCKLISTEN:srv:{ sockaccept peer | sockmark peer accepted }
on *:SOCKOPEN:cli:{
  if ($sockerr) { echo -s open failed | return }
  sockwrite -n cli first line
  sockwrite -n cli second line
}
on *:SOCKREAD:peer:{
  :next
  sockread %line
  if ($sockbr == 0) return
  echo -s server got: %line ( $+ $sock(peer).mark $+ )
  sockwrite -n peer echo: %line
  goto next
}
on *:SOCKREAD:cli:{
  sockread %reply
  if ($sockbr == 0) return
  echo -s client got: %reply
  if (%reply == echo: second line) sockclose cli
}
on *:SOCKCLOSE:peer:{ echo -s server saw close, $sock(*,0) left }
on *:UDPREAD:u1:{ sockread -f %pkt | echo -s udp from $sock(u1).saddr $+ : %pkt }
`, script.Remote)
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { eng.Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()
	eng.RT.Post(func() { _ = eng.RT.Exec("go") })

	want := []string{
		"server got: first line (accepted)", "server got: second line (accepted)",
		"client got: echo: first line", "client got: echo: second line",
		"server saw close, 3 left", "udp from 127.0.0.1: hello udp",
	}
	deadline := time.Now().Add(5 * time.Second)
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
	t.Fatalf("got:\n%s", strings.Join(out.lines, "\n"))
}
