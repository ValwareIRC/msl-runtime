package main

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/ValwareIRC/msl-runtime/internal/control"
	"github.com/ValwareIRC/msl-runtime/internal/engine"
	"github.com/ValwareIRC/msl-runtime/internal/script"
)

// apiState is the JSON snapshot served at /state.
type apiState struct {
	Connections []apiConn           `json:"connections"`
	Active      int                 `json:"active"`
	Scripts     []apiScript         `json:"scripts"`
	Variables   []apiVar            `json:"variables"`
	Timers      []control.TimerInfo `json:"timers"`
}

type apiConn struct {
	Cid      int          `json:"cid"`
	Status   string       `json:"status"`
	Server   string       `json:"server"`
	Network  string       `json:"network"`
	Nick     string       `json:"nick"`
	TLS      bool         `json:"tls"`
	LagMs    int64        `json:"lagMs"`
	Channels []apiChannel `json:"channels"`
}

type apiChannel struct {
	Name  string `json:"name"`
	Topic string `json:"topic"`
	Users int    `json:"users"`
	Modes string `json:"modes"`
}

type apiScript struct {
	Path    string `json:"path"`
	Kind    string `json:"kind"`
	Aliases int    `json:"aliases"`
	Events  int    `json:"events"`
}

type apiVar struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// apiLine is one streamed output line.
type apiLine struct {
	Time   int64  `json:"time"`
	Window string `json:"window"`
	Text   string `json:"text"`
	Error  bool   `json:"error,omitempty"`
}

// hub fans output lines out to /events subscribers and keeps recent history.
type hub struct {
	mu      sync.Mutex
	subs    map[chan apiLine]bool
	history []apiLine
}

const historySize = 500

func (h *hub) publish(l apiLine) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.history = append(h.history, l)
	if len(h.history) > historySize {
		h.history = h.history[len(h.history)-historySize:]
	}
	for ch := range h.subs {
		select {
		case ch <- l:
		default:
		}
	}
}

func (h *hub) subscribe() (chan apiLine, []apiLine) {
	h.mu.Lock()
	defer h.mu.Unlock()
	ch := make(chan apiLine, 256)
	if h.subs == nil {
		h.subs = map[chan apiLine]bool{}
	}
	h.subs[ch] = true
	return ch, append([]apiLine(nil), h.history...)
}

func (h *hub) unsubscribe(ch chan apiLine) {
	h.mu.Lock()
	delete(h.subs, ch)
	h.mu.Unlock()
}

// onRuntime runs fn on the runtime goroutine and waits for it.
func onRuntime[T any](eng *engine.Engine, fn func() T) T {
	done := make(chan T, 1)
	eng.RT.Post(func() { done <- fn() })
	return <-done
}

func snapshot(eng *engine.Engine) apiState {
	st := apiState{Active: eng.IRC.Active().ID, Timers: eng.Control.Timers()}
	for _, c := range eng.IRC.Conns() {
		ac := apiConn{Cid: c.ID, Status: c.Status.String(), Server: c.ServerID, Network: c.Network(),
			Nick: c.Me, TLS: c.Spec.TLS, LagMs: c.Lag.Milliseconds()}
		if ac.Server == "" {
			ac.Server = c.Spec.Host
		}
		for _, ch := range c.Channels {
			ac.Channels = append(ac.Channels, apiChannel{Name: ch.Name, Topic: ch.Topic, Users: len(ch.Members), Modes: ch.ModeString()})
		}
		sort.Slice(ac.Channels, func(i, j int) bool { return ac.Channels[i].Name < ac.Channels[j].Name })
		st.Connections = append(st.Connections, ac)
	}
	for _, f := range eng.RT.Files() {
		kind := "remote"
		if f.Kind == script.Aliases {
			kind = "aliases"
		}
		st.Scripts = append(st.Scripts, apiScript{Path: f.Path, Kind: kind, Aliases: len(f.Aliases), Events: len(f.Events)})
	}
	for _, v := range eng.RT.Globals.All() {
		st.Variables = append(st.Variables, apiVar{Name: v.Name, Value: v.Value})
	}
	return st
}

// serveAPI starts the local control API and returns its address.
func serveAPI(eng *engine.Engine, addr string, h *hub) (string, error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return "", err
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /state", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, onRuntime(eng, func() apiState { return snapshot(eng) }))
	})
	mux.HandleFunc("POST /exec", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Line string `json:"line"`
			Cid  int    `json:"cid"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Line == "" {
			http.Error(w, "expected {\"line\": \"...\"}", http.StatusBadRequest)
			return
		}
		errText := onRuntime(eng, func() string {
			if req.Cid > 0 {
				eng.IRC.SetActive(req.Cid)
			}
			if err := eng.RT.Exec(req.Line); err != nil {
				return err.Error()
			}
			return ""
		})
		writeJSON(w, map[string]string{"error": errText})
	})
	mux.HandleFunc("GET /events", func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming unsupported", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		ch, history := h.subscribe()
		defer h.unsubscribe(ch)
		send := func(l apiLine) {
			b, _ := json.Marshal(l)
			fmt.Fprintf(w, "data: %s\n\n", b)
		}
		for _, l := range history {
			send(l)
		}
		flusher.Flush()
		ping := time.NewTicker(15 * time.Second)
		defer ping.Stop()
		for {
			select {
			case l := <-ch:
				send(l)
				flusher.Flush()
			case <-ping.C:
				fmt.Fprint(w, ": ping\n\n")
				flusher.Flush()
			case <-r.Context().Done():
				return
			}
		}
	})
	go http.Serve(ln, cors(mux))
	return "http://" + ln.Addr().String(), nil
}

func cors(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			return
		}
		h.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}
