package data

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/ValwareIRC/msl-runtime/internal/interp"
	"github.com/ValwareIRC/msl-runtime/internal/regex"
)

// table is a hash table. Items keep insertion order for $hget(t,N).item.
type table struct {
	name  string
	size  int
	items map[string]*item
	keys  []string // folded keys in insertion order
}

type item struct {
	key     string
	value   []byte
	binary  bool
	unsetAt time.Time
	perSec  float64 // /hinc -c, /hdec -c
	toZero  bool    // -z
}

func (t *table) get(key string) *item { return t.items[strings.ToLower(key)] }

func (t *table) put(it *item) {
	k := strings.ToLower(it.key)
	if _, ok := t.items[k]; !ok {
		t.keys = append(t.keys, k)
	}
	t.items[k] = it
}

func (t *table) del(key string) {
	k := strings.ToLower(key)
	if _, ok := t.items[k]; !ok {
		return
	}
	delete(t.items, k)
	for i, x := range t.keys {
		if x == k {
			t.keys = append(t.keys[:i], t.keys[i+1:]...)
			return
		}
	}
}

func (t *table) ordered() []*item {
	out := make([]*item, 0, len(t.keys))
	for _, k := range t.keys {
		out = append(out, t.items[k])
	}
	return out
}

func (d *Data) table(name string) *table { return d.tables[strings.ToLower(name)] }

func (d *Data) makeTable(name string, size int) *table {
	if t := d.table(name); t != nil {
		return t
	}
	if size <= 0 {
		size = 100
	}
	t := &table{name: name, size: size, items: map[string]*item{}}
	d.tables[strings.ToLower(name)] = t
	d.order = append(d.order, strings.ToLower(name))
	return t
}

func (d *Data) freeTable(name string) {
	k := strings.ToLower(name)
	delete(d.tables, k)
	for i, x := range d.order {
		if x == k {
			d.order = append(d.order[:i], d.order[i+1:]...)
			return
		}
	}
}

func (d *Data) installTables() {
	rt := d.rt
	rt.RegisterCommand("hmake", interp.Command{Fn: func(c *interp.CmdCall) error {
		_, rest := c.Switches()
		f := strings.Fields(rest)
		if len(f) == 0 {
			return c.InvalidParams()
		}
		if d.table(f[0]) != nil {
			return c.Errorf("table exists")
		}
		size := 100
		if len(f) > 1 {
			size = interp.Int(f[1])
		}
		d.makeTable(f[0], size)
		return nil
	}})
	rt.RegisterCommand("hfree", interp.Command{Fn: func(c *interp.CmdCall) error {
		sw, rest := c.Switches()
		name := strings.TrimSpace(rest)
		if strings.Contains(sw, "w") {
			for _, k := range append([]string(nil), d.order...) {
				if interp.WildMatch(name, d.tables[k].name, false) {
					d.freeTable(k)
				}
			}
			return nil
		}
		if d.table(name) == nil {
			return c.Errorf("no such table '%s'", name)
		}
		d.freeTable(name)
		return nil
	}})
	rt.RegisterCommand("hadd", interp.Command{Fn: func(c *interp.CmdCall) error { return d.cmdHadd(c, 0) }})
	rt.RegisterCommand("hinc", interp.Command{Fn: func(c *interp.CmdCall) error { return d.cmdHadd(c, 1) }})
	rt.RegisterCommand("hdec", interp.Command{Fn: func(c *interp.CmdCall) error { return d.cmdHadd(c, -1) }})
	rt.RegisterCommand("hdel", interp.Command{Fn: d.cmdHdel})
	rt.RegisterCommand("hload", interp.Command{Fn: d.cmdHload})
	rt.RegisterCommand("hsave", interp.Command{Fn: d.cmdHsave})
	rt.RegisterIdent("hget", d.identHget)
	rt.RegisterIdent("hfind", d.identHfind)
	rt.AfterRun(d.expireItems)
}

// expireItems removes -uN items whose time has passed and advances -c/-z
// counters; it also runs once per second from the engine tick.
func (d *Data) expireItems() {
	now := time.Now()
	for _, t := range d.tables {
		for _, it := range t.ordered() {
			if !it.unsetAt.IsZero() && !now.Before(it.unsetAt) {
				t.del(it.key)
			}
		}
	}
}

