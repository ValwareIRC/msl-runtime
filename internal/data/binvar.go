// Package data provides binary variables, hash tables, regular expression
// identifiers, and hashing, encoding and compression identifiers.
package data

import (
	"bytes"
	"encoding/binary"
	"os"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/ValwareIRC/msl-runtime/internal/interp"
	"github.com/ValwareIRC/msl-runtime/internal/regex"
	"github.com/ValwareIRC/msl-runtime/internal/storage"
)

// Data is the per-runtime state of this package.
type Data struct {
	rt      *interp.Runtime
	st      *storage.Storage
	binvars map[string][]byte
	tables  map[string]*table
	order   []string // table names in creation order
	regml   map[string][]regmlItem
	regmlQ  []string // names in least-recently-set order, at most 50
	regErr  string
}

type dataKey struct{}

// Install registers the package's commands and identifiers.
func Install(rt *interp.Runtime) *Data {
	d := &Data{rt: rt, st: storage.From(rt), binvars: map[string][]byte{}, tables: map[string]*table{}, regml: map[string][]regmlItem{}}
	interp.Ext(rt, dataKey{}, func() *Data { return d })
	// Binary variables only live until the script that made them finishes.
	rt.AfterRun(func() { d.binvars = map[string][]byte{} })
	if d.st != nil {
		d.st.Binvars = func(_ *interp.Frame, name string) []byte { return d.binvars[strings.ToLower(name)] }
		d.st.SetBinvar = func(_ *interp.Frame, name string, b []byte) { d.setBin(name, b) }
	}
	d.installBinvars()
	d.installTables()
	d.installRegex()
	d.installCrypto()
	return d
}

func (d *Data) path(name string) string {
	if d.st != nil {
		return d.st.Path(name)
	}
	return name
}

// Bin returns a binary variable's bytes and whether it exists.
func (d *Data) Bin(name string) ([]byte, bool) {
	b, ok := d.binvars[strings.ToLower(name)]
	return b, ok
}

func (d *Data) setBin(name string, b []byte) { d.binvars[strings.ToLower(name)] = b }

// SetBin stores bytes in a binary variable.
func (d *Data) SetBin(name string, b []byte) { d.setBin(name, b) }

// From returns the data package state installed on rt, or nil.
func From(rt *interp.Runtime) *Data {
	return interp.Ext(rt, dataKey{}, func() *Data { return nil })
}

// textBytes encodes text as mIRC does for binary variables: UTF-8, unless
// latin1 is set and every character fits in one byte.
func textBytes(s string, latin1 bool) []byte {
	if latin1 {
		out := make([]byte, 0, len(s))
		for _, r := range s {
			if r > 0xff {
				return []byte(s)
			}
			out = append(out, byte(r))
		}
		return out
	}
	return []byte(s)
}

// bytesText decodes bytes as UTF-8, falling back to one character per byte.
func bytesText(b []byte) string {
	if utf8.Valid(b) {
		return string(b)
	}
	var sb strings.Builder
	for _, c := range b {
		sb.WriteRune(rune(c))
	}
	return sb.String()
}

func (d *Data) installBinvars() {
	rt := d.rt
	rt.RegisterCommand("bset", interp.Command{Fn: d.cmdBset})
	rt.RegisterCommand("bunset", interp.Command{Fn: func(c *interp.CmdCall) error {
		for _, n := range c.Fields() {
			delete(d.binvars, strings.ToLower(n))
		}
		return nil
	}})
	rt.RegisterCommand("bcopy", interp.Command{Fn: d.cmdBcopy})
	rt.RegisterCommand("breplace", interp.Command{Fn: d.cmdBreplace})
	rt.RegisterCommand("bread", interp.Command{Fn: d.cmdBread})
	rt.RegisterCommand("bwrite", interp.Command{Fn: d.cmdBwrite, Raw: true})
	rt.RegisterCommand("btrunc", interp.Command{Fn: func(c *interp.CmdCall) error {
		f := c.Fields()
		if len(f) < 2 {
			return c.InvalidParams()
		}
		if err := os.Truncate(d.path(f[0]), int64(interp.Int(f[1]))); err != nil {
			return c.Errorf("%s", err.Error())
		}
		return nil
	}})
	rt.RegisterIdent("bvar", d.identBvar)
	rt.RegisterIdent("bfind", d.identBfind)
}

