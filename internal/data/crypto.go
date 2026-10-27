package data

import (
	"archive/zip"
	"bytes"
	"compress/flate"
	"compress/gzip"
	"compress/zlib"
	"crypto/hmac"
	"crypto/md5"
	"crypto/pbkdf2"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base32"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"hash"
	"hash/crc32"
	"hash/crc64"
	"hash/fnv"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/argon2"

	"github.com/ValwareIRC/msl-runtime/internal/interp"
)

func (d *Data) installCrypto() {
	rt := d.rt
	for name, h := range hashes {
		h := h
		rt.RegisterIdent(name, func(c *interp.Call) (string, error) {
			b, err := d.source(c, 0, 1, 0)
			if err != nil {
				return "", nil
			}
			hh := h()
			hh.Write(b)
			return hex.EncodeToString(hh.Sum(nil)), nil
		})
	}
	rt.RegisterIdent("crc", func(c *interp.Call) (string, error) {
		b, err := d.source(c, 0, 1, 2)
		if err != nil {
			return "", nil
		}
		return fmt.Sprintf("%08X", crc32.ChecksumIEEE(b)), nil
	})
	rt.RegisterIdent("crc64", func(c *interp.Call) (string, error) {
		b, err := d.source(c, 0, 1, 2)
		if err != nil {
			return "", nil
		}
		return fmt.Sprintf("%016X", crc64.Checksum(b, crc64.MakeTable(crc64.ECMA))), nil
	})
	rt.RegisterIdent("hmac", d.identHmac)
	rt.RegisterIdent("hotp", d.identHotp)
	rt.RegisterIdent("totp", d.identTotp)
	rt.RegisterIdent("hash", identHash)
	rt.RegisterIdent("encode", func(c *interp.Call) (string, error) { return d.identEncode(c, true) })
	rt.RegisterIdent("decode", func(c *interp.Call) (string, error) { return d.identEncode(c, false) })
	rt.RegisterIdent("compress", func(c *interp.Call) (string, error) { return d.identCompress(c, true) })
	rt.RegisterIdent("decompress", func(c *interp.Call) (string, error) { return d.identCompress(c, false) })
	rt.RegisterIdent("zip", d.identZip)
	rt.RegisterIdent("pbkdf2", d.identPbkdf2)
	rt.RegisterIdent("argon2", d.identArgon2)
}

var hashes = map[string]func() hash.Hash{
	"md5": md5.New, "sha1": sha1.New, "sha256": sha256.New, "sha384": sha512.New384, "sha512": sha512.New,
}

// source reads the data an identifier works on: argument i is text,
// a &binvar or a filename, chosen by the N argument at index modeArg
// (0 text, 1 binvar, 2 file), defaulting to def.
func (d *Data) source(c *interp.Call, i, modeArg, def int) ([]byte, error) {
	mode := def
	if len(c.Args) > modeArg && modeArg != i {
		mode = interp.Int(c.Arg(modeArg))
	}
	switch mode {
	case 1:
		b, ok := d.Bin(c.Arg(i))
		if !ok {
			return nil, fmt.Errorf("no binvar")
		}
		return b, nil
	case 2:
		return os.ReadFile(d.path(c.Arg(i)))
	}
	return []byte(c.Arg(i)), nil
}

// bytesArg reads text or, when opts contains b, a &binvar.
func (d *Data) bytesArg(arg string, binvar bool) []byte {
	if binvar {
		b, _ := d.Bin(arg)
		return b
	}
	return []byte(arg)
}

func hashByName(name string) func() hash.Hash {
	if h, ok := hashes[strings.ToLower(name)]; ok {
		return h
	}
	return nil
}

// identHmac implements $hmac(text|&binvar|file, key, hash, N).
func (d *Data) identHmac(c *interp.Call) (string, error) {
	b, err := d.source(c, 0, 3, 0)
	if err != nil {
		return "", nil
	}
	h := hashByName(c.Arg(2))
	if h == nil {
		h = sha256.New
	}
	m := hmac.New(h, []byte(c.Arg(1)))
	m.Write(b)
	return hex.EncodeToString(m.Sum(nil)), nil
}

