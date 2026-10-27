package control

import (
	"bytes"
	"context"
	"crypto/tls"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/ValwareIRC/msl-runtime/internal/data"
	"github.com/ValwareIRC/msl-runtime/internal/interp"
)

// urlJob is one $urlget transfer.
type urlJob struct {
	id       int
	url      string
	redirect string
	method   string
	target   string
	alias    string
	binvar   bool
	state    string // ok, fail, cancel, run
	size     int64
	rcvd     int64
	reply    string // response status and headers
	start    time.Time
	took     time.Duration
	body     []byte
	cancel   context.CancelFunc
	file     *interp.Frame
}

var urlMethods = map[byte]string{'h': "HEAD", 'g': "GET", 'p': "POST", 'u': "PUT", 'a': "PATCH", 'd': "DELETE"}

func (c *Control) installURL() {
	c.rt.RegisterIdent("urlget", c.identURLGet)
}

func (c *Control) job(arg string) *urlJob {
	n := interp.Int(arg)
	for _, j := range c.urls {
		if j.id == n {
			return j
		}
	}
	if n >= 1 && n <= len(c.urls) {
		return c.urls[n-1]
	}
	return nil
}

// identURLGet implements $urlget(url, options, target, alias, headers, body)
// and $urlget(N|id).prop. Transfers run in the background; the alias is
// called with the id when one completes.
func (c *Control) identURLGet(cl *interp.Call) (string, error) {
	if len(cl.Args) == 1 || cl.Prop != "" {
		return c.urlProp(cl)
	}
	opts := cl.Arg(1)
	if strings.Contains(opts, "c") {
		if j := c.job(cl.Arg(0)); j != nil && j.cancel != nil {
			j.cancel()
			j.cancel = nil
			j.state = "cancel"
		}
		return "1", nil
	}
	method := "GET"
	for k, m := range urlMethods {
		if strings.IndexByte(opts, k) >= 0 {
			method = m
		}
	}
	c.nextURL++
	j := &urlJob{
		id: c.nextURL, url: cl.Arg(0), method: method, target: cl.Arg(2), alias: cl.Arg(3),
		binvar: strings.Contains(opts, "b"), state: "run", start: time.Now(),
	}
	d := data.From(c.rt)
	var headers, body []byte
	if d != nil {
		headers, _ = d.Bin(cl.Arg(4))
		body, _ = d.Bin(cl.Arg(5))
	}
	req, err := http.NewRequest(method, j.url, bytes.NewReader(body))
	if err != nil {
		return "0", nil
	}
	for _, line := range strings.Split(strings.ReplaceAll(string(headers), "\r\n", "\n"), "\n") {
		if k, v, ok := strings.Cut(line, ":"); ok {
			req.Header.Add(strings.TrimSpace(k), strings.TrimSpace(v))
		}
	}
	client := &http.Client{Timeout: 5 * time.Minute}
	if strings.Contains(opts, "i") {
		client.Transport = &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}
	}
	if strings.Contains(opts, "k") {
		client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	}
	saveErrors := strings.Contains(opts, "e")
	path := j.target
	if !j.binvar && c.st != nil {
		path = c.st.Path(j.target)
	}
	resume := strings.Contains(opts, "r")
	if resume && !j.binvar {
		if st, err := os.Stat(path); err == nil {
			req.Header.Set("Range", "bytes="+strconv.FormatInt(st.Size(), 10)+"-")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	j.cancel = cancel
	req = req.WithContext(ctx)
	c.urls = append(c.urls, j)
	go func() {
		resp, err := client.Do(req)
		var payload []byte
		var reply string
		ok := false
		if err == nil {
			payload, err = io.ReadAll(resp.Body)
			resp.Body.Close()
			var hb strings.Builder
			hb.WriteString(resp.Proto + " " + resp.Status + "\r\n")
			resp.Header.Write(&hb)
			reply = hb.String()
			ok = err == nil && (resp.StatusCode < 400 || saveErrors)
			j.redirect = resp.Request.URL.String()
		}
		c.rt.Post(func() { c.finishURL(j, payload, reply, ok, path, resume) })
	}()
	return strconv.Itoa(j.id), nil
}

func (c *Control) finishURL(j *urlJob, payload []byte, reply string, ok bool, path string, resume bool) {
	if j.state == "cancel" {
		return
	}
	j.cancel = nil
	j.reply, j.took = reply, time.Since(j.start)
	j.rcvd, j.size = int64(len(payload)), int64(len(payload))
	j.state = "fail"
	if ok {
		j.state = "ok"
		if j.binvar {
			if d := data.From(c.rt); d != nil {
				d.SetBin(j.target, payload)
			}
		} else if j.target != "" {
			flags := os.O_CREATE | os.O_WRONLY | os.O_TRUNC
			if resume {
				flags = os.O_CREATE | os.O_WRONLY | os.O_APPEND
			}
			if fh, err := os.OpenFile(path, flags, 0o644); err == nil {
				fh.Write(payload)
				fh.Close()
			} else {
				j.state = "fail"
			}
		}
	}
	if j.alias == "" {
		return
	}
	f := c.rt.NewFrame(nil, nil)
	f.Caller = "other"
	_ = c.rt.Run(func() error { return f.ExecLine(j.alias+" "+strconv.Itoa(j.id), 0) })
}

func (c *Control) urlProp(cl *interp.Call) (string, error) {
	if n, ok := interp.ParseNumber(cl.Arg(0)); ok && int(n) == 0 {
		return strconv.Itoa(len(c.urls)), nil
	}
	j := c.job(cl.Arg(0))
	if j == nil {
		return "", nil
	}
	switch strings.ToLower(cl.Prop) {
	case "url":
		return j.url, nil
	case "redirect":
		return j.redirect, nil
	case "method":
		return strings.ToLower(j.method), nil
	case "type":
		if j.binvar {
			return "binvar", nil
		}
		return "file", nil
	case "target":
		return j.target, nil
	case "alias":
		return j.alias, nil
	case "state":
		return j.state, nil
	case "size":
		return strconv.FormatInt(j.size, 10), nil
	case "rcvd":
		return strconv.FormatInt(j.rcvd, 10), nil
	case "time":
		return strconv.FormatInt(j.took.Milliseconds(), 10), nil
	case "reply":
		return j.reply, nil
	case "resume":
		return interp.Bool(false), nil
	}
	return strconv.Itoa(j.id), nil
}
