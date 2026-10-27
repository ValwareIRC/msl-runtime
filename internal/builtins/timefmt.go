package builtins

import (
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/ValwareIRC/msl-runtime/internal/interp"
)

// started is when the runtime process began, the base of $ticks and $uptime(mirc).
var started = time.Now()

// Default timestamp formats, matching mIRC's out-of-the-box settings.
const (
	DefaultTimestampFmt = "[HH:nn]"
	DefaultLogstampFmt  = "[HH:nn]"
	fullDateFmt         = "ddd mmm dd HH:nn:ss yyyy"
)

func installTime(rt *interp.Runtime) {
	ident(rt, identAsctime, "asctime")
	ident(rt, timeIdent("HH:nn:ss"), "time")
	ident(rt, timeIdent("dd/mm/yyyy"), "date")
	ident(rt, timeIdent("mm/dd/yyyy"), "adate")
	ident(rt, timeIdent("dddd"), "day")
	ident(rt, timeIdent(fullDateFmt), "fulldate")
	ident(rt, timeIdent(DefaultTimestampFmt), "timestamp")
	ident(rt, timeIdent(DefaultLogstampFmt), "logstamp")
	ident(rt, func(*interp.Call) (string, error) { return DefaultTimestampFmt, nil }, "timestampfmt")
	ident(rt, func(*interp.Call) (string, error) { return DefaultLogstampFmt, nil }, "logstampfmt")
	ident(rt, identCtime, "ctime")
	ident(rt, identGmt, "gmt")
	ident(rt, identDuration, "duration")
	ident(rt, identTimezone, "timezone")
	ident(rt, identDaylight, "daylight")
	ident(rt, func(*interp.Call) (string, error) {
		return strconv.FormatInt(time.Since(started).Milliseconds(), 10), nil
	}, "ticks", "ticksqpc")
	ident(rt, identUptime, "uptime")
}

// timeIdent returns an identifier that formats the current time, or the
// time given as its argument, with a default format overridable by an argument.
func timeIdent(def string) interp.Ident {
	return func(c *interp.Call) (string, error) {
		if c.Paren && c.Arg(0) != "" {
			return FormatTime(time.Now(), c.Arg(0)), nil
		}
		return FormatTime(time.Now(), def), nil
	}
}

// identAsctime implements $asctime([N],[format]).
func identAsctime(c *interp.Call) (string, error) {
	t := time.Now()
	format := fullDateFmt
	switch len(c.Args) {
	case 1:
		if _, ok := interp.ParseNumber(c.Arg(0)); ok {
			t = time.Unix(int64(interp.Num(c.Arg(0))), 0)
		} else {
			format = c.Arg(0)
		}
	case 2:
		t = time.Unix(int64(interp.Num(c.Arg(0))), 0)
		format = c.Arg(1)
	}
	return FormatTime(t, format), nil
}

// identGmt returns the current time as a $ctime value, or formats it in GMT.
func identGmt(c *interp.Call) (string, error) {
	if c.Paren && c.Arg(0) != "" {
		return FormatTime(time.Now().UTC(), c.Arg(0)), nil
	}
	return strconv.FormatInt(time.Now().Unix(), 10), nil
}

// timeTokens are $asctime format items, longest first so matching is greedy.
var timeTokens = []string{
	"yyyy", "mmmm", "dddd", "zzz", "mmm", "ddd", "yy", "mm", "dd", "hh", "HH",
	"nn", "ss", "tt", "TT", "oo", "zz", "m", "d", "h", "H", "n", "s", "t", "T", "z",
}

// FormatTime renders t with an mIRC time format.
func FormatTime(t time.Time, format string) string {
	var b strings.Builder
	for i := 0; i < len(format); {
		tok := ""
		for _, cand := range timeTokens {
			if strings.HasPrefix(format[i:], cand) {
				tok = cand
				break
			}
		}
		if tok == "" {
			b.WriteByte(format[i])
			i++
			continue
		}
		b.WriteString(timeItem(t, tok))
		i += len(tok)
	}
	return b.String()
}