// otpKey accepts a base32 secret, or the raw text when it is not base32.
func otpKey(s string) []byte {
	clean := strings.ToUpper(strings.ReplaceAll(s, " ", ""))
	if k, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(strings.TrimRight(clean, "=")); err == nil && len(k) > 0 {
		return k
	}
	return []byte(s)
}

func otp(key []byte, counter uint64, h func() hash.Hash, digits int) string {
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], counter)
	m := hmac.New(h, key)
	m.Write(msg[:])
	sum := m.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	code := binary.BigEndian.Uint32(sum[off:off+4]) & 0x7fffffff
	mod := uint32(1)
	for range digits {
		mod *= 10
	}
	return fmt.Sprintf("%0*d", digits, code%mod)
}

func otpParams(c *interp.Call, hashArg, digitsArg int) (func() hash.Hash, int) {
	h := hashByName(c.Arg(hashArg))
	if h == nil {
		h = sha1.New
	}
	digits := 6
	if n := interp.Int(c.Arg(digitsArg)); n > 0 {
		digits = n
	}
	return h, digits
}

// identHotp implements $hotp(key, count, hash, digits).
func (d *Data) identHotp(c *interp.Call) (string, error) {
	h, digits := otpParams(c, 2, 3)
	return otp(otpKey(c.Arg(0)), uint64(interp.Num(c.Arg(1))), h, digits), nil
}

// identTotp implements $totp(key, time, hash, digits, period).
func (d *Data) identTotp(c *interp.Call) (string, error) {
	h, digits := otpParams(c, 2, 3)
	t := time.Now().Unix()
	if c.Arg(1) != "" {
		t = int64(interp.Num(c.Arg(1)))
	}
	period := int64(30)
	if p := interp.Int(c.Arg(4)); p > 0 {
		period = int64(p)
	}
	return otp(otpKey(c.Arg(0)), uint64(t/period), h, digits), nil
}

// identHash implements $hash(text, bits): a hash of text reduced to bits.
func identHash(c *interp.Call) (string, error) {
	bits := interp.Int(c.Arg(1))
	if bits <= 0 || bits > 32 {
		bits = 32
	}
	h := fnv.New32a()
	h.Write([]byte(strings.ToLower(c.Arg(0))))
	v := h.Sum32()
	if bits < 32 {
		v &= (1 << bits) - 1
	}
	return strconv.FormatUint(uint64(v), 10), nil
}

// identEncode implements $encode/$decode(text|&binvar, amuvbtxy[blowfish], N|key, [salt|iv]).
func (d *Data) identEncode(c *interp.Call, encode bool) (string, error) {
	opts := c.Arg(1)
	if opts == "" {
		opts = "u"
	}
	binvar := strings.Contains(opts, "b")
	in := d.bytesArg(c.Arg(0), binvar)
	var out []byte
	var err error
	if strings.ContainsAny(opts, "ce") {
		out, err = blowfishCodec(in, opts, c.Arg(2), c.Arg(3), encode)
	} else if encode {
		out = encodeBytes(in, opts)
	} else {
		out, err = decodeBytes(in, opts)
	}
	if err != nil {
		return "", nil
	}
	if binvar {
		d.setBin(c.Arg(0), out)
		return strconv.Itoa(len(out)), nil
	}
	if encode && !strings.ContainsAny(opts, "ce") && len(c.Args) > 2 && c.Arg(2) != "" {
		chunks := chunk(string(out), chunkSize(opts))
		n := interp.Int(c.Arg(2))
		if n == 0 {
			return strconv.Itoa(len(chunks)), nil
		}
		if n >= 1 && n <= len(chunks) {
			return chunks[n-1], nil
		}
		return "", nil
	}
	if !encode {
		return bytesText(out), nil
	}
	return string(out), nil
}

func chunkSize(opts string) int {
	if strings.Contains(opts, "m") {
		return 76
	}
	return 61
}

func chunk(s string, n int) []string {
	var out []string
	for len(s) > n {
		out = append(out, s[:n])
		s = s[n:]
	}
	return append(out, s)
}

func encodeBytes(in []byte, opts string) []byte {
	switch {
	case strings.Contains(opts, "m"):
		return []byte(base64.StdEncoding.EncodeToString(in))
	case strings.Contains(opts, "a"):
		return []byte(base32.StdEncoding.EncodeToString(in))
	case strings.Contains(opts, "v"):
		return []byte(z85Encode(in))
	case strings.Contains(opts, "x"):
		return []byte(percentEncode(in))
	case strings.Contains(opts, "y"):
		return []byte(punyEncode(string(in)))
	}
	return []byte(uuEncode(in))
}