// Tick advances /hinc -c, /hdec -c and -z items once per second.
func (d *Data) Tick() {
	for _, t := range d.tables {
		for _, it := range t.ordered() {
			switch {
			case it.toZero:
				n := interp.Num(string(it.value)) - 1
				if n <= 0 {
					t.del(it.key)
					continue
				}
				it.value = []byte(interp.FormatNumber(n))
			case it.perSec != 0:
				it.value = []byte(interp.FormatNumber(interp.Num(string(it.value)) + it.perSec))
			}
		}
	}
	d.expireItems()
}

// cmdHadd implements /hadd, /hinc and /hdec [-smNbczkuN] <table> <item> [value].
func (d *Data) cmdHadd(c *interp.CmdCall, sign float64) error {
	sw, rest := c.Switches()
	name, rest, _ := strings.Cut(rest, " ")
	key, value, _ := strings.Cut(strings.TrimLeft(rest, " "), " ")
	if name == "" || key == "" {
		return c.InvalidParams()
	}
	t := d.table(name)
	if t == nil {
		if i := strings.IndexByte(sw, 'm'); i >= 0 {
			t = d.makeTable(name, interp.Int(sw[i+1:]))
		} else {
			return c.Errorf("no such table '%s'", name)
		}
	}
	old := t.get(key)
	it := &item{key: key}
	switch {
	case strings.Contains(sw, "b"):
		b, _ := d.Bin(strings.TrimSpace(value))
		if strings.Contains(sw, "c") {
			if i := bytes.IndexByte(b, 0); i >= 0 {
				b = b[:i]
			}
		}
		it.value, it.binary = append([]byte(nil), b...), true
	case sign != 0:
		by := 1.0
		if strings.TrimSpace(value) != "" {
			by = interp.Num(value)
		}
		cur := 0.0
		if old != nil {
			cur = interp.Num(string(old.value))
		}
		if strings.Contains(sw, "c") {
			it.value, it.perSec = []byte(interp.FormatNumber(cur)), by*sign
		} else {
			it.value = []byte(interp.FormatNumber(cur + by*sign))
		}
	default:
		it.value = []byte(value)
	}
	it.toZero = strings.Contains(sw, "z")
	if strings.Contains(sw, "k") && old != nil {
		it.unsetAt = old.unsetAt
	} else if i := strings.IndexByte(sw, 'u'); i >= 0 {
		secs := interp.Int(sw[i+1:])
		it.unsetAt = time.Now().Add(time.Duration(secs) * time.Second)
	}
	t.put(it)
	if strings.Contains(sw, "s") && !c.Quiet {
		c.F.RT.Out.Echo(interp.StatusWindow, "* Added item '"+key+"' to hash table '"+t.name+"'")
	}
	return nil
}

func (d *Data) cmdHdel(c *interp.CmdCall) error {
	sw, rest := c.Switches()
	name, key, _ := strings.Cut(strings.TrimSpace(rest), " ")
	t := d.table(name)
	if t == nil {
		return c.Errorf("no such table '%s'", name)
	}
	if strings.Contains(sw, "w") {
		for _, it := range t.ordered() {
			if interp.WildMatch(key, it.key, false) {
				t.del(it.key)
			}
		}
		return nil
	}
	t.del(key)
	return nil
}

// cmdHsave implements /hsave [-sbBniau] <table> <file> [section].
func (d *Data) cmdHsave(c *interp.CmdCall) error {
	sw, rest := c.Switches()
	name, rest, _ := strings.Cut(strings.TrimSpace(rest), " ")
	file, section, _ := strings.Cut(strings.TrimSpace(rest), " ")
	t := d.table(name)
	if t == nil {
		return c.Errorf("no such table '%s'", name)
	}
	items := t.ordered()
	if !strings.Contains(sw, "u") {
		kept := items[:0:0]
		for _, it := range items {
			if it.unsetAt.IsZero() {
				kept = append(kept, it)
			}
		}
		items = kept
	}
	path := d.path(file)
	var buf bytes.Buffer
	switch {
	case strings.Contains(sw, "i"):
		if section == "" {
			section = "hashtable"
		}
		return d.saveINI(c, file, section, items)
	case strings.ContainsAny(sw, "bB"):
		wide := strings.Contains(sw, "B")
		for _, it := range items {
			if !strings.Contains(sw, "n") {
				writeLen(&buf, len(it.key), wide)
				buf.WriteString(it.key)
			}
			writeLen(&buf, len(it.value), wide)
			buf.Write(it.value)
		}
	default:
		strip := strings.NewReplacer("\r", "", "\n", "")
		for _, it := range items {
			if !strings.Contains(sw, "n") {
				buf.WriteString(strip.Replace(it.key) + "\r\n")
			}
			buf.WriteString(strip.Replace(string(it.value)) + "\r\n")
		}
	}
	flags := os.O_CREATE | os.O_WRONLY | os.O_TRUNC
	if strings.Contains(sw, "a") {
		flags = os.O_CREATE | os.O_WRONLY | os.O_APPEND
	}
	fh, err := os.OpenFile(path, flags, 0o644)
	if err != nil {
		return c.Errorf("unable to open '%s'", file)
	}
	defer fh.Close()
	_, err = fh.Write(buf.Bytes())
	return err
}

