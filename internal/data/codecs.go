package data

import (
	"bytes"
	"crypto/cipher"
	"crypto/md5"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"golang.org/x/crypto/blowfish"
)

// uuEncode encodes data in uuencode lines of up to 45 bytes, without
// begin/end lines, as mIRC's $encode does.
func uuEncode(in []byte) string {
	var b strings.Builder
	for len(in) > 0 {
		n := min(45, len(in))
		line := in[:n]
		in = in[n:]
		b.WriteByte(byte(32 + n))
		for i := 0; i < len(line); i += 3 {
			var c [3]byte
			copy(c[:], line[i:])
			for _, v := range []byte{c[0] >> 2, (c[0]<<4 | c[1]>>4) & 63, (c[1]<<2 | c[2]>>6) & 63, c[2] & 63} {
				if v == 0 {
					b.WriteByte('`')
				} else {
					b.WriteByte(32 + v)
				}
			}
		}
	}
	return b.String()
}

func uuDecode(s string) ([]byte, error) {
	var out []byte
	for len(s) > 0 {
		n := int((s[0] - 32) & 63)
		s = s[1:]
		groups := (n + 2) / 3
		if len(s) < groups*4 {
			return nil, errors.New("short uuencode line")
		}
		var line []byte
		for g := 0; g < groups; g++ {
			var v [4]byte
			for k := range 4 {
				v[k] = (s[g*4+k] - 32) & 63
			}
			line = append(line, v[0]<<2|v[1]>>4, v[1]<<4|v[2]>>2, v[2]<<6|v[3])
		}
		out = append(out, line[:n]...)
		s = s[groups*4:]
	}
	return out, nil
}

const z85Chars = "0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ.-:+=^!/*?&<>()[]{}@%$#"

// z85Encode encodes data with Z85, padding to a multiple of four bytes.
func z85Encode(in []byte) string {
	for len(in)%4 != 0 {
		in = append(in, 0)
	}
	var b strings.Builder
	for i := 0; i < len(in); i += 4 {
		v := uint32(in[i])<<24 | uint32(in[i+1])<<16 | uint32(in[i+2])<<8 | uint32(in[i+3])
		var chunk [5]byte
		for k := 4; k >= 0; k-- {
			chunk[k] = z85Chars[v%85]
			v /= 85
		}
		b.Write(chunk[:])
	}
	return b.String()
}

func z85Decode(s string) ([]byte, error) {
	if len(s)%5 != 0 {
		return nil, errors.New("bad z85 length")
	}
	var out []byte
	for i := 0; i < len(s); i += 5 {
		var v uint32
		for k := range 5 {
			idx := strings.IndexByte(z85Chars, s[i+k])
			if idx < 0 {
				return nil, errors.New("bad z85 character")
			}
			v = v*85 + uint32(idx)
		}
		out = append(out, byte(v>>24), byte(v>>16), byte(v>>8), byte(v))
	}
	return bytes.TrimRight(out, "\x00"), nil
}

// percentEncode escapes everything except RFC 3986 unreserved characters.
func percentEncode(in []byte) string {
	var b strings.Builder
	for _, c := range in {
		if c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || strings.IndexByte("-._~", c) >= 0 {
			b.WriteByte(c)
			continue
		}
		fmt.Fprintf(&b, "%%%02X", c)
	}
	return b.String()
}

func percentDecode(s string) ([]byte, error) {
	var out []byte
	for i := 0; i < len(s); i++ {
		if s[i] == '%' && i+2 < len(s) {
			var v byte
			if _, err := fmt.Sscanf(s[i+1:i+3], "%02X", &v); err == nil {
				out = append(out, v)
				i += 2
				continue
			}
		}
		out = append(out, s[i])
	}
	return out, nil
}

// Punycode (RFC 3492) parameters.
const (
	pcBase = 36
	pcTMin = 1
	pcTMax = 26
	pcSkew = 38
	pcDamp = 700
	pcBias = 72
	pcN    = 128
)

