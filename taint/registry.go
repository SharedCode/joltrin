// Package taint is information-flow control plus tripwires for agent tool
// calls. Values you label (secret, pii, customer-data) are registered as keyed
// fingerprints. A tool call whose arguments contain a labeled value, in one of
// the supported encodings, is blocked unless its sink (egress host, file path
// or tool) is allowed for that label. Honeytokens are fake credentials planted
// in the agent's context: any appearance in a call is blocked and alerted.
//
// The registry never stores a raw value. Matching is exact on the supported
// encodings, so it does not catch paraphrase or transformations outside that
// list. See docs/SECURITY_CONTROLS.md for the limits.
package taint

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"math/bits"
	"regexp"
	"sort"
	"sync"
)

const (
	// MinValueLen is the shortest value (after Normalize) the registry accepts.
	// Shorter values collide with ordinary text too often to be useful.
	MinValueLen = 8
	// MaxValueLen is the longest value accepted.
	MaxValueLen = 4096
	// ChunkMinLen is the normalized length from which a value is also matched
	// in pieces of ChunkLen characters.
	ChunkMinLen = 24
	// ChunkLen is the piece size used for chunk matching.
	ChunkLen = 12
	// MaxArgBytes is the most argument text one call may carry. A larger call
	// is blocked as not inspectable rather than skipped.
	MaxArgBytes = 1 << 20
	// MaxFingerprints bounds the registry.
	MaxFingerprints = 500_000
)

var (
	ErrValueTooShort     = errors.New("taint: value is shorter than the minimum length")
	ErrValueTooLong      = errors.New("taint: value is longer than the maximum length")
	ErrBadLabel          = errors.New("taint: label must match [a-z][a-z0-9_-]{0,31}")
	ErrAlreadyRegistered = errors.New("taint: value is already registered")
	ErrRegistryFull      = errors.New("taint: registry is full")
)

var labelRE = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)

// HoneytokenLabel is the label given to planted honeytokens.
const HoneytokenLabel = "honeytoken"

// Match says which registered value appeared in a call and in which encoding.
// It never carries the value.
type Match struct {
	ValueID    string `json:"value_id"`
	Label      string `json:"label"`
	Variant    string `json:"variant"`
	Honeytoken bool   `json:"honeytoken,omitempty"`
}

type valueInfo struct {
	id    string
	label string
	honey bool
}

type fingerprint struct {
	mac     [sha256.Size]byte
	val     *valueInfo
	variant string
}

// Registry holds keyed fingerprints of labeled values. Each registry has its
// own random key, so fingerprints from one session cannot be matched against
// another. It is safe for concurrent use.
type Registry struct {
	mu     sync.RWMutex
	key    []byte
	base   uint64
	seq    int
	values map[string]*valueInfo
	byLen  map[int]map[uint64][]*fingerprint
	pow    map[int]uint64
	lens   []int
	count  int
}

// NewRegistry returns a registry with a fresh random key.
func NewRegistry() (*Registry, error) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("taint: cannot read random key: %w", err)
	}
	return NewRegistryWithKey(key)
}

// NewRegistryWithKey uses the given key (at least 16 bytes). Use it only when
// you need fingerprints to be reproducible, such as in tests.
func NewRegistryWithKey(key []byte) (*Registry, error) {
	if len(key) < 16 {
		return nil, errors.New("taint: key must be at least 16 bytes")
	}
	k := append([]byte(nil), key...)
	m := hmac.New(sha256.New, k)
	m.Write([]byte("rolling-base"))
	base := binary.BigEndian.Uint64(m.Sum(nil)[:8])%(rollMod-1000) + 500
	return &Registry{
		key:    k,
		base:   base,
		values: map[string]*valueInfo{},
		byLen:  map[int]map[uint64][]*fingerprint{},
		pow:    map[int]uint64{},
	}, nil
}

// Register fingerprints value under label. name is an optional handle used in
// findings and the audit log; it must not itself be sensitive. The value is
// not kept. It returns the handle.
func (r *Registry) Register(label, name, value string) (string, error) {
	return r.register(label, name, value, false)
}

