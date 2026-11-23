package taint

import (
	"encoding/base32"
	"encoding/base64"
	"encoding/hex"
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Normalize is the one canonical form used on both sides of a match. It drops
// whitespace and invisible format characters (zero-width spaces, joiners, byte
// order marks, soft hyphens) and lower-cases the rest, so spacing, line
// wrapping and case changes cannot hide a value. Invalid UTF-8 bytes are kept
// as they are. Normalize is idempotent: dropping a character can join stray
// bytes into a new valid character, so it repeats until nothing changes.
func Normalize(s string) string {
	for i := 0; i < 16; i++ {
		n := normalizeOnce(s)
		if n == s {
			return n
		}
		s = n
	}
	return s
}

func normalizeOnce(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size == 1 {
			b.WriteByte(s[i])
			i++
			continue
		}
		i += size
		if unicode.IsSpace(r) || unicode.Is(unicode.Cf, r) {
			continue
		}
		lr := unicode.ToLower(r)
		if lr == utf8.RuneError {
			lr = r
		}
		b.WriteRune(lr)
	}
	return b.String()
}

// needle is one normalized string whose appearance in a call means the value
// appeared in it, in some encoding.
type needle struct {
	variant string
	text    string
}

// needles returns every form of raw that the barrier looks for: the value
// itself, reversed, hex, percent-encoded, base64 (both alphabets, all three
// byte alignments) and base32 (all five alignments). Forms of ChunkMinLen
// characters or more also yield ChunkLen-character windows, so a value sent in
// pieces still trips the check when a piece is that long.
func needles(raw string) []needle {
	seen := map[string]bool{}
	var out []needle
	add := func(variant, text string) {
		text = Normalize(text)
		if len(text) < MinValueLen || seen[text] {
			return
		}
		seen[text] = true
		out = append(out, needle{variant, text})
		if len(text) >= ChunkMinLen {
			for i := 0; i+ChunkLen <= len(text); i++ {
				w := text[i : i+ChunkLen]
				if seen[w] {
					continue
				}
				seen[w] = true
				out = append(out, needle{variant + "+chunk", w})
			}
		}
	}

	add("plain", raw)
	add("reversed", reverseRunes(raw))
	b := []byte(raw)
	add("hex", hex.EncodeToString(b))
	add("url", url.QueryEscape(raw))
	add("url", url.PathEscape(raw))
	add("url", percentAll(b))
	for _, enc := range []struct {
		name string
		e    *base64.Encoding
	}{{"base64", base64.StdEncoding.WithPadding(base64.NoPadding)}, {"base64url", base64.URLEncoding.WithPadding(base64.NoPadding)}} {
		for k := 0; k < 3; k++ {
			add(enc.name, stable(b, k, 3, 6, enc.e.EncodeToString))
		}
	}
	b32 := base32.StdEncoding.WithPadding(base32.NoPadding)
	for k := 0; k < 5; k++ {
		add("base32", stable(b, k, 5, 5, b32.EncodeToString))
	}
	return out
}

// stable encodes b as it would appear in the middle of a longer encoded
// stream, where k bytes precede it. group is the bytes per encoding group and
// bits the bits per output character. Characters that also depend on the
// neighbouring bytes are dropped, so what is returned appears verbatim in any
// stream that contains b at that alignment.
func stable(b []byte, k, group, bits int, encode func([]byte) string) string {
	buf := make([]byte, 0, k+len(b))
	for i := 0; i < k; i++ {
		buf = append(buf, 'A')
	}
	buf = append(buf, b...)
	s := encode(buf)
	start := (k*8 + bits - 1) / bits
	rem := len(buf) % group
	end := len(buf)/group*(group*8/bits) + rem*8/bits
	if start >= end || end > len(s) {
		return ""
	}
	return s[start:end]
}

func percentAll(b []byte) string {
	const digits = "0123456789abcdef"
	out := make([]byte, 0, len(b)*3)
	for _, c := range b {
		out = append(out, '%', digits[c>>4], digits[c&15])
	}
	return string(out)
}

func reverseRunes(s string) string {
	r := []rune(s)
	for i, j := 0, len(r)-1; i < j; i, j = i+1, j-1 {
		r[i], r[j] = r[j], r[i]
	}
	return string(r)
}

// percentDecode decodes every valid %XX and leaves anything else alone. The
// standard library decoder fails on one bad escape, which would let a caller
// switch off decoding by adding "%zz".
func percentDecode(s string) string {
	if !strings.Contains(s, "%") {
		return s
	}
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == '%' && i+2 < len(s) {
			if hi, ok := unhex(s[i+1]); ok {
				if lo, ok := unhex(s[i+2]); ok {
					out = append(out, hi<<4|lo)
					i += 2
					continue
				}
			}
		}
		out = append(out, s[i])
	}
	return string(out)
}

func unhex(c byte) (byte, bool) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', true
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, true
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, true
	}
	return 0, false
}