// cmdBset implements /bset [-tacz] <&binvar> <N> <values...>.
func (d *Data) cmdBset(c *interp.CmdCall) error {
	sw, rest := c.Switches()
	name, rest, _ := strings.Cut(rest, " ")
	if !strings.HasPrefix(name, "&") {
		return c.InvalidParams()
	}
	b, _ := d.Bin(name)
	if strings.Contains(sw, "z") {
		d.setBin(name, make([]byte, len(b)))
		if strings.TrimSpace(rest) == "" {
			return nil
		}
		b = d.binvars[strings.ToLower(name)]
	}
	posText, values, _ := strings.Cut(strings.TrimLeft(rest, " "), " ")
	pos := interp.Int(posText)
	var data []byte
	if strings.Contains(sw, "t") {
		data = textBytes(values, strings.Contains(sw, "a"))
	} else {
		for _, v := range strings.Fields(values) {
			data = append(data, byte(interp.Int(v)))
		}
	}
	start := len(b)
	if pos != -1 {
		start = max(pos-1, 0)
	}
	if need := start + len(data); need > len(b) {
		b = append(b, make([]byte, need-len(b))...)
	}
	copy(b[start:], data)
	if strings.Contains(sw, "c") {
		b = b[:start+len(data)]
	}
	d.setBin(name, b)
	return nil
}

// cmdBcopy implements /bcopy [-zc] <&dst> <N> <&src> <S> <M>.
func (d *Data) cmdBcopy(c *interp.CmdCall) error {
	sw, rest := c.Switches()
	f := strings.Fields(rest)
	if len(f) < 5 {
		return c.InvalidParams()
	}
	src, _ := d.Bin(f[2])
	s, m := max(interp.Int(f[3])-1, 0), interp.Int(f[4])
	if s > len(src) {
		s = len(src)
	}
	end := len(src)
	if m >= 0 && s+m < end {
		end = s + m
	}
	chunk := append([]byte(nil), src[s:end]...)
	if strings.Contains(sw, "z") {
		for i := s; i < end; i++ {
			src[i] = 0
		}
	}
	dst, _ := d.Bin(f[0])
	n := interp.Int(f[1])
	start := len(dst)
	if n != -1 {
		start = max(n-1, 0)
	}
	if need := start + len(chunk); need > len(dst) {
		dst = append(dst, make([]byte, need-len(dst))...)
	}
	copy(dst[start:], chunk)
	if strings.Contains(sw, "c") {
		dst = dst[:start+len(chunk)]
	}
	d.setBin(f[0], dst)
	return nil
}

func (d *Data) cmdBreplace(c *interp.CmdCall) error {
	f := c.Fields()
	if len(f) < 3 {
		return c.InvalidParams()
	}
	b, _ := d.Bin(f[0])
	for i := 1; i+1 < len(f); i += 2 {
		from, to := byte(interp.Int(f[i])), byte(interp.Int(f[i+1]))
		for k := range b {
			if b[k] == from {
				b[k] = to
			}
		}
	}
	return nil
}

// cmdBread implements /bread [-ta] <file> <S> <N> <&binvar>.
func (d *Data) cmdBread(c *interp.CmdCall) error {
	sw, rest := c.Switches()
	f := strings.Fields(rest)
	if len(f) < 4 {
		return c.InvalidParams()
	}
	data, err := os.ReadFile(d.path(f[0]))
	if err != nil {
		return c.Errorf("unable to open '%s'", f[0])
	}
	s, n := interp.Int(f[1]), interp.Int(f[2])
	s = min(max(s, 0), len(data))
	end := min(s+max(n, 0), len(data))
	chunk := data[s:end]
	if strings.Contains(sw, "t") {
		if i := bytes.IndexAny(chunk, "\r\n"); i >= 0 {
			chunk = chunk[:i]
		}
	}
	d.setBin(f[3], append([]byte(nil), chunk...))
	return nil
}

