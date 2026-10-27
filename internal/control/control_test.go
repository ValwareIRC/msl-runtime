package control_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ValwareIRC/msl-runtime/internal/engine"
	"github.com/ValwareIRC/msl-runtime/internal/interp"
	"github.com/ValwareIRC/msl-runtime/internal/script"
)

type output struct {
	mu    sync.Mutex
	lines []string
}

func (o *output) Echo(_, text string) { o.mu.Lock(); o.lines = append(o.lines, text); o.mu.Unlock() }
func (o *output) Error(text string)   { o.Echo("", text) }

func (o *output) waitFor(t *testing.T, want ...string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		o.mu.Lock()
		got := strings.Join(o.lines, "\n")
		o.mu.Unlock()
		missing := false
		for _, w := range want {
			if !strings.Contains(got, w) {
				missing = true
			}
		}
		if !missing {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	t.Fatalf("missing %q in output:\n%s", want, strings.Join(o.lines, "\n"))
}

// start runs an engine with src loaded and returns it with its output.
func start(t *testing.T, dir, src string) (*engine.Engine, *output) {
	t.Helper()
	out := &output{}
	eng := engine.New(out, dir)
	if _, errs := eng.RT.LoadSource(filepath.Join(dir, "t.mrc"), src, script.Remote); len(errs) > 0 {
		t.Fatal(errs)
	}
	eng.Start()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { eng.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	return eng, out
}

func exec(eng *engine.Engine, line string) { eng.RT.Post(func() { _ = eng.RT.Exec(line) }) }

func TestTimersSignalsGroups(t *testing.T) {
	eng, out := start(t, t.TempDir(), `
on *:START:{ echo -s started $starting }
alias go {
  .timertick -m 3 20 echo -s tick $!ctimer
  timerlater 1 0 echo -s delayed after script
  echo -s script done $timer(0) $timer(tick).reps
  signal hello a b
  echo -s before signal
  signal -n now x
}
on *:SIGNAL:hel*:{ echo -s signal $signal $1- }
on *:SIGNAL:now:{ echo -s immediate $1 }
#extra off
on *:SIGNAL:hello:{ echo -s disabled group ran }
#extra end
alias toggle { enable #extra | echo -s group $group(#extra) $group(0) }
`)
	out.waitFor(t, "started 1")
	exec(eng, "go")
	out.waitFor(t, "script done 2 3", "immediate x", "before signal", "signal hello a b",
		"delayed after script", "tick tick")
	out.mu.Lock()
	order := strings.Join(out.lines, "|")
	out.mu.Unlock()
	if strings.Index(order, "before signal") > strings.Index(order, "signal hello") {
		t.Errorf("deferred signal ran before the script finished: %s", order)
	}
	exec(eng, "toggle")
	out.waitFor(t, "group on 1")
}

func TestUsersLevelsAndPersistence(t *testing.T) {
	dir := t.TempDir()
	eng, out := start(t, dir, `
alias setup {
  auser 5 friend!*@*
  auser =10 boss
  auser -a 20 boss
  set %kept value one
  set -u60 %temp gone
  echo -s levels $level(friend!*@*) $level(boss) $ulist(*,10,0) $ulist(*,20,1)
  alias hi echo -s hi from alias
  hi
  echo -s isalias $isalias(hi) $isalias(hi).ftype
  saveini
}
`)
	exec(eng, "setup")
	out.waitFor(t, "levels 5 =10,20 1 boss", "hi from alias", "isalias $true alias")
	vars, err := os.ReadFile(filepath.Join(dir, "vars.ini"))
	if err != nil || !strings.Contains(string(vars), "%kept value one") || strings.Contains(string(vars), "%temp") {
		t.Fatalf("vars.ini = %q, %v", vars, err)
	}
	users, _ := os.ReadFile(filepath.Join(dir, "users.ini"))
	if !strings.Contains(string(users), "=10,20:boss") {
		t.Fatalf("users.ini = %q", users)
	}

	// A fresh engine in the same directory restores both.
	eng2, out2 := start(t, dir, `alias check { echo -s restored %kept $level(boss) }`)
	exec(eng2, "check")
	out2.waitFor(t, "restored value one =10,20")
}

func TestEventLevels(t *testing.T) {
	out := &output{}
	eng := engine.New(out, t.TempDir())
	eng.RT.LoadSource("lv.mrc", `
on 1:SIGNAL:x:{ echo -s level 1 for $ulevel }
on 5:SIGNAL:x:{ echo -s level 5 for $ulevel }
on +10:SIGNAL:x:{ echo -s exact 10 }
`, script.Remote)
	eng.RT.Exec("auser 7 seven!*@*")
	for _, addr := range []string{"seven!u@h", "nobody!u@h"} {
		eng.RT.Dispatch(interp.Fire{Kind: "on", Name: "SIGNAL", Address: addr,
			Event: &interp.Event{Values: map[string]string{}},
			Match: func(f *interp.Frame, e *script.Event) (bool, error) { return true, nil }})
	}
	got := strings.Join(out.lines, "|")
	if got != "level 5 for 7|level 1 for 1" {
		t.Fatalf("got %s", got)
	}
}

func TestLevelComparison(t *testing.T) {
	out := &output{}
	eng := engine.New(out, t.TempDir())
	eng.RT.LoadSource("cmp.mrc", `
on >=1:SIGNAL:x:{ echo -s legal }
on 1:SIGNAL:x:{ echo -s illegal }
`, script.Remote)
	eng.RT.Exec("auser 10 boss!*@*")
	eng.RT.Exec("auser 5 helper!*@*")
	fire := func(by, target string) {
		eng.RT.Dispatch(interp.Fire{Kind: "on", Name: "SIGNAL", Address: by, TargetAddress: target,
			Event: &interp.Event{Values: map[string]string{}},
			Match: func(f *interp.Frame, e *script.Event) (bool, error) { return true, nil }})
	}
	fire("boss!u@h", "helper!u@h")
	fire("helper!u@h", "boss!u@h")
	if got := strings.Join(out.lines, "|"); got != "legal|illegal" {
		t.Fatalf("got %s", got)
	}
}