func timeItem(t time.Time, tok string) string {
	h12 := t.Hour() % 12
	if h12 == 0 {
		h12 = 12
	}
	two := func(n int) string { return pad2(n) }
	switch tok {
	case "yyyy":
		return strconv.Itoa(t.Year())
	case "yy":
		return two(t.Year() % 100)
	case "mmmm":
		return t.Month().String()
	case "mmm":
		return t.Month().String()[:3]
	case "mm":
		return two(int(t.Month()))
	case "m":
		return strconv.Itoa(int(t.Month()))
	case "dddd":
		return t.Weekday().String()
	case "ddd":
		return t.Weekday().String()[:3]
	case "dd":
		return two(t.Day())
	case "d":
		return strconv.Itoa(t.Day())
	case "hh":
		return two(h12)
	case "h":
		return strconv.Itoa(h12)
	case "HH":
		return two(t.Hour())
	case "H":
		return strconv.Itoa(t.Hour())
	case "nn":
		return two(t.Minute())
	case "n":
		return strconv.Itoa(t.Minute())
	case "ss":
		return two(t.Second())
	case "s":
		return strconv.Itoa(t.Second())
	case "t", "tt", "T", "TT":
		s := "am"
		if t.Hour() >= 12 {
			s = "pm"
		}
		if len(tok) == 1 {
			s = s[:1]
		}
		if tok[0] == 'T' {
			s = strings.ToUpper(s)
		}
		return s
	case "oo":
		return ordinalSuffix(t.Day())
	case "z", "zz", "zzz":
		_, off := t.Zone()
		sign := "+"
		if off < 0 {
			sign, off = "-", -off
		}
		hh, mm := off/3600, off%3600/60
		switch tok {
		case "z":
			return sign + strconv.Itoa(hh)
		case "zz":
			return sign + pad2(hh) + pad2(mm)
		}
		return sign + pad2(hh) + pad2(mm) + " GMT"
	}
	return tok
}

func pad2(n int) string {
	if n < 10 && n >= 0 {
		return "0" + strconv.Itoa(n)
	}
	return strconv.Itoa(n)
}

func ordinalSuffix(n int) string {
	if n%100 >= 11 && n%100 <= 13 {
		return "th"
	}
	switch n % 10 {
	case 1:
		return "st"
	case 2:
		return "nd"
	case 3:
		return "rd"
	}
	return "th"
}

// identCtime returns the current $ctime, or parses a date and time text.
func identCtime(c *interp.Call) (string, error) {
	if !c.Paren || strings.TrimSpace(c.Arg(0)) == "" {
		return strconv.FormatInt(time.Now().Unix(), 10), nil
	}
	t, ok := ParseDate(c.Arg(0), time.Local)
	if !ok {
		return "", nil
	}
	return strconv.FormatInt(t.Unix(), 10), nil
}

// ParseDate reads the loose date formats $ctime(text) accepts, such as
// "January 1 1970 00:00:00", "3rd August 1987 3:46pm", "21/4/72 1:30:37" and
// "Wed 1998-3-27 21:16". Missing parts default to today and midnight.
func ParseDate(s string, loc *time.Location) (time.Time, bool) {
	now := time.Now().In(loc)
	year, month, day := now.Year(), int(now.Month()), now.Day()
	hour, minute, sec := 0, 0, 0
	gotDate := false
	var loose []int // bare numbers not yet placed

	for _, f := range strings.FieldsFunc(s, func(r rune) bool { return r == ' ' || r == ',' }) {
		lf := strings.ToLower(f)
		switch {
		case strings.Contains(f, ":"):
			pm := strings.HasSuffix(lf, "pm")
			am := strings.HasSuffix(lf, "am")
			parts := strings.Split(strings.TrimRight(lf, "apm"), ":")
			hour = atoi(parts[0])
			if len(parts) > 1 {
				minute = atoi(parts[1])
			}
			if len(parts) > 2 {
				sec = atoi(parts[2])
			}
			if pm && hour < 12 {
				hour += 12
			}
			if am && hour == 12 {
				hour = 0
			}
		case strings.ContainsAny(f, "/-") && len(f) > 2:
			sep := "/"
			if strings.Contains(f, "-") {
				sep = "-"
			}
			p := strings.Split(f, sep)
			if len(p) < 2 {
				return time.Time{}, false
			}
			if sep == "-" && len(p[0]) == 4 {
				year, month = atoi(p[0]), atoi(p[1])
				if len(p) > 2 {
					day = atoi(p[2])
				}
			} else {
				day, month = atoi(p[0]), atoi(p[1])
				if len(p) > 2 {
					year = fullYear(atoi(p[2]))
				}
			}
			gotDate = true
		case monthNumber(lf) > 0:
			month = monthNumber(lf)
			gotDate = true
		case isWeekday(lf):
		default:
			digits := strings.TrimRightFunc(lf, unicode.IsLetter)
			n, err := strconv.Atoi(digits)
			if err != nil {
				return time.Time{}, false
			}
			loose = append(loose, n)
		}
	}
	for _, n := range loose {
		switch {
		case n > 31 || (gotDate && day != now.Day() && n > 0):
			year = fullYear(n)
		default:
			day = n
			gotDate = true
		}
	}
	return time.Date(year, time.Month(month), day, hour, minute, sec, 0, loc), true
}

func atoi(s string) int { n, _ := strconv.Atoi(strings.TrimSpace(s)); return n }

func fullYear(y int) int {
	switch {
	case y >= 100:
		return y
	case y >= 70:
		return 1900 + y
	}
	return 2000 + y
}