func pcAdapt(delta, numPoints int, first bool) int {
	if first {
		delta /= pcDamp
	} else {
		delta /= 2
	}
	delta += delta / numPoints
	k := 0
	for delta > ((pcBase-pcTMin)*pcTMax)/2 {
		delta /= pcBase - pcTMin
		k += pcBase
	}
	return k + (pcBase-pcTMin+1)*delta/(delta+pcSkew)
}

func pcDigit(d int) byte {
	if d < 26 {
		return byte('a' + d)
	}
	return byte('0' + d - 26)
}

// punyEncode encodes a Unicode string with punycode (without the xn-- prefix).
func punyEncode(s string) string {
	runes := []rune(s)
	var out []byte
	for _, r := range runes {
		if r < 0x80 {
			out = append(out, byte(r))
		}
	}
	b := len(out)
	h := b
	if b > 0 {
		out = append(out, '-')
	}
	n, delta, bias := pcN, 0, pcBias
	for h < len(runes) {
		m := int(^uint(0) >> 1)
		for _, r := range runes {
			if int(r) >= n && int(r) < m {
				m = int(r)
			}
		}
		delta += (m - n) * (h + 1)
		n = m
		for _, r := range runes {
			if int(r) < n {
				delta++
			}
			if int(r) == n {
				q := delta
				for k := pcBase; ; k += pcBase {
					t := min(max(k-bias, pcTMin), pcTMax)
					if q < t {
						break
					}
					out = append(out, pcDigit(t+(q-t)%(pcBase-t)))
					q = (q - t) / (pcBase - t)
				}
				out = append(out, pcDigit(q))
				bias = pcAdapt(delta, h+1, h == b)
				delta = 0
				h++
			}
		}
		delta++
		n++
	}
	return string(out)
}

func punyDecode(s string) (string, error) {
	var out []rune
	if i := strings.LastIndexByte(s, '-'); i >= 0 {
		out = []rune(s[:i])
		s = s[i+1:]
	}
	n, i, bias := pcN, 0, pcBias
	for len(s) > 0 {
		oldi, w := i, 1
		for k := pcBase; ; k += pcBase {
			if len(s) == 0 {
				return "", errors.New("bad punycode")
			}
			c := s[0]
			s = s[1:]
			var digit int
			switch {
			case c >= 'a' && c <= 'z':
				digit = int(c - 'a')
			case c >= 'A' && c <= 'Z':
				digit = int(c - 'A')
			case c >= '0' && c <= '9':
				digit = int(c-'0') + 26
			default:
				return "", errors.New("bad punycode")
			}
			i += digit * w
			t := min(max(k-bias, pcTMin), pcTMax)
			if digit < t {
				break
			}
			w *= pcBase - t
		}
		bias = pcAdapt(i-oldi, len(out)+1, oldi == 0)
		n += i / (len(out) + 1)
		i %= len(out) + 1
		if !utf8.ValidRune(rune(n)) {
			return "", errors.New("bad punycode")
		}
		out = append(out[:i], append([]rune{rune(n)}, out[i:]...)...)
		i++
	}
	return string(out), nil
}

