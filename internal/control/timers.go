package control

import (
	"strconv"
	"strings"
	"time"

	"github.com/ValwareIRC/msl-runtime/internal/interp"
	"github.com/ValwareIRC/msl-runtime/internal/irc"
	"github.com/ValwareIRC/msl-runtime/internal/script"
)

// timer is one /timer.
type timer struct {
	name    string
	command string
	reps    int // 0 repeats forever
	done    int
	delay   time.Duration
	millis  bool
	offline bool
	anyConn bool
	catchUp bool
	paused  bool
	cid     int
	file    *script.File
	next    time.Time
	t       *time.Timer
	gen     int
}

func (c *Control) installTimers() {
	c.rt.RegisterCommand("timer", interp.Command{Fn: c.cmdTimer})
	c.rt.RegisterPrefixCommand("timer", interp.Command{Fn: c.cmdTimer})
	c.rt.RegisterIdent("timer", c.identTimer)
	c.rt.RegisterIdent("ltimer", func(*interp.Call) (string, error) { return c.ltimer, nil })
	c.rt.RegisterIdent("ctimer", func(cl *interp.Call) (string, error) { v, _ := cl.F.Event.Get("ctimer"); return v, nil })
}

func (c *Control) timerByName(name string) *timer {
	for _, t := range c.timers {
		if strings.EqualFold(t.name, name) {
			return t
		}
	}
	return nil
}

func (c *Control) freeName() string {
	for n := 1; ; n++ {
		if c.timerByName(strconv.Itoa(n)) == nil {
			return strconv.Itoa(n)
		}
	}
}

func (c *Control) removeTimer(t *timer) {
	if t.t != nil {
		t.t.Stop()
	}
	t.gen++
	for i, x := range c.timers {
		if x == t {
			c.timers = append(c.timers[:i], c.timers[i+1:]...)
			return
		}
	}
}

// cmdTimer implements /timer[N|name] [-cdeomhipPrz] [time] <reps> <interval> <command>,
// /timerN off, /timers off and /timer[name] -e|-p|-r.
func (c *Control) cmdTimer(cc *interp.CmdCall) error {
	name := strings.TrimPrefix(cc.Name, "timer")
	all := name == "s"
	if all {
		name = ""
	}
	sw, rest := cc.Switches()
	rest = strings.TrimSpace(rest)

	if strings.EqualFold(rest, "off") || (rest == "" && strings.ContainsAny(sw, "eprP")) {
		pattern := name
		if pattern == "" {
			pattern = "*"
		}
		for _, t := range append([]*timer(nil), c.timers...) {
			if !interp.WildMatch(pattern, t.name, false) {
				continue
			}
			switch {
			case strings.EqualFold(rest, "off"):
				c.removeTimer(t)
			case strings.Contains(sw, "e"):
				c.fireTimer(t, t.gen)
			case strings.Contains(sw, "p"):
				t.paused = true
			case strings.ContainsAny(sw, "rP"):
				t.paused = false
				c.schedule(t)
			}
		}
		return nil
	}
	if rest == "" {
		for _, t := range c.timers {
			if name == "" || interp.WildMatch(name, t.name, false) {
				c.rt.Out.Echo(interp.StatusWindow, "* Timer "+t.name+" "+strconv.Itoa(t.reps)+" "+t.delayText()+" "+t.command)
			}
		}
		return nil
	}

	f := strings.Fields(rest)
	var start time.Time
	if len(f) > 0 && strings.Contains(f[0], ":") {
		if at, err := time.ParseInLocation("15:04", f[0], time.Local); err == nil {
			now := time.Now()
			start = time.Date(now.Year(), now.Month(), now.Day(), at.Hour(), at.Minute(), 0, 0, time.Local)
			if start.Before(now) {
				start = start.Add(24 * time.Hour)
			}
		}
		rest = strings.TrimSpace(strings.TrimPrefix(rest, f[0]))
		f = f[1:]
	}
	if len(f) < 3 {
		return cc.InvalidParams()
	}
	reps := interp.Int(f[0])
	interval := interp.Num(f[1])
	_, after, _ := strings.Cut(rest, " ")
	_, command, _ := strings.Cut(strings.TrimLeft(after, " "), " ")

	if name == "" {
		name = c.freeName()
	} else if old := c.timerByName(name); old != nil {
		c.removeTimer(old)
	}
	millis := strings.ContainsAny(sw, "mh")
	unit := time.Second
	if millis {
		unit = time.Millisecond
	}
	t := &timer{
		name: name, command: strings.TrimSpace(command), reps: reps,
		delay: time.Duration(interval * float64(unit)), millis: millis,
		anyConn: strings.Contains(sw, "i"), catchUp: strings.Contains(sw, "c"),
		cid: cc.F.Cid, file: cc.F.File,
	}
	t.offline = strings.Contains(sw, "o") || c.irc == nil || c.irc.FrameConn(cc.F).Status != irc.Connected
	if t.cid == 0 && c.irc != nil {
		t.cid = c.irc.FrameConn(cc.F).ID
	}
	c.timers = append(c.timers, t)
	c.ltimer = name
	if !start.IsZero() {
		t.next = start
	} else {
		t.next = time.Now().Add(t.delay)
	}
	c.schedule(t)
	return nil
}

