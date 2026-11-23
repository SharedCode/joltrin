package taint

import (
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"hash/crc32"
	"math/big"
)

// Honeytoken kinds.
const (
	KindAWSAccessKeyID     = "aws_access_key_id"
	KindAWSSecretAccessKey = "aws_secret_access_key"
	KindGitHubToken        = "github_token"
	KindGenericAPIKey      = "generic_api_key"
)

// Kinds lists the supported honeytoken kinds.
var Kinds = []string{KindAWSAccessKeyID, KindAWSSecretAccessKey, KindGitHubToken, KindGenericAPIKey}

const (
	alphaB32   = "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567"
	alphaB62   = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
	alphaB64   = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
	hnyContext = "joltrin-honeytoken-v1:"
)

var ErrUnknownKind = errors.New("taint: unknown honeytoken kind")

// GenerateHoneytoken returns a random credential-shaped string of the given
// kind. It carries a checksum marker so IsHoneytoken can recognise it without
// a key.
//
// It cannot authenticate: it is random and was never issued by any provider.
// The AWS and generic forms rely on that alone. The GitHub form also has a
// deliberately wrong CRC32 suffix, so GitHub's own offline checksum rejects
// it. Whether a random AWS-shaped ID could collide with a real one is a
// probability argument (about 2^-60 for the random part), not a guarantee
// from AWS.
func GenerateHoneytoken(kind string) (string, error) {
	switch kind {
	case KindAWSAccessKeyID:
		body, err := randString(alphaB32, 12)
		if err != nil {
			return "", err
		}
		return "AKIA" + body + checkChars(alphaB32, kind+"AKIA"+body, 4), nil
	case KindAWSSecretAccessKey:
		body, err := randString(alphaB64, 36)
		if err != nil {
			return "", err
		}
		return body + checkChars(alphaB64, kind+body, 4), nil
	case KindGitHubToken:
		body, err := randString(alphaB62, 30)
		if err != nil {
			return "", err
		}
		return "ghp_" + body + githubFakeSuffix(body), nil
	case KindGenericAPIKey:
		body, err := randString(alphaB62, 28)
		if err != nil {
			return "", err
		}
		return "jlt_" + body + checkChars(alphaB62, kind+"jlt_"+body, 4), nil
	}
	return "", ErrUnknownKind
}

// IsHoneytoken reports whether s has the shape and checksum marker of a token
// made by GenerateHoneytoken, and which kind. It needs no key. A false result
// does not mean s is real, only that it does not carry the marker.
func IsHoneytoken(s string) (kind string, ok bool) {
	switch {
	case len(s) == 20 && s[:4] == "AKIA" && onlyFrom(s[4:], alphaB32):
		return KindAWSAccessKeyID, s[16:] == checkChars(alphaB32, KindAWSAccessKeyID+s[:16], 4)
	case len(s) == 40 && onlyFrom(s, alphaB64):
		return KindAWSSecretAccessKey, s[36:] == checkChars(alphaB64, KindAWSSecretAccessKey+s[:36], 4)
	case len(s) == 40 && s[:4] == "ghp_" && onlyFrom(s[4:], alphaB62):
		return KindGitHubToken, s[34:] == githubFakeSuffix(s[4:34])
	case len(s) == 36 && s[:4] == "jlt_" && onlyFrom(s[4:], alphaB62):
		return KindGenericAPIKey, s[32:] == checkChars(alphaB62, KindGenericAPIKey+s[:32], 4)
	}
	return "", false
}

// PlantHoneytoken generates a honeytoken of kind and registers it. Put the
// returned token where the agent can read it, such as a fake .env entry in its
// context. The second result is the handle used in findings.
func (r *Registry) PlantHoneytoken(kind, name string) (token, id string, err error) {
	token, err = GenerateHoneytoken(kind)
	if err != nil {
		return "", "", err
	}
	id, err = r.register(HoneytokenLabel, name, token, true)
	if err != nil {
		return "", "", err
	}
	return token, id, nil
}

// RegisterHoneytoken registers a honeytoken you already planted, such as one
// made earlier with GenerateHoneytoken. Matches on it are always blocked.
func (r *Registry) RegisterHoneytoken(name, token string) (string, error) {
	return r.register(HoneytokenLabel, name, token, true)
}

func onlyFrom(s, alphabet string) bool {
	for i := 0; i < len(s); i++ {
		found := false
		for j := 0; j < len(alphabet); j++ {
			if s[i] == alphabet[j] {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func randString(alphabet string, n int) (string, error) {
	out := make([]byte, n)
	max := big.NewInt(int64(len(alphabet)))
	for i := range out {
		v, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", fmt.Errorf("taint: cannot read random bytes: %w", err)
		}
		out[i] = alphabet[v.Int64()]
	}
	return string(out), nil
}

func checkChars(alphabet, data string, n int) string {
	sum := sha256.Sum256([]byte(hnyContext + data))
	out := make([]byte, n)
	for i := range out {
		out[i] = alphabet[int(sum[i])%len(alphabet)]
	}
	return string(out)
}

// githubFakeSuffix is the six-character base62 CRC32 that real GitHub tokens
// end with, XORed with a constant so it is always wrong for that body.
func githubFakeSuffix(body string) string {
	v := crc32.ChecksumIEEE([]byte(body)) ^ 0x5A5A5A5A
	out := make([]byte, 6)
	for i := 5; i >= 0; i-- {
		out[i] = alphaB62[v%62]
		v /= 62
	}
	return string(out)
}
