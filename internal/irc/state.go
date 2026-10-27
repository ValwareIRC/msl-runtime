package irc

import (
	"sort"
	"strconv"
	"strings"
	"time"
)

// Support holds the server's ISUPPORT (005) settings that scripts and
// state tracking depend on.
type Support struct {
	Tokens      map[string]string
	ChanTypes   string
	PrefixModes string // e.g. "ohv"
	Prefixes    string // e.g. "@%+"
	ChanModes   [4]string
	ModesPerMsg int
	CaseMapping string
	Network     string
}

func defaultSupport() Support {
	return Support{
		Tokens:      map[string]string{},
		ChanTypes:   "#&",
		PrefixModes: "ov",
		Prefixes:    "@+",
		ChanModes:   [4]string{"beI", "k", "l", "imnpst"},
		ModesPerMsg: 3,
		CaseMapping: "rfc1459",
	}
}

// apply reads one ISUPPORT token.
func (s *Support) apply(tok string) {
	k, v, _ := strings.Cut(tok, "=")
	k = strings.ToUpper(k)
	if strings.HasPrefix(k, "-") {
		delete(s.Tokens, k[1:])
		return
	}
	s.Tokens[k] = v
	switch k {
	case "CHANTYPES":
		s.ChanTypes = v
	case "PREFIX":
		if modes, prefixes, ok := strings.Cut(strings.TrimPrefix(v, "("), ")"); ok {
			s.PrefixModes, s.Prefixes = modes, prefixes
		}
	case "CHANMODES":
		parts := strings.SplitN(v, ",", 4)
		for i := range parts {
			s.ChanModes[i] = parts[i]
		}
	case "MODES":
		if n, err := strconv.Atoi(v); err == nil {
			s.ModesPerMsg = n
		}
	case "CASEMAPPING":
		s.CaseMapping = strings.ToLower(v)
	case "NETWORK":
		s.Network = v
	}
}

// Fold lowers a name using the server's case mapping.
func (s *Support) Fold(name string) string {
	b := []byte(strings.ToLower(name))
	if s.CaseMapping == "ascii" {
		return string(b)
	}
	for i, c := range b {
		switch c {
		case '[':
			b[i] = '{'
		case ']':
			b[i] = '}'
		case '\\':
			b[i] = '|'
		case '~':
			if s.CaseMapping != "strict-rfc1459" {
				b[i] = '^'
			}
		}
	}
	return string(b)
}

// IsChannel reports whether name starts with a channel type character.
func (s *Support) IsChannel(name string) bool {
	return name != "" && strings.IndexByte(s.ChanTypes, name[0]) >= 0
}

// modeArg reports whether a channel mode takes a parameter when set (+)
// or unset (-).
func (s *Support) modeArg(mode byte, adding bool) bool {
	switch {
	case strings.IndexByte(s.PrefixModes, mode) >= 0,
		strings.IndexByte(s.ChanModes[0], mode) >= 0,
		strings.IndexByte(s.ChanModes[1], mode) >= 0:
		return true
	case strings.IndexByte(s.ChanModes[2], mode) >= 0:
		return adding
	}
	return false
}

// Member is a nick on a channel with its status modes ("ov").
type Member struct {
	Nick     string
	Modes    string
	LastSeen time.Time
}

// ListEntry is an entry in a channel's ban, except, invite or quiet list.
type ListEntry struct {
	Mask string
	By   string
	At   time.Time
}

// Channel is the tracked state of a joined channel.
type Channel struct {
	Name      string
	Topic     string
	TopicBy   string
	TopicAt   time.Time
	Modes     string // simple modes, e.g. "nt"
	Key       string
	Limit     string
	Created   time.Time
	Members   map[string]*Member
	order     []string
	Lists     map[byte][]ListEntry // by list mode: b, e, I, q
	JoinedAt  time.Time
	Synced    bool
	modeExtra map[byte]string
}

func newChannel(name string) *Channel {
	return &Channel{
		Name: name, Members: map[string]*Member{}, Lists: map[byte][]ListEntry{},
		JoinedAt: time.Now(), modeExtra: map[byte]string{},
	}
}

// Nicks returns members in join order.
func (c *Channel) Nicks() []*Member {
	out := make([]*Member, 0, len(c.order))
	for _, k := range c.order {
		if m, ok := c.Members[k]; ok {
			out = append(out, m)
		}
	}
	return out
}

func (c *Channel) add(key, nick, modes string) *Member {
	if m, ok := c.Members[key]; ok {
		m.Nick = nick
		if modes != "" {
			m.Modes = modes
		}
		return m
	}
	m := &Member{Nick: nick, Modes: modes, LastSeen: time.Now()}
	c.Members[key] = m
	c.order = append(c.order, key)
	return m
}

func (c *Channel) remove(key string) {
	delete(c.Members, key)
	for i, k := range c.order {
		if k == key {
			c.order = append(c.order[:i], c.order[i+1:]...)
			return
		}
	}
}

func (c *Channel) rename(oldKey, newKey, nick string) {
	m, ok := c.Members[oldKey]
	if !ok {
		return
	}
	delete(c.Members, oldKey)
	m.Nick = nick
	c.Members[newKey] = m
	for i, k := range c.order {
		if k == oldKey {
			c.order[i] = newKey
		}
	}
}

// ModeString renders the channel modes with key and limit, as $chan().mode does.
func (c *Channel) ModeString() string {
	if c.Modes == "" && c.Key == "" && c.Limit == "" {
		return ""
	}
	letters := []byte(c.Modes)
	sort.Slice(letters, func(i, j int) bool { return letters[i] < letters[j] })
	s := "+" + string(letters)
	var args []string
	if c.Key != "" {
		s += "k"
		args = append(args, c.Key)
	}
	if c.Limit != "" {
		s += "l"
		args = append(args, c.Limit)
	}
	if len(args) > 0 {
		s += " " + strings.Join(args, " ")
	}
	return s
}

// IALEntry is one user in the internal address list.
type IALEntry struct {
	Nick    string
	User    string
	Host    string
	Account string
	Away    bool
	Mark    string
}

// Address returns nick!user@host.
func (e *IALEntry) Address() string {
	if e.User == "" && e.Host == "" {
		return e.Nick
	}
	return e.Nick + "!" + e.User + "@" + e.Host
}
