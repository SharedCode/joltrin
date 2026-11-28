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

// Built from parts so the source holds no key-shaped text for secret scanners.
var keyHeaders = func() []string {
	var out []string
	for _, k := range []string{"PGP PRIVATE KEY BLOCK", "PRIVATE KEY", "RSA PRIVATE KEY", "EC PRIVATE KEY", "DSA PRIVATE KEY", "OPENSSH PRIVATE KEY", "ENCRYPTED PRIVATE KEY"} {
		out = append(out, "-----BEGIN "+k+"-----")
	}
	return out
}()

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

// opensshMagic starts the binary body of every OpenSSH private key file, so the
// base64 body can be found even when the BEGIN line was left off. Built from
// parts like the headers above.
var opensshMagic = "openssh-key-" + "v1\x00"

// keyBodyB64 is opensshMagic in base64 (both alphabets, all three alignments),
// edges trimmed the same way as keyHeaderB64.
var keyBodyB64 = func() []string {
	var out []string
	for off := 0; off < 3; off++ {
		b := []byte(strings.Repeat("x", off) + opensshMagic)
		for _, enc := range []*base64.Encoding{base64.RawStdEncoding, base64.RawURLEncoding} {
			s := enc.EncodeToString(b)
			if len(s) > 8 {
				out = append(out, s[4:len(s)-4])
			}
		}
	}
	return out
}()

// ScanKeyMaterial reports private key blocks in text. The match carries no
// key data: ValueID names the kind of block that was found.
func ScanKeyMaterial(text string) []Match {
	found := map[string]bool{}
	cur := text
	for pass := 0; pass < 3; pass++ {
		// Form-encoded text writes a space as "+", which percentDecode leaves
		// alone, so turn it into a space first. The "percent" case in the
		// test fails without this line.
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
		for _, n := range keyBodyB64 {
			if strings.Contains(noWS, n) {
				found["openssh-body"] = true
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
	for _, k := range []string{"pgp", "pem", "openssh", "encoded", "openssh-body"} {
		if found[k] {
			variant := "header"
			if k == "openssh-body" {
				variant = "body"
			}
			out = append(out, Match{ValueID: "keyblock:" + k, Label: LabelPrivateKey, Variant: variant})
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
