package sockets

import (
	"net"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"github.com/ValwareIRC/msl-runtime/internal/interp"
)

func (s *Sockets) installIdents() {
	rt := s.rt
	rt.RegisterIdent("sock", s.identSock)
	rt.RegisterIdent("sockname", func(c *interp.Call) (string, error) { v, _ := c.F.Event.Get("sockname"); return v, nil })
	rt.RegisterIdent("sockerr", func(*interp.Call) (string, error) { return strconv.Itoa(s.sockerr), nil })
	rt.RegisterIdent("sockbr", func(*interp.Call) (string, error) { return strconv.Itoa(s.sockbr), nil })
	rt.RegisterIdent("bindip", identBindip)
	rt.RegisterIdent("portfree", identPortfree)
	rt.RegisterIdent("iptype", identIptype)
}

// identSock implements $sock(name|wildcard, N).prop.
func (s *Sockets) identSock(c *interp.Call) (string, error) {
	list := s.Match(c.Arg(0))
	n := 1
	if len(c.Args) > 1 {
		n = interp.Int(c.Arg(1))
	}
	if n == 0 {
		return strconv.Itoa(len(list)), nil
	}
	if n < 1 || n > len(list) {
		return "", nil
	}
	k := list[n-1]
	secs := func(t time.Time) string {
		if t.IsZero() {
			return "0"
		}
		return strconv.Itoa(int(time.Since(t).Seconds()))
	}
	switch strings.ToLower(c.Prop) {
	case "", "name":
		return k.Name, nil
	case "ip":
		return k.IP, nil
	case "addr":
		if k.Addr != "" {
			return k.Addr, nil
		}
		return k.IP, nil
	case "port":
		return strconv.Itoa(k.Port), nil
	case "status":
		return k.Status, nil
	case "sent":
		return strconv.FormatInt(k.Sent, 10), nil
	case "rcvd":
		return strconv.FormatInt(k.Rcvd, 10), nil
	case "sq":
		return strconv.Itoa(k.sq), nil
	case "rq":
		return strconv.Itoa(len(k.rq)), nil
	case "ls":
		return secs(k.lastSent), nil
	case "lr":
		return secs(k.lastRecv), nil
	case "mark":
		return k.Mark, nil
	case "type":
		return k.Kind, nil
	case "saddr":
		return k.SAddr, nil
	case "sport":
		return strconv.Itoa(k.SPort), nil
	case "to":
		return secs(k.opened), nil
	case "wserr":
		return strconv.Itoa(k.WSErr), nil
	case "wsmsg":
		return k.WSMsg, nil
	case "bindip":
		return k.BindIP, nil
	case "bindport":
		return strconv.Itoa(k.BindPort), nil
	case "ssl":
		return interp.Bool(k.SSL), nil
	case "pause":
		return interp.Bool(k.Paused), nil
	case "starttls", "upnp":
		return interp.Bool(false), nil
	}
	return "", nil
}

type adapter struct {
	name     string
	ip       string
	loopback bool
}

func adapters() []adapter {
	ifs, _ := net.Interfaces()
	var out []adapter
	for _, i := range ifs {
		if i.Flags&net.FlagUp == 0 {
			continue
		}
		addrs, _ := i.Addrs()
		for _, a := range addrs {
			if ipn, ok := a.(*net.IPNet); ok {
				out = append(out, adapter{i.Name, ipn.IP.String(), i.Flags&net.FlagLoopback != 0})
			}
		}
	}
	return out
}

// identBindip implements $bindip(N|ipaddress).name|ip|loopback.
func identBindip(c *interp.Call) (string, error) {
	list := adapters()
	var a *adapter
	if n, ok := interp.ParseNumber(c.Arg(0)); ok && !strings.Contains(c.Arg(0), ".") {
		i := int(n)
		if i == 0 {
			return strconv.Itoa(len(list)), nil
		}
		if i < 1 || i > len(list) {
			return "", nil
		}
		a = &list[i-1]
	} else {
		// The best adapter for reaching an address is the one the OS would use.
		if conn, err := net.Dial("udp", net.JoinHostPort(c.Arg(0), "9")); err == nil {
			local := conn.LocalAddr().(*net.UDPAddr).IP.String()
			conn.Close()
			for i := range list {
				if list[i].ip == local {
					a = &list[i]
				}
			}
		}
	}
	if a == nil {
		return "", nil
	}
	switch strings.ToLower(c.Prop) {
	case "name":
		return a.name, nil
	case "loopback":
		return interp.Bool(a.loopback), nil
	}
	return a.ip, nil
}

// identPortfree implements $portfree(N,[ipaddress]).
func identPortfree(c *interp.Call) (string, error) {
	ln, err := net.Listen("tcp", net.JoinHostPort(c.Arg(1), strconv.Itoa(interp.Int(c.Arg(0)))))
	if err != nil {
		return interp.Bool(false), nil
	}
	ln.Close()
	return interp.Bool(true), nil
}

// identIptype implements $iptype(text).compress|expand.
func identIptype(c *interp.Call) (string, error) {
	a, err := netip.ParseAddr(strings.TrimSpace(c.Arg(0)))
	if err != nil {
		return "", nil
	}
	switch strings.ToLower(c.Prop) {
	case "compress":
		return a.String(), nil
	case "expand":
		return a.StringExpanded(), nil
	}
	if a.Is4() {
		return "ipv4", nil
	}
	return "ipv6", nil
}