func decodeBytes(in []byte, opts string) ([]byte, error) {
	s := string(in)
	switch {
	case strings.Contains(opts, "m"):
		return base64.StdEncoding.DecodeString(strings.TrimSpace(s))
	case strings.Contains(opts, "a"):
		return base32.StdEncoding.DecodeString(strings.TrimSpace(s))
	case strings.Contains(opts, "v"):
		return z85Decode(s)
	case strings.Contains(opts, "x"):
		return percentDecode(s)
	case strings.Contains(opts, "y"):
		r, err := punyDecode(s)
		return []byte(r), err
	}
	return uuDecode(s)
}

// identCompress implements $compress/$decompress(file|&bvar, blNmN).
func (d *Data) identCompress(c *interp.Call, compress bool) (string, error) {
	opts := c.Arg(1)
	binvar := strings.Contains(opts, "b") || strings.HasPrefix(c.Arg(0), "&")
	var in []byte
	if binvar {
		in, _ = d.Bin(c.Arg(0))
	} else {
		b, err := os.ReadFile(d.path(c.Arg(0)))
		if err != nil {
			return "0", nil
		}
		in = b
	}
	level, method := 6, 2
	for i := 0; i < len(opts); i++ {
		if (opts[i] == 'l' || opts[i] == 'm') && i+1 < len(opts) {
			v := int(opts[i+1] - '0')
			if opts[i] == 'l' {
				level = v
			} else {
				method = v
			}
			i++
		}
	}
	var out []byte
	var err error
	if compress {
		out, err = deflate(in, method, level)
	} else {
		out, err = inflate(in)
	}
	if err != nil {
		return "0", nil
	}
	if binvar {
		d.setBin(c.Arg(0), out)
	} else if os.WriteFile(d.path(c.Arg(0)), out, 0o644) != nil {
		return "0", nil
	}
	return "1", nil
}