// cmdBwrite implements /bwrite [-tac] <file> <S> [N] <text|%var|&binvar>.
func (d *Data) cmdBwrite(c *interp.CmdCall) error {
	text, err := c.F.EvalWords(c.Words)
	if err != nil {
		return err
	}
	sw, rest := interp.SplitSwitches(text)
	f := strings.SplitN(rest, " ", 4)
	if len(f) < 3 {
		return c.InvalidParams()
	}
	path := d.path(f[0])
	s := interp.Int(f[1])
	n := -1
	payload := strings.Join(f[2:], " ")
	if len(f) == 4 {
		if v, ok := interp.ParseNumber(f[2]); ok {
			n, payload = int(v), f[3]
		}
	}
	var data []byte
	switch {
	case !strings.Contains(sw, "t") && strings.HasPrefix(payload, "&"):
		data, _ = d.Bin(strings.Fields(payload)[0])
	case strings.HasPrefix(payload, "%"):
		if v, ok := c.F.GetVar(payload); ok {
			data = textBytes(v.Value, strings.Contains(sw, "a"))
		}
	default:
		data = textBytes(payload, strings.Contains(sw, "a"))
	}
	if n >= 0 && n < len(data) {
		data = data[:n]
	}
	file, _ := os.ReadFile(path)
	if s < 0 || s > len(file) {
		s = len(file)
	}
	if need := s + len(data); need > len(file) {
		file = append(file, make([]byte, need-len(file))...)
	}
	copy(file[s:], data)
	if strings.Contains(sw, "c") {
		file = file[:s+len(data)]
	}
	if err := os.WriteFile(path, file, 0o644); err != nil {
		return c.Errorf("%s", err.Error())
	}
	return nil
}

// identBvar implements $bvar(&v,N,M).text|word|nword|long|nlong.
func (d *Data) identBvar(c *interp.Call) (string, error) {
	b, ok := d.Bin(c.Arg(0))
	if !ok {
		return "", nil
	}
	if len(c.Args) < 2 {
		return c.Arg(0), nil
	}
	n := interp.Int(c.Arg(1))
	if n == 0 {
		return strconv.Itoa(len(b)), nil
	}
	if n < 1 || n > len(b) {
		return "", nil
	}
	m := 1
	if len(c.Args) > 2 {
		m = interp.Int(c.Arg(2))
	}
	end := min(n-1+max(m, 1), len(b))
	chunk := b[n-1 : end]
	switch strings.ToLower(c.Prop) {
	case "text":
		if i := bytes.IndexByte(chunk, 0); i >= 0 {
			chunk = chunk[:i]
		}
		return bytesText(chunk), nil
	case "word", "nword", "long", "nlong":
		return numberProp(strings.ToLower(c.Prop), b[n-1:]), nil
	}
	parts := make([]string, len(chunk))
	for i, x := range chunk {
		parts[i] = strconv.Itoa(int(x))
	}
	return strings.Join(parts, " "), nil
}

func numberProp(prop string, b []byte) string {
	size := 2
	if strings.HasSuffix(prop, "long") {
		size = 4
	}
	if len(b) < size {
		return ""
	}
	var order binary.ByteOrder = binary.LittleEndian
	if strings.HasPrefix(prop, "n") {
		order = binary.BigEndian
	}
	if size == 2 {
		return strconv.Itoa(int(order.Uint16(b)))
	}
	return strconv.FormatUint(uint64(order.Uint32(b)), 10)
}

// identBfind implements $bfind(&binvar, N, M, [name]).text|textcs|regex.
func (d *Data) identBfind(c *interp.Call) (string, error) {
	b, _ := d.Bin(c.Arg(0))
	start := max(interp.Int(c.Arg(1))-1, 0)
	if start > len(b) {
		return "0", nil
	}
	needleText := c.Arg(2)
	prop := strings.ToLower(c.Prop)
	if prop == "regex" {
		re, err := regex.Compile(needleText)
		if err != nil {
			d.regErr = err.Error()
			return "0", nil
		}
		text := bytesText(b[start:])
		m, err := re.FindStringMatch(text)
		if err != nil || m == nil {
			return "0", nil
		}
		if len(c.Args) > 3 {
			d.storeMatches(c.Arg(3), re, []regex.Match{{Text: m.String(), Index: m.Index}}, false)
		}
		return strconv.Itoa(start + len(string([]rune(text)[:m.Index])) + 1), nil
	}
	var needle []byte
	fields := strings.Fields(needleText)
	allNumbers := prop == "" && len(fields) > 0
	for _, f := range fields {
		if _, ok := interp.ParseNumber(f); !ok {
			allNumbers = false
		}
	}
	if allNumbers {
		for _, f := range fields {
			needle = append(needle, byte(interp.Int(f)))
		}
	} else {
		needle = []byte(needleText)
	}
	hay := b[start:]
	var i int
	if prop == "textcs" || allNumbers {
		i = bytes.Index(hay, needle)
	} else {
		i = bytes.Index(bytes.ToLower(hay), bytes.ToLower(needle))
	}
	if i < 0 {
		return "0", nil
	}
	return strconv.Itoa(start + i + 1), nil
}