func writeLen(buf *bytes.Buffer, n int, wide bool) {
	if wide {
		binary.Write(buf, binary.LittleEndian, uint32(n))
		return
	}
	binary.Write(buf, binary.LittleEndian, uint16(min(n, 0xffff)))
}

func (d *Data) saveINI(c *interp.CmdCall, file, section string, items []*item) error {
	if err := c.F.ExecLine("!remini "+quote(file)+" "+section, 0); err != nil {
		return err
	}
	for _, it := range items {
		v := strings.NewReplacer("\r", "", "\n", "").Replace(string(it.value))
		if err := c.F.ExecLine("!writeini -z "+quote(file)+" "+section+" "+it.key+" "+v, 0); err != nil {
			return err
		}
	}
	return nil
}

func quote(s string) string {
	if strings.ContainsRune(s, ' ') {
		return `"` + s + `"`
	}
	return s
}

// cmdHload implements /hload [-smNbBni] <table> <file> [section].
func (d *Data) cmdHload(c *interp.CmdCall) error {
	sw, rest := c.Switches()
	name, rest, _ := strings.Cut(strings.TrimSpace(rest), " ")
	file, section, _ := strings.Cut(strings.TrimSpace(rest), " ")
	t := d.table(name)
	if t == nil {
		i := strings.IndexByte(sw, 'm')
		if i < 0 {
			return c.Errorf("no such table '%s'", name)
		}
		t = d.makeTable(name, interp.Int(sw[i+1:]))
	}
	path := d.path(file)
	switch {
	case strings.Contains(sw, "i"):
		return d.loadINI(c, t, path, section)
	case strings.ContainsAny(sw, "bB"):
		data, err := os.ReadFile(path)
		if err != nil {
			return c.Errorf("unable to open '%s'", file)
		}
		r := bytes.NewReader(data)
		wide := strings.Contains(sw, "B")
		for n := 1; ; n++ {
			key := strconv.Itoa(n)
			if !strings.Contains(sw, "n") {
				k, err := readChunk(r, wide)
				if err != nil {
					break
				}
				key = string(k)
			}
			v, err := readChunk(r, wide)
			if err != nil {
				break
			}
			t.put(&item{key: key, value: v, binary: true})
		}
	default:
		fh, err := os.Open(path)
		if err != nil {
			return c.Errorf("unable to open '%s'", file)
		}
		defer fh.Close()
		sc := bufio.NewScanner(fh)
		sc.Buffer(make([]byte, 64*1024), 16*1024*1024)
		n := 0
		for sc.Scan() {
			n++
			key := strconv.Itoa(n)
			if !strings.Contains(sw, "n") {
				key = strings.TrimRight(sc.Text(), "\r")
				if !sc.Scan() {
					t.put(&item{key: key})
					break
				}
			}
			t.put(&item{key: key, value: []byte(strings.TrimRight(sc.Text(), "\r"))})
		}
	}
	return nil
}

func readChunk(r io.Reader, wide bool) ([]byte, error) {
	var n int
	if wide {
		var v uint32
		if err := binary.Read(r, binary.LittleEndian, &v); err != nil {
			return nil, err
		}
		n = int(v)
	} else {
		var v uint16
		if err := binary.Read(r, binary.LittleEndian, &v); err != nil {
			return nil, err
		}
		n = int(v)
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		return nil, errors.New("truncated")
	}
	return buf, nil
}