// blowfishCodec encrypts or decrypts with Blowfish. Switches: c (CBC) or e
// (ECB); l uses the key literally (otherwise it is derived with an OpenSSL
// compatible MD5 key derivation and a salt); s/i/r give a salt, IV or random
// IV; z/n/p choose zero, one-and-zeros or space padding (default PKCS#7).
// Encrypted output is base64 (MIME) text.
func blowfishCodec(in []byte, opts, key, saltIV string, encode bool) ([]byte, error) {
	if !encode {
		raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(in)))
		if err != nil {
			return nil, err
		}
		in = raw
	}
	var k, iv, salt []byte
	literal := strings.Contains(opts, "l")
	switch {
	case strings.Contains(opts, "i"):
		iv = padTo([]byte(saltIV), blowfish.BlockSize)
	case strings.Contains(opts, "s"):
		salt = padTo([]byte(saltIV), 8)
	}
	if literal {
		k = []byte(key)
	} else {
		if !encode && salt == nil && bytes.HasPrefix(in, []byte("Salted__")) && len(in) >= 16 {
			salt, in = in[8:16], in[16:]
		}
		if salt == nil && encode {
			salt = make([]byte, 8)
			rand.Read(salt)
		}
		var derivedIV []byte
		k, derivedIV = bytesToKey([]byte(key), salt, 16, blowfish.BlockSize)
		if iv == nil {
			iv = derivedIV
		}
	}
	if iv == nil {
		iv = make([]byte, blowfish.BlockSize)
		if strings.Contains(opts, "r") && encode {
			rand.Read(iv)
		}
	}
	if strings.Contains(opts, "r") && !encode && len(in) >= blowfish.BlockSize {
		iv, in = in[:blowfish.BlockSize], in[blowfish.BlockSize:]
	}
	block, err := blowfish.NewCipher(k)
	if err != nil {
		return nil, err
	}
	cbc := strings.Contains(opts, "c")
	if encode {
		data := pad(in, opts)
		out := make([]byte, len(data))
		if cbc {
			cipher.NewCBCEncrypter(block, iv).CryptBlocks(out, data)
		} else {
			for i := 0; i < len(data); i += blowfish.BlockSize {
				block.Encrypt(out[i:], data[i:])
			}
		}
		if strings.Contains(opts, "r") {
			out = append(append([]byte(nil), iv...), out...)
		}
		if !literal && !strings.Contains(opts, "s") {
			out = append(append([]byte("Salted__"), salt...), out...)
		}
		return []byte(base64.StdEncoding.EncodeToString(out)), nil
	}
	if len(in)%blowfish.BlockSize != 0 {
		return nil, errors.New("bad ciphertext length")
	}
	out := make([]byte, len(in))
	if cbc {
		cipher.NewCBCDecrypter(block, iv).CryptBlocks(out, in)
	} else {
		for i := 0; i < len(in); i += blowfish.BlockSize {
			block.Decrypt(out[i:], in[i:])
		}
	}
	return unpad(out, opts), nil
}

func padTo(b []byte, n int) []byte {
	out := make([]byte, n)
	copy(out, b)
	return out
}

// bytesToKey is OpenSSL's EVP_BytesToKey with MD5 and one iteration.
func bytesToKey(pass, salt []byte, keyLen, ivLen int) ([]byte, []byte) {
	var out, prev []byte
	for len(out) < keyLen+ivLen {
		h := md5.New()
		h.Write(prev)
		h.Write(pass)
		h.Write(salt)
		prev = h.Sum(nil)
		out = append(out, prev...)
	}
	return out[:keyLen], out[keyLen : keyLen+ivLen]
}

func pad(in []byte, opts string) []byte {
	n := blowfish.BlockSize - len(in)%blowfish.BlockSize
	switch {
	case strings.Contains(opts, "z"):
		if len(in)%blowfish.BlockSize == 0 {
			return in
		}
		return append(append([]byte(nil), in...), make([]byte, n)...)
	case strings.Contains(opts, "n"):
		return append(append(append([]byte(nil), in...), 0x80), make([]byte, n-1)...)
	case strings.Contains(opts, "p"):
		return append(append([]byte(nil), in...), bytes.Repeat([]byte(" "), n)...)
	}
	return append(append([]byte(nil), in...), bytes.Repeat([]byte{byte(n)}, n)...)
}

func unpad(out []byte, opts string) []byte {
	switch {
	case strings.Contains(opts, "z"):
		return bytes.TrimRight(out, "\x00")
	case strings.Contains(opts, "n"):
		out = bytes.TrimRight(out, "\x00")
		return bytes.TrimSuffix(out, []byte{0x80})
	case strings.Contains(opts, "p"):
		return bytes.TrimRight(out, " ")
	}
	if len(out) == 0 {
		return out
	}
	n := int(out[len(out)-1])
	if n == 0 || n > blowfish.BlockSize || n > len(out) {
		return out
	}
	return out[:len(out)-n]
}
