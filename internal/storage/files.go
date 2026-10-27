package storage

import (
	"math/rand/v2"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/ValwareIRC/msl-runtime/internal/interp"
	"github.com/ValwareIRC/msl-runtime/internal/regex"
)

func (s *Storage) installFiles() {
	s.rt.RegisterCommand("write", interp.Command{Fn: s.cmdWrite})
	s.rt.RegisterCommand("filter", interp.Command{Fn: s.cmdFilter})
	ident(s.rt, s.identRead, "read")
	ident(s.rt, func(c *interp.Call) (string, error) { return strconv.Itoa(s.readn), nil }, "readn")
	ident(s.rt, func(c *interp.Call) (string, error) { return strconv.Itoa(s.filter), nil }, "filtered")
	ident(s.rt, func(c *interp.Call) (string, error) {
		lines, err := readLines(s.Path(c.Arg(0)))
		if err != nil {
			return "0", nil
		}
		return strconv.Itoa(len(lines)), nil
	}, "lines")
}

// readLines returns a text file's lines without line endings.
func readLines(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	text = strings.TrimSuffix(text, "\n")
	if text == "" {
		return nil, nil
	}
	return strings.Split(text, "\n"), nil
}

func writeLines(path string, lines []string, finalNewline bool) error {
	text := strings.Join(lines, "\r\n")
	if finalNewline && len(lines) > 0 {
		text += "\r\n"
	}
	return os.WriteFile(path, []byte(text), 0o644)
}

// writeOpts are the parsed /write switches.
type writeOpts struct {
	clear, insert, del, appendText, noNewline bool
	line                                      int
	scan                                      string // s, w or r (W/R: file line is the pattern)
	scanText                                  string
}

// parseWriteSwitches reads /write switches, where -lN, -sTEXT, -wTEXT and
// -rTEXT carry values; scan text may be quoted to include spaces.
func parseWriteSwitches(text string) (writeOpts, string) {
	var o writeOpts
	text = strings.TrimLeft(text, " ")
	for strings.HasPrefix(text, "-") && len(text) > 1 && text[1] != ' ' {
		word, rest, _ := strings.Cut(text, " ")
		for i := 1; i < len(word); i++ {
			switch ch := word[i]; ch {
			case 'c':
				o.clear = true
			case 'i':
				o.insert = true
			case 'd':
				o.del = true
			case 'a':
				o.appendText = true
			case 'n':
				o.noNewline = true
			case 'l', 'm':
				j := i + 1
				for j < len(word) && word[j] >= '0' && word[j] <= '9' {
					j++
				}
				if ch == 'l' {
					o.line, _ = strconv.Atoi(word[i+1 : j])
				}
				i = j - 1
			case 's', 'w', 'r', 'W', 'R':
				o.scan = string(ch)
				val := word[i+1:]
				if strings.HasPrefix(val, `"`) {
					full := val + " " + rest
					if end := strings.IndexByte(full[1:], '"'); end >= 0 {
						o.scanText = full[1 : end+1]
						rest = strings.TrimLeft(full[end+2:], " ")
						i = len(word)
						break
					}
				}
				o.scanText = val
				i = len(word)
			}
		}
		text = strings.TrimLeft(rest, " ")
	}
	return o, text
}

// scanLine finds the first line matching a /write or $read scan, starting
// at index from. It returns -1 when nothing matches.
func scanLine(lines []string, mode, pattern string, from int) (int, error) {
	for i := max(from, 0); i < len(lines); i++ {
		l := lines[i]
		var ok bool
		switch mode {
		case "s":
			ok = strings.EqualFold(l, pattern) || strings.HasPrefix(strings.ToLower(l), strings.ToLower(pattern)+" ")
		case "w":
			ok = interp.WildMatch(pattern, l, false)
		case "W":
			ok = interp.WildMatch(l, pattern, false)
		case "r", "R":
			expr, subject := pattern, l
			if mode == "R" {
				expr, subject = l, pattern
			}
			re, err := regex.Compile(expr)
			if err != nil {
				return -1, err
			}
			ok, _ = re.MatchString(subject)
		}
		if ok {
			return i, nil
		}
	}
	return -1, nil
}