func monthNumber(s string) int {
	if len(s) < 3 {
		return 0
	}
	for m := time.January; m <= time.December; m++ {
		if strings.HasPrefix(strings.ToLower(m.String()), s[:3]) && strings.HasPrefix(strings.ToLower(m.String()), s) {
			return int(m)
		}
	}
	return 0
}

func isWeekday(s string) bool {
	if len(s) < 3 {
		return false
	}
	for d := time.Sunday; d <= time.Saturday; d++ {
		if strings.HasPrefix(strings.ToLower(d.String()), s) {
			return true
		}
	}
	return false
}

// durationUnits are $duration's units, largest first.
var durationUnits = []struct {
	secs           int64
	single, plural string
}{
	{604800, "wk", "wks"}, {86400, "day", "days"}, {3600, "hr", "hrs"},
	{60, "min", "mins"}, {1, "sec", "secs"},
}

// identDuration formats seconds as "1wk 2days 3hrs 4mins 5secs", or parses
// that text back into seconds.
func identDuration(c *interp.Call) (string, error) {
	in := strings.TrimSpace(c.Arg(0))
	if _, ok := interp.ParseNumber(in); !ok && in != "" {
		return strconv.FormatInt(parseDuration(in), 10), nil
	}
	secs := int64(interp.Num(in))
	switch interp.Int(c.Arg(1)) {
	case 3:
		return pad2(int(secs/3600)) + ":" + pad2(int(secs%3600/60)) + ":" + pad2(int(secs%60)), nil
	case 2:
		return FormatDuration(secs, false), nil
	}
	return FormatDuration(secs, true), nil
}

// FormatDuration renders seconds in $duration's style.
func FormatDuration(secs int64, withSeconds bool) string {
	neg := secs < 0
	if neg {
		secs = -secs
	}
	var parts []string
	for _, u := range durationUnits {
		if u.secs == 1 && !withSeconds {
			break
		}
		n := secs / u.secs
		secs %= u.secs
		switch n {
		case 0:
		case 1:
			parts = append(parts, "1"+u.single)
		default:
			parts = append(parts, strconv.FormatInt(n, 10)+u.plural)
		}
	}
	if len(parts) == 0 {
		if withSeconds {
			return "0secs"
		}
		return "0mins"
	}
	s := strings.Join(parts, " ")
	if neg {
		return "-" + s
	}
	return s
}

func parseDuration(s string) int64 {
	var total int64
	for _, f := range strings.Fields(s) {
		i := 0
		for i < len(f) && (f[i] >= '0' && f[i] <= '9' || f[i] == '-') {
			i++
		}
		n, _ := strconv.ParseInt(f[:i], 10, 64)
		unit := strings.ToLower(f[i:])
		for _, u := range durationUnits {
			if unit == u.single || unit == u.plural {
				total += n * u.secs
			}
		}
	}
	return total
}

// identTimezone returns the local offset in seconds west of GMT, as Windows reports it.
func identTimezone(*interp.Call) (string, error) {
	_, off := time.Date(time.Now().Year(), 1, 1, 0, 0, 0, 0, time.Local).Zone()
	return strconv.Itoa(-off), nil
}

func identDaylight(*interp.Call) (string, error) {
	now := time.Now()
	_, std := time.Date(now.Year(), 1, 1, 0, 0, 0, 0, time.Local).Zone()
	_, cur := now.Zone()
	if cur == std {
		return "0", nil
	}
	return strconv.Itoa(cur - std), nil
}

// uptimeSources resolve $uptime items; the irc package adds "server".
var uptimeSources = map[string]func(*interp.Call) time.Duration{
	"mirc":   func(*interp.Call) time.Duration { return time.Since(started) },
	"system": func(*interp.Call) time.Duration { return systemUptime() },
}

// RegisterUptime adds an $uptime(item) source.
func RegisterUptime(item string, fn func(*interp.Call) time.Duration) {
	uptimeSources[item] = fn
}

func identUptime(c *interp.Call) (string, error) {
	src, ok := uptimeSources[strings.ToLower(c.Arg(0))]
	if !ok {
		return "", c.InvalidParams()
	}
	d := src(c)
	switch interp.Int(c.Arg(1)) {
	case 1:
		return FormatDuration(int64(d.Seconds()), true), nil
	case 2:
		return FormatDuration(int64(d.Seconds()), false), nil
	case 3:
		return strconv.FormatInt(int64(d.Seconds()), 10), nil
	}
	return strconv.FormatInt(d.Milliseconds(), 10), nil
}

// systemUptime approximates the operating system's uptime with the process's.
// Portable Go has no boot-time API; per-OS files can refine this.
func systemUptime() time.Duration { return time.Since(started) }
