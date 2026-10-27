// Package engine assembles a runtime with every feature package installed
// and drives its main loop.
package engine

import (
	"context"
	"time"

	"github.com/ValwareIRC/msl-runtime/internal/builtins"
	"github.com/ValwareIRC/msl-runtime/internal/control"
	"github.com/ValwareIRC/msl-runtime/internal/data"
	"github.com/ValwareIRC/msl-runtime/internal/dcc"
	"github.com/ValwareIRC/msl-runtime/internal/interp"
	"github.com/ValwareIRC/msl-runtime/internal/irc"
	"github.com/ValwareIRC/msl-runtime/internal/sockets"
	"github.com/ValwareIRC/msl-runtime/internal/storage"
	"github.com/ValwareIRC/msl-runtime/internal/ui"
)

// Engine is a runtime with its feature packages.
type Engine struct {
	RT      *interp.Runtime
	Data    *data.Data
	IRC     *irc.Manager
	Control *control.Control
	UI      *ui.UI

	stop context.CancelFunc
}

// New returns an engine with all built-ins registered. dataDir is $mircdir,
// the base of relative file paths ("" for the working directory).
func New(out interp.Output, dataDir string) *Engine {
	rt := interp.New(out)
	builtins.Install(rt)
	st := storage.Install(rt, dataDir)
	e := &Engine{RT: rt, Data: data.Install(rt), IRC: irc.Install(rt)}
	e.IRC.ResolvePath = st.Path
	e.Control = control.Install(rt, e.IRC)
	sockets.Install(rt)
	d := dcc.Install(rt, e.IRC)
	e.UI = ui.Install(rt)
	d.CloseWindows = e.UI.CloseWindows
	rt.Exit = func(bool) {
		if e.stop != nil {
			e.stop()
		}
	}
	rt.DeclareEvents("START", "LOAD", "UNLOAD", "EXIT")
	return e
}

// Start loads saved variables and users, then fires on START for every
// loaded script, as mIRC does at startup.
func (e *Engine) Start() {
	e.Control.LoadState()
	e.Control.Starting(true)
	e.RT.Dispatch(interp.Fire{Kind: "on", Name: "START"})
	e.Control.Starting(false)
}

// Run processes posted work and once-per-second ticks until ctx ends, then
// fires on EXIT and quits every connection.
func (e *Engine) Run(ctx context.Context) {
	ctx, e.stop = context.WithCancel(ctx)
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case fn := <-e.RT.Posted():
			fn()
		case now := <-tick.C:
			e.RT.Tick(now)
			e.Data.Tick()
		case <-ctx.Done():
			e.Shutdown()
			return
		}
	}
}

// Shutdown fires on EXIT and sends QUIT on every connection.
func (e *Engine) Shutdown() {
	e.RT.Dispatch(interp.Fire{Kind: "on", Name: "EXIT"})
	_ = e.Control.SaveState()
	for _, c := range e.IRC.Conns() {
		c.Quit("")
	}
	// Give writers a moment to flush QUIT lines.
	deadline := time.After(500 * time.Millisecond)
	for {
		select {
		case fn := <-e.RT.Posted():
			fn()
		case <-deadline:
			return
		}
	}
}