// cmdWrite implements /write [-cidnalNsNwNrNmN] <filename> [text].
func (s *Storage) cmdWrite(c *interp.CmdCall) error {
	o, rest := parseWriteSwitches(c.Args)
	name, text := cutFilename(rest)
	if name == "" {
		return c.InvalidParams()
	}
	path := s.Path(name)
	var lines []string
	if !o.clear {
		lines, _ = readLines(path)
	}
	target := len(lines) // 0-based index; len means "after the end"
	if o.line > 0 {
		target = o.line - 1
	}
	if o.scan != "" {
		i, err := scanLine(lines, o.scan, o.scanText, 0)
		if err != nil {
			return c.Errorf("%s", err.Error())
		}
		if i < 0 {
			if o.del || o.line == 0 && !o.insert {
				return nil
			}
		} else {
			target = i
		}
	}
	switch {
	case o.del:
		if o.line == 0 && o.scan == "" {
			target = len(lines) - 1
		}
		if target >= 0 && target < len(lines) {
			lines = append(lines[:target], lines[target+1:]...)
		}
	case o.insert:
		for len(lines) < target {
			lines = append(lines, "")
		}
		lines = append(lines[:target], append([]string{text}, lines[target:]...)...)
	case target < len(lines) && (o.line > 0 || o.scan != ""):
		if o.appendText {
			lines[target] += text
		} else {
			lines[target] = text
		}
	default:
		for len(lines) < target {
			lines = append(lines, "")
		}
		if o.appendText && len(lines) > 0 {
			lines[len(lines)-1] += text
		} else {
			lines = append(lines, text)
		}
	}
	return writeLines(path, lines, !o.noNewline)
}

// identRead implements $read(file, [ntswrp], [matchtext], [N]). Lines are
// evaluated unless the n switch is given; a first line holding a number is
// the line count unless t is given.
func (s *Storage) identRead(c *interp.Call) (string, error) {
	args := c.Args
	if len(args) == 0 {
		return "", c.InvalidParams()
	}
	lines, err := readLines(s.Path(args[0]))
	if err != nil {
		s.readn = 0
		return "", nil
	}
	opts, match, nArg := "", "", ""
	switch len(args) {
	case 2:
		if _, ok := interp.ParseNumber(args[1]); ok {
			nArg = args[1]
		} else {
			opts = args[1]
		}
	case 3:
		opts = args[1]
		if strings.ContainsAny(opts, "swr") {
			match = args[2]
		} else {
			nArg = args[2]
		}
	default:
		if len(args) >= 4 {
			opts, match, nArg = args[1], args[2], args[3]
		}
	}
	base := 0
	if !strings.Contains(opts, "t") && len(lines) > 0 {
		if _, ok := interp.ParseNumber(strings.TrimSpace(lines[0])); ok {
			if nArg == "0" {
				return lines[0], nil
			}
			base = 1
		}
	}
	body := lines[base:]
	idx := -1
	switch {
	case strings.ContainsAny(opts, "swr"):
		mode := string(opts[strings.IndexAny(opts, "swr")])
		from := 0
		if nArg != "" {
			from = interp.Int(nArg) - 1
		}
		if idx, err = scanLine(body, mode, match, from); err != nil {
			return "", c.Errorf("%s", err.Error())
		}
		if idx >= 0 && mode == "s" {
			s.readn = idx + base + 1
			_, after, _ := strings.Cut(body[idx], " ")
			return s.evalRead(c, opts, after)
		}
	case nArg != "":
		idx = interp.Int(nArg) - 1
	case len(body) > 0:
		idx = rand.IntN(len(body))
	}
	if idx < 0 || idx >= len(body) {
		s.readn = 0
		return "", nil
	}
	s.readn = idx + base + 1
	return s.evalRead(c, opts, body[idx])
}

