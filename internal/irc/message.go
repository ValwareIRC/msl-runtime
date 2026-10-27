// Package irc connects the runtime to IRC servers: connections, protocol
// state (channels, the internal address list), the events scripts see, and
// the IRC commands and identifiers. It has no user interface.
package irc

import (
	"strings"
)

// Message is one parsed IRC protocol line.
type Message struct {
	Tags    map[string]string
	Source  string
	Command string
	Params  []string
	Raw     string
}

// ParseMessage parses an IRC line, including IRCv3 message tags.
func ParseMessage(line string) Message {
	m := Message{Raw: line}
	line = strings.TrimRight(line, "\r\n")
	if strings.HasPrefix(line, "@") {
		var tags string
		tags, line, _ = strings.Cut(line[1:], " ")
		m.Tags = parseTags(tags)
		line = strings.TrimLeft(line, " ")
	}
	if strings.HasPrefix(line, ":") {
		m.Source, line, _ = strings.Cut(line[1:], " ")
		line = strings.TrimLeft(line, " ")
	}
	m.Command, line, _ = strings.Cut(line, " ")
	m.Command = strings.ToUpper(m.Command)
	for line != "" {
		line = strings.TrimLeft(line, " ")
		if line == "" {
			break
		}
		if line[0] == ':' {
			m.Params = append(m.Params, line[1:])
			break
		}
		var p string
		p, line, _ = strings.Cut(line, " ")
		m.Params = append(m.Params, p)
	}
	return m
}

func parseTags(s string) map[string]string {
	tags := map[string]string{}
	for _, kv := range strings.Split(s, ";") {
		k, v, _ := strings.Cut(kv, "=")
		if k != "" {
			tags[k] = unescapeTag(v)
		}
	}
	return tags
}

func unescapeTag(v string) string {
	if !strings.Contains(v, `\`) {
		return v
	}
	var b strings.Builder
	for i := 0; i < len(v); i++ {
		if v[i] != '\\' || i+1 == len(v) {
			if v[i] != '\\' {
				b.WriteByte(v[i])
			}
			continue
		}
		i++
		switch v[i] {
		case ':':
			b.WriteByte(';')
		case 's':
			b.WriteByte(' ')
		case 'r':
			b.WriteByte('\r')
		case 'n':
			b.WriteByte('\n')
		default:
			b.WriteByte(v[i])
		}
	}
	return b.String()
}

// Param returns parameter i or "".
func (m Message) Param(i int) string {
	if i < 0 || i >= len(m.Params) {
		return ""
	}
	return m.Params[i]
}

// Last returns the final parameter or "".
func (m Message) Last() string { return m.Param(len(m.Params) - 1) }

// Nick returns the nickname part of the source.
func (m Message) Nick() string { return NickOf(m.Source) }

// NickOf returns the nick of a nick!user@host address.
func NickOf(addr string) string {
	n, _, _ := strings.Cut(addr, "!")
	n, _, _ = strings.Cut(n, "@")
	return n
}

// SplitAddress splits nick!user@host.
func SplitAddress(addr string) (nick, user, host string) {
	nick, rest, ok := strings.Cut(addr, "!")
	if !ok {
		nick, host, _ = strings.Cut(addr, "@")
		return nick, "", host
	}
	user, host, _ = strings.Cut(rest, "@")
	return nick, user, host
}

// CTCP returns the command and argument of a CTCP payload (\x01...\x01).
func CTCP(text string) (cmd, arg string, ok bool) {
	if len(text) < 2 || text[0] != '\x01' {
		return "", "", false
	}
	body := strings.TrimSuffix(text[1:], "\x01")
	cmd, arg, _ = strings.Cut(body, " ")
	return strings.ToUpper(cmd), arg, true
}

// EncodeCTCP wraps a CTCP command and argument.
func EncodeCTCP(cmd, arg string) string {
	if arg == "" {
		return "\x01" + cmd + "\x01"
	}
	return "\x01" + cmd + " " + arg + "\x01"
}
