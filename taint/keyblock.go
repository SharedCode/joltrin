package taint

import (
	"encoding/base64"
	"regexp"
	"strings"
	"unicode"
)

// LabelPrivateKey is the label given to key material found by structure
// rather than by registration: armored PGP private key blocks, PEM and
// OpenSSH private keys. A policy must list this label to let such text reach a
// sink. A policy that does not list it blocks it everywhere.
const LabelPrivateKey = "private_key"

// keyHeaderRE runs on Normalize output, so spacing, line wrapping, case and
// zero-width characters in the header cannot hide it.
var keyHeaderRE = regexp.MustCompile(`-----begin(?:pgp|rsa|ec|dsa|openssh|encrypted)?privatekey(?:block)?-----`)

var keyHeaders = []string{
	"-----BEGIN PGP PRIVATE KEY BLOCK-----",
	"-----BEGIN PRIVATE KEY-----",
	"-----BEGIN RSA PRIVATE KEY-----",
	"-----BEGIN EC PRIVATE KEY-----",
	"-----BEGIN DSA PRIVATE KEY-----",
	"-----BEGIN OPENSSH PRIVATE KEY-----",
	"-----BEGIN ENCRYPTED PRIVATE KEY-----",
}

// keyHeaderB64 holds the interior of each header encoded in base64 (standard
// and URL alphabets) at all three byte alignments, so the edge characters that
// depend on neighboring bytes are left out.
var keyHeaderB64 = buildKeyHeaderB64()

func buildKeyHeaderB64() []string {
	var out []string
	for _, h := range keyHeaders {
		for off := 0; off < 3; off++ {
			b := []byte(strings.Repeat("x", off) + h)
			for _, enc := range []*base64.Encoding{base64.RawStdEncoding, base64.RawURLEncoding} {
				s := enc.EncodeToString(b)
				// Skip the first group (it mixes in the filler bytes) and the
				// last group (it depends on what follows).
				if len(s) > 8 {
					out = append(out, s[4:len(s)-4])
				}
			}
		}
	}
	return out
}

// ScanKeyMaterial reports private key blocks in text. The match carries no
// key data: ValueID names the kind of block that was found.
func ScanKeyMaterial(text string) []Match {
	found := map[string]bool{}
	cur := text
	for pass := 0; pass < 3; pass++ {
		for _, h := range keyHeaderRE.FindAllString(Normalize(strings.ReplaceAll(cur, "+", " ")), -1) {
			found[keyKind(h)] = true
		}
		noWS := strings.Map(func(r rune) rune {
			if unicode.IsSpace(r) {
				return -1
			}
			return r
		}, cur)
		for _, n := range keyHeaderB64 {
			if strings.Contains(noWS, n) {
				found["encoded"] = true
				break
			}
		}
		next := percentDecode(cur)
		if next == cur {
			break
		}
		cur = next
	}
	var out []Match
	for _, k := range []string{"pgp", "pem", "openssh", "encoded"} {
		if found[k] {
			out = append(out, Match{ValueID: "keyblock:" + k, Label: LabelPrivateKey, Variant: "header"})
		}
	}
	return out
}

func keyKind(h string) string {
	switch {
	case strings.Contains(h, "pgp"):
		return "pgp"
	case strings.Contains(h, "openssh"):
		return "openssh"
	default:
		return "pem"
	}
}
