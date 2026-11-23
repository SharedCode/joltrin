package taint

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"path"
	"strings"
)

// Mode says whether a decision to block is enforced.
type Mode string

const (
	ModeEnforce Mode = "enforce"
	// ModeObserve records what would have been blocked and lets the call
	// through. Honeytoken trips still raise an alert.
	ModeObserve Mode = "observe"
)

// LabelPolicy lists where data with one label may go. Anything not listed is
// denied. A call that names a destination is judged by the destination alone.
// A call with no destination is judged by its tool name.
type LabelPolicy struct {
	// AllowHosts are egress hosts: "api.example.com", "api.example.com:443" or
	// "*.example.com" (any subdomain, not the bare domain). A host without a
	// port matches any port.
	AllowHosts []string `json:"allow_hosts,omitempty"`
	// AllowTools are tools that may receive the label when the call has no
	// destination. List only tools that keep the data local.
	AllowTools []string `json:"allow_tools,omitempty"`
	// AllowPaths are absolute file paths. An entry ending in "/" allows
	// everything under that directory, any other entry allows that one file.
	AllowPaths []string `json:"allow_paths,omitempty"`
}

// Policy is the per-label allowlist, written as JSON. Version is 1.
type Policy struct {
	Version int                    `json:"version"`
	Mode    Mode                   `json:"mode,omitempty"`
	Labels  map[string]LabelPolicy `json:"labels"`
}

// ParsePolicy reads and validates a JSON policy. Unknown fields and a second
// JSON document are errors, so a typo cannot silently widen or drop a rule.
func ParsePolicy(data []byte) (*Policy, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var p Policy
	if err := dec.Decode(&p); err != nil {
		return nil, fmt.Errorf("taint: policy: %w", err)
	}
	if dec.More() {
		return nil, errors.New("taint: policy: more than one JSON document")
	}
	if err := p.Validate(); err != nil {
		return nil, err
	}
	return &p, nil
}

// Validate checks the policy.
func (p *Policy) Validate() error {
	if p.Version != 1 {
		return fmt.Errorf("taint: policy version must be 1, got %d", p.Version)
	}
	switch p.Mode {
	case "", ModeEnforce, ModeObserve:
	default:
		return fmt.Errorf("taint: policy mode must be enforce or observe, got %q", p.Mode)
	}
	for label, lp := range p.Labels {
		if !labelRE.MatchString(label) {
			return fmt.Errorf("taint: policy label %q: %w", label, ErrBadLabel)
		}
		for _, h := range lp.AllowHosts {
			if err := validHostEntry(h); err != nil {
				return fmt.Errorf("taint: policy label %q: allow_hosts %q: %w", label, h, err)
			}
		}
		for _, t := range lp.AllowTools {
			if strings.TrimSpace(t) == "" || t == "*" {
				return fmt.Errorf("taint: policy label %q: allow_tools entries must be tool names", label)
			}
		}
		for _, pa := range lp.AllowPaths {
			if !strings.HasPrefix(pa, "/") || strings.Contains(pa, "..") || strings.Contains(pa, "*") {
				return fmt.Errorf("taint: policy label %q: allow_paths %q must be absolute with no \"..\" or wildcard", label, pa)
			}
		}
	}
	return nil
}

func validHostEntry(h string) error {
	if h == "" || h == "*" || strings.ContainsAny(h, "/@ \t?#") || strings.Contains(h, "://") {
		return errors.New("must be a host or host:port")
	}
	host := strings.TrimPrefix(h, "*.")
	if strings.Contains(host, "*") || host == "" {
		return errors.New("wildcard is only allowed as a leading \"*.\"")
	}
	return nil
}

// sink describes where a call sends its data.
type sink struct {
	kind  string // "host", "path", "tool", "invalid"
	host  string
	port  string
	path  string
	tool  string
	label string // safe text for logs
}

func classify(tool, dest string) sink {
	d := strings.TrimSpace(dest)
	switch {
	case d == "":
		return sink{kind: "tool", tool: tool, label: "tool:" + tool}
	case strings.Contains(d, "://"):
		u, err := url.Parse(d)
		if err != nil || u.Hostname() == "" && u.Scheme != "file" {
			return sink{kind: "invalid", label: "invalid"}
		}
		if u.Scheme == "file" {
			return pathSink(u.Path)
		}
		return hostSink(u.Hostname(), u.Port())
	case strings.HasPrefix(d, "/"), strings.HasPrefix(d, "./"), strings.HasPrefix(d, "../"), strings.HasPrefix(d, "~"):
		return pathSink(d)
	default:
		if strings.ContainsAny(d, "/?#@ ") {
			return sink{kind: "invalid", label: "invalid"}
		}
		host, port, err := net.SplitHostPort(d)
		if err != nil {
			host, port = d, ""
		}
		return hostSink(host, port)
	}
}

func hostSink(host, port string) sink {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	if host == "" {
		return sink{kind: "invalid", label: "invalid"}
	}
	l := "host:" + host
	if port != "" {
		l += ":" + port
	}
	return sink{kind: "host", host: host, port: port, label: l}
}

func pathSink(p string) sink {
	if !strings.HasPrefix(p, "/") {
		// Relative and home-relative paths cannot be matched to an allow entry.
		return sink{kind: "path", path: "", label: "path:relative"}
	}
	c := path.Clean(p)
	return sink{kind: "path", path: c, label: "path:" + c}
}

// allows reports whether label may flow to the sink.
func (p *Policy) allows(label string, s sink) bool {
	lp, ok := p.Labels[label]
	if !ok {
		return false
	}
	switch s.kind {
	case "host":
		for _, e := range lp.AllowHosts {
			if hostMatches(e, s.host, s.port) {
				return true
			}
		}
	case "path":
		if s.path == "" {
			return false
		}
		for _, e := range lp.AllowPaths {
			if strings.HasSuffix(e, "/") {
				if strings.HasPrefix(s.path+"/", path.Clean(e)+"/") && s.path != path.Clean(e) {
					return true
				}
			} else if s.path == path.Clean(e) {
				return true
			}
		}
	case "tool":
		for _, t := range lp.AllowTools {
			if t == s.tool {
				return true
			}
		}
	}
	return false
}

func hostMatches(entry, host, port string) bool {
	entry = strings.ToLower(entry)
	ehost, eport := entry, ""
	if h, p, err := net.SplitHostPort(entry); err == nil {
		ehost, eport = h, p
	}
	if eport != "" && eport != port {
		return false
	}
	if strings.HasPrefix(ehost, "*.") {
		return strings.HasSuffix(host, ehost[1:]) && len(host) > len(ehost)-1
	}
	return host == ehost
}