func (r *Registry) register(label, name, value string, honey bool) (string, error) {
	if !labelRE.MatchString(label) {
		return "", ErrBadLabel
	}
	if len(value) > MaxValueLen {
		return "", ErrValueTooLong
	}
	if len(Normalize(value)) < MinValueLen {
		return "", ErrValueTooShort
	}
	ns := needles(value)
	if len(ns) == 0 {
		return "", ErrValueTooShort
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	full := r.macSum([]byte(ns[0].text))
	if _, dup := r.lookupMac(len(ns[0].text), r.rollHash([]byte(ns[0].text)), full); dup {
		return "", ErrAlreadyRegistered
	}
	if r.count+len(ns) > MaxFingerprints {
		return "", ErrRegistryFull
	}
	r.seq++
	if name == "" {
		name = fmt.Sprintf("v%d", r.seq)
	}
	if _, taken := r.values[name]; taken {
		name = fmt.Sprintf("%s#%d", name, r.seq)
	}
	info := &valueInfo{id: name, label: label, honey: honey}
	r.values[name] = info
	for _, n := range ns {
		t := []byte(n.text)
		l := len(t)
		h := r.rollHash(t)
		if _, dup := r.lookupMac(l, h, r.macSum(t)); dup {
			continue
		}
		bucket := r.byLen[l]
		if bucket == nil {
			bucket = map[uint64][]*fingerprint{}
			r.byLen[l] = bucket
			r.lens = append(r.lens, l)
			sort.Ints(r.lens)
			r.pow[l] = r.powOf(l - 1)
		}
		bucket[h] = append(bucket[h], &fingerprint{mac: r.macSum(t), val: info, variant: n.variant})
		r.count++
	}
	return name, nil
}

// Len reports how many fingerprints are held.
func (r *Registry) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.count
}

func (r *Registry) macSum(b []byte) [sha256.Size]byte {
	m := hmac.New(sha256.New, r.key)
	m.Write([]byte("fp\x00"))
	m.Write(b)
	var out [sha256.Size]byte
	copy(out[:], m.Sum(nil))
	return out
}

func (r *Registry) lookupMac(l int, h uint64, mac [sha256.Size]byte) (*fingerprint, bool) {
	for _, f := range r.byLen[l][h] {
		if hmac.Equal(f.mac[:], mac[:]) {
			return f, true
		}
	}
	return nil, false
}

// Keyed Rabin-Karp prefilter. The base is derived from the session key, so the
// rolling value is useless without it. Every candidate it finds is confirmed
// with HMAC-SHA256 before it counts.
const rollMod = (1 << 61) - 1

func mulmod(a, b uint64) uint64 {
	hi, lo := bits.Mul64(a, b)
	r := (lo & rollMod) + (lo >> 61) + (hi << 3)
	for r >= rollMod {
		r -= rollMod
	}
	return r
}

func (r *Registry) rollHash(b []byte) uint64 {
	var h uint64
	for _, c := range b {
		h = mulmod(h, r.base) + uint64(c) + 1
		if h >= rollMod {
			h -= rollMod
		}
	}
	return h
}

func (r *Registry) powOf(n int) uint64 {
	res := uint64(1)
	for i := 0; i < n; i++ {
		res = mulmod(res, r.base)
	}
	return res
}

// scan reports every registered value found in text, which must already be
// normalized. Each value is reported once, with the first variant that hit.
func (r *Registry) scan(text []byte) []Match {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.count == 0 || len(text) == 0 {
		return nil
	}
	var out []Match
	found := map[*valueInfo]bool{}
	for _, l := range r.lens {
		if l > len(text) {
			break
		}
		bucket := r.byLen[l]
		top := r.pow[l]
		h := r.rollHash(text[:l])
		for i := 0; ; i++ {
			if cands, ok := bucket[h]; ok {
				mac := r.macSum(text[i : i+l])
				for _, f := range cands {
					if !found[f.val] && hmac.Equal(f.mac[:], mac[:]) {
						found[f.val] = true
						out = append(out, Match{ValueID: f.val.id, Label: f.val.label, Variant: f.variant, Honeytoken: f.val.honey})
					}
				}
			}
			if i+l >= len(text) {
				break
			}
			drop := mulmod(uint64(text[i])+1, top)
			h = h + rollMod - drop
			if h >= rollMod {
				h -= rollMod
			}
			h = mulmod(h, r.base) + uint64(text[i+l]) + 1
			if h >= rollMod {
				h -= rollMod
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ValueID < out[j].ValueID })
	return out
}

// Scan reports the registered values found in text, after Normalize and with
// percent-decoding applied up to twice. It is what Check runs on each call.
func (r *Registry) Scan(text string) []Match {
	seen := map[string]bool{}
	var out []Match
	cur := text
	for pass := 0; pass < 3; pass++ {
		for _, m := range r.scan([]byte(Normalize(cur))) {
			if !seen[m.ValueID] {
				seen[m.ValueID] = true
				out = append(out, m)
			}
		}
		next := percentDecode(cur)
		if next == cur {
			break
		}
		cur = next
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ValueID < out[j].ValueID })
	return out
}

// ScanViews is Scan over several texts, with each value reported once.
func (r *Registry) ScanViews(texts ...string) []Match {
	seen := map[string]bool{}
	var out []Match
	for _, t := range texts {
		for _, m := range r.Scan(t) {
			if !seen[m.ValueID] {
				seen[m.ValueID] = true
				out = append(out, m)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ValueID < out[j].ValueID })
	return out
}