func deflate(in []byte, method, level int) ([]byte, error) {
	var buf bytes.Buffer
	var w io.WriteCloser
	var err error
	switch method {
	case 1:
		w, err = flate.NewWriter(&buf, level)
	case 3:
		w, err = gzip.NewWriterLevel(&buf, level)
	default:
		w, err = zlib.NewWriterLevel(&buf, level)
	}
	if err != nil {
		return nil, err
	}
	if _, err := w.Write(in); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// inflate detects zlib, gzip or raw deflate data.
func inflate(in []byte) ([]byte, error) {
	if r, err := zlib.NewReader(bytes.NewReader(in)); err == nil {
		if out, err := io.ReadAll(r); err == nil {
			return out, nil
		}
	}
	if r, err := gzip.NewReader(bytes.NewReader(in)); err == nil {
		if out, err := io.ReadAll(r); err == nil {
			return out, nil
		}
	}
	return io.ReadAll(flate.NewReader(bytes.NewReader(in)))
}

// identZip implements $zip(file.zip, cetlpo, file|dir, password, N).size|crc|mtime.
// Encrypted archives are not supported.
func (d *Data) identZip(c *interp.Call) (string, error) {
	zpath := d.path(c.Arg(0))
	opts := c.Arg(1)
	target := c.Arg(2)
	if strings.Contains(opts, "p") || c.Arg(3) != "" && !strings.Contains(opts, "l") {
		return "0", nil
	}
	switch {
	case strings.Contains(opts, "c"):
		return boolDigit(zipCreate(zpath, d.path(target), strings.Contains(opts, "o")) == nil), nil
	case strings.Contains(opts, "e"):
		return boolDigit(zipExtract(zpath, d.path(target), strings.Contains(opts, "o")) == nil), nil
	case strings.Contains(opts, "t"):
		r, err := zip.OpenReader(zpath)
		if err != nil {
			return "0", nil
		}
		defer r.Close()
		for _, f := range r.File {
			rc, err := f.Open()
			if err != nil {
				return "0", nil
			}
			_, err = io.Copy(io.Discard, rc)
			rc.Close()
			if err != nil {
				return "0", nil
			}
		}
		return "1", nil
	case strings.Contains(opts, "l"):
		r, err := zip.OpenReader(zpath)
		if err != nil {
			return "", nil
		}
		defer r.Close()
		n := interp.Int(c.Arg(4))
		if n == 0 {
			return strconv.Itoa(len(r.File)), nil
		}
		if n < 1 || n > len(r.File) {
			return "", nil
		}
		f := r.File[n-1]
		switch strings.ToLower(c.Prop) {
		case "size":
			return strconv.FormatUint(f.UncompressedSize64, 10), nil
		case "crc":
			return fmt.Sprintf("%08X", f.CRC32), nil
		case "mtime":
			return strconv.FormatInt(f.Modified.Unix(), 10), nil
		case "cm":
			return strconv.Itoa(int(f.Method)), nil
		case "idx":
			return strconv.Itoa(n), nil
		}
		return f.Name, nil
	}
	return "0", nil
}

func boolDigit(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

func zipCreate(zpath, src string, overwrite bool) error {
	if _, err := os.Stat(zpath); err == nil && !overwrite {
		return fmt.Errorf("exists")
	}
	out, err := os.Create(zpath)
	if err != nil {
		return err
	}
	defer out.Close()
	w := zip.NewWriter(out)
	base := filepath.Dir(src)
	err = filepath.Walk(src, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(base, p)
		fw, err := w.Create(filepath.ToSlash(rel))
		if err != nil {
			return err
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		_, err = fw.Write(data)
		return err
	})
	if err != nil {
		return err
	}
	return w.Close()
}

func zipExtract(zpath, dst string, overwrite bool) error {
	r, err := zip.OpenReader(zpath)
	if err != nil {
		return err
	}
	defer r.Close()
	for _, f := range r.File {
		p := filepath.Join(dst, filepath.FromSlash(f.Name))
		if !strings.HasPrefix(filepath.Clean(p), filepath.Clean(dst)) {
			return fmt.Errorf("unsafe path %s", f.Name)
		}
		if f.FileInfo().IsDir() {
			os.MkdirAll(p, 0o755)
			continue
		}
		if _, err := os.Stat(p); err == nil && !overwrite {
			continue
		}
		os.MkdirAll(filepath.Dir(p), 0o755)
		rc, err := f.Open()
		if err != nil {
			return err
		}
		data, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			return err
		}
		if err := os.WriteFile(p, data, 0o644); err != nil {
			return err
		}
	}
	return nil
}

// identPbkdf2 implements $pbkdf2(text|&binvar, salt|&binvar, hash, length, iterations, opts).
func (d *Data) identPbkdf2(c *interp.Call) (string, error) {
	bin := strings.Contains(c.Arg(5), "b")
	h := hashByName(c.Arg(2))
	if h == nil {
		return "", c.InvalidParams()
	}
	key, err := pbkdf2.Key(h, string(d.bytesArg(c.Arg(0), bin)), d.bytesArg(c.Arg(1), bin),
		max(interp.Int(c.Arg(4)), 1), max(interp.Int(c.Arg(3)), 1))
	if err != nil {
		return "", nil
	}
	return hex.EncodeToString(key), nil
}

// identArgon2 implements $argon2(text|&binvar, salt|&binvar, 2id|2i|2d,
// length, iterations, parallelism, memcost, opts). Argon2d is not available.
func (d *Data) identArgon2(c *interp.Call) (string, error) {
	bin := strings.Contains(c.Arg(7), "b")
	pass, salt := d.bytesArg(c.Arg(0), bin), d.bytesArg(c.Arg(1), bin)
	length := uint32(max(interp.Int(c.Arg(3)), 1))
	iters := uint32(max(interp.Int(c.Arg(4)), 1))
	par := uint8(max(interp.Int(c.Arg(5)), 1))
	mem := uint32(max(interp.Int(c.Arg(6)), 8))
	switch strings.ToLower(c.Arg(2)) {
	case "2id":
		return hex.EncodeToString(argon2.IDKey(pass, salt, iters, mem, par, length)), nil
	case "2i":
		return hex.EncodeToString(argon2.Key(pass, salt, iters, mem, par, length)), nil
	}
	return "", nil
}
