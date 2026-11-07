package governance

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strings"
)

// PIIRedactor masks common personal and secret values in free text before it is stored.
// It is pattern based, so it catches well-formed values and will miss anything it has no
// pattern for. Treat it as a safety net, not a guarantee.
type PIIRedactor struct {
	// Key, when set, replaces each match with a keyed pseudonym such as [email:3fa9c1d2]
	// so the same value can be correlated across events without being stored.
	// When empty, matches become a fixed label such as [email].
	Key []byte
}

type piiRule struct {
	label string
	re    *regexp.Regexp
	ok    func(string) bool
}

var piiRules = []piiRule{
	{"aws_key", regexp.MustCompile(`\b(?:AKIA|ASIA)[0-9A-Z]{16}\b`), nil},
	{"jwt", regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\b`), nil},
	{"email", regexp.MustCompile(`[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}`), nil},
	{"ssn", regexp.MustCompile(`\b\d{3}-\d{2}-\d{4}\b`), validSSN},
	{"card", regexp.MustCompile(`\b\d(?:[ -]?\d){12,18}\b`), luhnValid},
	{"phone", regexp.MustCompile(`(?:\+\d{1,3}[ .-]?)?\(?\b\d{3}\)?[ .-]\d{3}[ .-]\d{4}\b`), nil},
	{"ipv4", regexp.MustCompile(`\b(?:25[0-5]|2[0-4]\d|1?\d?\d)(?:\.(?:25[0-5]|2[0-4]\d|1?\d?\d)){3}\b`), nil},
}

func validSSN(s string) bool {
	return !strings.HasPrefix(s, "000") && !strings.HasPrefix(s, "666") && !strings.HasPrefix(s, "9") &&
		s[4:6] != "00" && s[7:] != "0000"
}

func luhnValid(s string) bool {
	sum, alt, n := 0, false, 0
	for i := len(s) - 1; i >= 0; i-- {
		c := s[i]
		if c == ' ' || c == '-' {
			continue
		}
		d := int(c - '0')
		if alt {
			if d *= 2; d > 9 {
				d -= 9
			}
		}
		sum += d
		alt = !alt
		n++
	}
	return n >= 13 && n <= 19 && sum%10 == 0
}

// Redact returns s with every recognised value replaced.
func (p PIIRedactor) Redact(s string) string {
	for _, rule := range piiRules {
		rule := rule
		s = rule.re.ReplaceAllStringFunc(s, func(m string) string {
			if rule.ok != nil && !rule.ok(m) {
				return m
			}
			if len(p.Key) == 0 {
				return "[" + rule.label + "]"
			}
			mac := hmac.New(sha256.New, p.Key)
			mac.Write([]byte(rule.label + ":" + m))
			return "[" + rule.label + ":" + hex.EncodeToString(mac.Sum(nil))[:8] + "]"
		})
	}
	return s
}

// RedactEvent masks Reason, Resource and every Metadata value in place.
// Identity fields such as ActorID are left alone so the log still answers who did what.
func (p PIIRedactor) RedactEvent(ev *AuditEvent) {
	ev.Reason = p.Redact(ev.Reason)
	ev.Resource = p.Redact(ev.Resource)
	for k, v := range ev.Metadata {
		ev.Metadata[k] = p.Redact(v)
	}
}

// RedactingSink masks PII before an event reaches the wrapped sink, so the hash chain and
// signature cover the redacted text and the original never lands in the log.
type RedactingSink struct {
	Next     AuditSink
	Redactor PIIRedactor
}

func (s *RedactingSink) Record(ctx context.Context, ev *AuditEvent) error {
	cp := *ev
	if ev.Metadata != nil {
		cp.Metadata = make(map[string]string, len(ev.Metadata))
		for k, v := range ev.Metadata {
			cp.Metadata[k] = v
		}
	}
	s.Redactor.RedactEvent(&cp)
	err := s.Next.Record(ctx, &cp)
	ev.ID, ev.Timestamp, ev.PrevHash, ev.Hash, ev.Signature = cp.ID, cp.Timestamp, cp.PrevHash, cp.Hash, cp.Signature
	return err
}
func (s *RedactingSink) Flush(ctx context.Context) error { return s.Next.Flush(ctx) }
func (s *RedactingSink) Close() error                    { return s.Next.Close() }