func (s *Storage) evalRead(c *interp.Call, opts, line string) (string, error) {
	if strings.Contains(opts, "n") {
		return line, nil
	}
	return c.F.Eval(line)
}

// cmdFilter implements /filter for files and aliases:
// /filter [-switches] [n-n2] [c s] <infile> <outfile | alias> [alias] <matchtext>.
// Window sources and targets are handled by the window package through Windows.
func (s *Storage) cmdFilter(c *interp.CmdCall) error {
	sw, rest := c.Switches()
	f := strings.Fields(rest)
	if len(f) > 0 && strings.Contains(f[0], "-") && isRange(f[0]) {
		f = f[1:]
	}
	if strings.Contains(sw, "t") && len(f) > 2 {
		f = f[2:]
	}
	if len(f) < 2 {
		return c.InvalidParams()
	}
	in, out := f[0], f[1]
	f = f[2:]
	sortAlias := ""
	if strings.Contains(sw, "a") && len(f) > 1 {
		sortAlias, f = f[0], f[1:]
	}
	match := strings.Join(f, " ")
	if match == "" {
		match = "*"
	}
	src, err := s.filterSource(c, sw, in)
	if err != nil {
		return err
	}
	var kept []string
	for i, l := range src {
		var ok bool
		if strings.Contains(sw, "g") {
			re, err := regex.Compile(match)
			if err != nil {
				return c.Errorf("%s", err.Error())
			}
			ok, _ = re.MatchString(l)
		} else {
			ok = interp.WildMatch(match, l, strings.Contains(sw, "b"))
		}
		if ok == strings.Contains(sw, "x") {
			continue
		}
		if strings.Contains(sw, "n") {
			l = strconv.Itoa(i+1) + " " + l
		}
		kept = append(kept, l)
	}
	if sortAlias != "" {
		var sortErr error
		sort.SliceStable(kept, func(i, j int) bool {
			v, err := c.F.Eval("$" + sortAlias + "(" + kept[i] + "," + kept[j] + ")")
			if err != nil {
				sortErr = err
			}
			return interp.Int(v) < 0
		})
		if sortErr != nil {
			return sortErr
		}
	} else if strings.Contains(sw, "e") || strings.Contains(sw, "u") {
		sort.SliceStable(kept, func(i, j int) bool { return strings.ToLower(kept[i]) < strings.ToLower(kept[j]) })
		if strings.Contains(sw, "e") {
			for i, j := 0, len(kept)-1; i < j; i, j = i+1, j-1 {
				kept[i], kept[j] = kept[j], kept[i]
			}
		}
	}
	s.filter = len(kept)
	return s.filterSink(c, sw, out, kept)
}

func isRange(s string) bool {
	a, b, ok := strings.Cut(s, "-")
	_, e1 := strconv.Atoi(a)
	_, e2 := strconv.Atoi(b)
	return ok && e1 == nil && (b == "" || e2 == nil)
}

func (s *Storage) filterSource(c *interp.CmdCall, sw, in string) ([]string, error) {
	if (strings.Contains(sw, "w") || strings.HasPrefix(in, "@")) && s.Windows != nil && !strings.HasPrefix(sw, "f") {
		if lines, ok := s.Windows.Lines(c.F, in); ok {
			return lines, nil
		}
	}
	lines, err := readLines(s.Path(in))
	if err != nil {
		return nil, c.Errorf("unable to open '%s'", in)
	}
	return lines, nil
}

func (s *Storage) filterSink(c *interp.CmdCall, sw, out string, lines []string) error {
	if strings.Contains(sw, "k") {
		for _, l := range lines {
			if err := c.F.ExecLine(out+" "+l, 0); err != nil {
				return err
			}
		}
		return nil
	}
	if strings.HasPrefix(out, "@") && s.Windows != nil {
		if s.Windows.SetLines(c.F, out, lines, strings.Contains(sw, "c")) {
			return nil
		}
	}
	return writeLines(s.Path(out), lines, true)
}