func (t *timer) delayText() string {
	if t.millis {
		return strconv.FormatInt(t.delay.Milliseconds(), 10)
	}
	return interp.FormatNumber(t.delay.Seconds())
}

// schedule arms a timer for its next run on the runtime goroutine.
func (c *Control) schedule(t *timer) {
	if t.t != nil {
		t.t.Stop()
	}
	if t.paused {
		return
	}
	gen := t.gen
	wait := time.Until(t.next)
	if wait < 0 {
		wait = 0
	}
	t.t = time.AfterFunc(wait, func() { c.rt.Post(func() { c.fireTimer(t, gen) }) })
}

// fireTimer runs a timer's command. The command was evaluated when the
// timer was made and is evaluated again now, as in mIRC.
func (c *Control) fireTimer(t *timer, gen int) {
	if gen != t.gen || t.paused {
		return
	}
	t.done++
	last := t.reps > 0 && t.done >= t.reps
	if last {
		c.removeTimer(t)
	} else {
		if t.catchUp {
			t.next = t.next.Add(t.delay)
		} else {
			t.next = time.Now().Add(t.delay)
		}
		c.schedule(t)
	}
	ev := &interp.Event{Name: "TIMER", Values: map[string]string{"ctimer": t.name}}
	f := c.rt.NewFrame(t.file, ev)
	f.Cid = t.cid
	f.Caller = "timer"
	_ = c.rt.Run(func() error {
		err := f.ExecLine(t.command, 0)
		if interp.IsHalt(err) {
			return nil
		}
		return err
	})
}

// TimerInfo describes a running timer for status displays.
type TimerInfo struct {
	Name    string `json:"name"`
	Command string `json:"command"`
	Reps    int    `json:"reps"`
	Done    int    `json:"done"`
	Delay   string `json:"delay"`
	Millis  bool   `json:"millis"`
	Next    int64  `json:"next"` // unix milliseconds
	Paused  bool   `json:"paused"`
}

// Timers lists running timers.
func (c *Control) Timers() []TimerInfo {
	out := make([]TimerInfo, 0, len(c.timers))
	for _, t := range c.timers {
		out = append(out, TimerInfo{Name: t.name, Command: t.command, Reps: t.reps, Done: t.done,
			Delay: t.delayText(), Millis: t.millis, Next: t.next.UnixMilli(), Paused: t.paused})
	}
	return out
}

// stopOnline removes the online timers of a closed connection.
func (c *Control) stopOnline(conn *irc.Conn) {
	for _, t := range append([]*timer(nil), c.timers...) {
		if !t.offline && !t.anyConn && t.cid == conn.ID {
			c.removeTimer(t)
		}
	}
}

func (c *Control) identTimer(cl *interp.Call) (string, error) {
	arg := cl.Arg(0)
	var t *timer
	if n, ok := interp.ParseNumber(arg); ok && !strings.EqualFold(cl.Prop, "name") {
		i := int(n)
		if i == 0 {
			return strconv.Itoa(len(c.timers)), nil
		}
		if i < 1 || i > len(c.timers) {
			return "", nil
		}
		t = c.timers[i-1]
	} else {
		t = c.timerByName(arg)
		if strings.EqualFold(cl.Prop, "name") {
			for i, x := range c.timers {
				if x == t {
					return strconv.Itoa(i + 1), nil
				}
			}
			return "", nil
		}
	}
	if t == nil {
		return "", nil
	}
	switch strings.ToLower(cl.Prop) {
	case "com":
		return t.command, nil
	case "time":
		return t.next.Format("15:04"), nil
	case "reps":
		if t.reps == 0 {
			return "0", nil
		}
		return strconv.Itoa(t.reps - t.done), nil
	case "delay":
		return t.delayText(), nil
	case "type":
		if t.offline {
			return "offline", nil
		}
		return "online", nil
	case "secs":
		return strconv.Itoa(int(time.Until(t.next).Seconds() + 0.5)), nil
	case "mmt":
		return interp.Bool(t.millis), nil
	case "anysc":
		return interp.Bool(t.anyConn), nil
	case "cid":
		return strconv.Itoa(t.cid), nil
	case "pause":
		return interp.Bool(t.paused), nil
	case "wid", "hwnd":
		return "0", nil
	}
	return t.name, nil
}