func (d *Data) loadINI(c *interp.CmdCall, t *table, path, section string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return c.Errorf("unable to open '%s'", path)
	}
	if section == "" {
		section = "hashtable"
	}
	in := false
	for _, line := range strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n") {
		trim := strings.TrimSpace(line)
		if strings.HasPrefix(trim, "[") && strings.HasSuffix(trim, "]") {
			in = strings.EqualFold(trim[1:len(trim)-1], section)
			continue
		}
		if k, v, ok := strings.Cut(line, "="); ok && in {
			t.put(&item{key: k, value: []byte(v)})
		}
	}
	return nil
}

// identHget implements $hget(table|N), $hget(table, item[, &binvar]) and
// $hget(table, N).item|data.
func (d *Data) identHget(c *interp.Call) (string, error) {
	var t *table
	if n, ok := interp.ParseNumber(c.Arg(0)); ok {
		i := int(n)
		if i == 0 && len(c.Args) == 1 {
			return strconv.Itoa(len(d.order)), nil
		}
		if i >= 1 && i <= len(d.order) {
			t = d.tables[d.order[i-1]]
		}
	} else {
		t = d.table(c.Arg(0))
	}
	if t == nil {
		return "", nil
	}
	if len(c.Args) == 1 {
		if strings.EqualFold(c.Prop, "size") {
			return strconv.Itoa(t.size), nil
		}
		return t.name, nil
	}
	prop := strings.ToLower(c.Prop)
	if prop == "item" || prop == "data" {
		n := interp.Int(c.Arg(1))
		items := t.ordered()
		if n == 0 {
			return strconv.Itoa(len(items)), nil
		}
		if n < 1 || n > len(items) {
			return "", nil
		}
		it := items[n-1]
		if prop == "item" {
			return it.key, nil
		}
		return d.itemValue(c, it)
	}
	it := t.get(c.Arg(1))
	if it == nil {
		return "", nil
	}
	if prop == "unset" {
		if it.unsetAt.IsZero() {
			return "0", nil
		}
		return strconv.Itoa(int(time.Until(it.unsetAt).Seconds() + 0.5)), nil
	}
	return d.itemValue(c, it)
}

// itemValue returns an item's text, or stores it in the &binvar given as
// the third argument and returns its length.
func (d *Data) itemValue(c *interp.Call, it *item) (string, error) {
	if len(c.Args) > 2 && strings.HasPrefix(c.Arg(2), "&") {
		d.setBin(c.Arg(2), append([]byte(nil), it.value...))
		return strconv.Itoa(len(it.value)), nil
	}
	return bytesText(it.value), nil
}

// identHfind implements $hfind(table, text, N, M, command).data.
func (d *Data) identHfind(c *interp.Call) (string, error) {
	t := d.table(c.Arg(0))
	if t == nil {
		return "", nil
	}
	text := c.Arg(1)
	n := interp.Int(c.Arg(2))
	mode := c.Arg(3)
	if mode == "" {
		mode = "n"
	}
	command := strings.Join(c.Args[min(4, len(c.Args)):], ",")
	byData := strings.EqualFold(c.Prop, "data")
	count := 0
	for _, it := range t.ordered() {
		subject := it.key
		if byData {
			subject = bytesText(it.value)
		}
		ok, err := hfindMatch(mode, text, subject)
		if err != nil {
			d.regErr = err.Error()
			return "", nil
		}
		if !ok {
			continue
		}
		count++
		if command != "" {
			f := d.rt.NewFrame(c.F.File, c.F.Event)
			f.Params = strings.Fields(it.key)
			if err := f.ExecLine(command, 0); err != nil {
				if interp.IsHalt(err) {
					break
				}
				return "", err
			}
		}
		if n > 0 && count == n {
			return it.key, nil
		}
	}
	if n == 0 {
		return strconv.Itoa(count), nil
	}
	return "", nil
}

func hfindMatch(mode, text, subject string) (bool, error) {
	switch mode[0] {
	case 'w':
		return interp.WildMatch(text, subject, false), nil
	case 'W':
		return interp.WildMatch(subject, text, false), nil
	case 'r', 'R':
		expr, s := text, subject
		if mode[0] == 'R' {
			expr, s = subject, text
		}
		re, err := regex.Compile(expr)
		if err != nil {
			return false, err
		}
		return re.MatchString(s)
	}
	return strings.EqualFold(text, subject), nil
}
