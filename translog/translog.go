// Package translog is an append-only Merkle log for decision receipts, with
// signed tree heads, inclusion proofs and consistency proofs. It follows the
// hashing and proof construction of RFC 6962 and RFC 9162, the same shape as
// Certificate Transparency and Sigstore Rekor.
//
// What it gives an auditor: a receipt that was appended is provably in the
// log at a given size (inclusion), and a later log of a larger size extends an
// earlier one without rewriting it (consistency). Both checks run offline from
// a signed tree head and a short proof.
//
// What it does not give: the log cannot stop its operator from showing two
// different histories to two parties (a split view). Detecting that needs
// independent monitors that compare signed tree heads, which this package does
// not run. It also says nothing about whether an action ran.
package translog

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"sync"
)

// HashSize is the size of every node hash.
const HashSize = sha256.Size

// Hash is a SHA-256 node hash.
type Hash [HashSize]byte

var (
	// ErrIndexRange is returned when a leaf index or tree size is out of range.
	ErrIndexRange = errors.New("translog: index or size out of range")
	// ErrProof is returned when a proof does not verify.
	ErrProof = errors.New("translog: proof does not verify")
)

// LeafHash is SHA-256(0x00 || data). The prefix keeps a leaf from ever being
// read as an interior node.
func LeafHash(data []byte) Hash {
	h := sha256.New()
	h.Write([]byte{0x00})
	h.Write(data)
	var out Hash
	h.Sum(out[:0])
	return out
}

func nodeHash(l, r Hash) Hash {
	h := sha256.New()
	h.Write([]byte{0x01})
	h.Write(l[:])
	h.Write(r[:])
	var out Hash
	h.Sum(out[:0])
	return out
}

// emptyRoot is the root of a log with no entries: SHA-256 of the empty string.
func emptyRoot() Hash { return sha256.Sum256(nil) }

// Log is an in-memory append-only Merkle log. It is safe for concurrent use.
// Entries are kept so the log can be persisted and replayed by a Store.
type Log struct {
	mu     sync.RWMutex
	leaves []Hash
}

// New returns an empty log.
func New() *Log { return &Log{} }

// Size is the number of entries.
func (l *Log) Size() uint64 {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return uint64(len(l.leaves))
}

// Append adds one entry and returns its index. The log stores only its hash.
func (l *Log) Append(data []byte) uint64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.leaves = append(l.leaves, LeafHash(data))
	return uint64(len(l.leaves) - 1)
}

// Root returns the root hash of the first size entries.
func (l *Log) Root(size uint64) (Hash, error) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	if size > uint64(len(l.leaves)) {
		return Hash{}, ErrIndexRange
	}
	return rootOf(l.leaves[:size]), nil
}

// largestPow2Below returns the largest power of two strictly less than n, n >= 2.
func largestPow2Below(n uint64) uint64 {
	k := uint64(1)
	for k<<1 < n {
		k <<= 1
	}
	return k
}

func rootOf(leaves []Hash) Hash {
	switch len(leaves) {
	case 0:
		return emptyRoot()
	case 1:
		return leaves[0]
	}
	k := largestPow2Below(uint64(len(leaves)))
	return nodeHash(rootOf(leaves[:k]), rootOf(leaves[k:]))
}

// InclusionProof returns the audit path showing that entry index is in the
// log of the given size.
func (l *Log) InclusionProof(index, size uint64) ([]Hash, error) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	if size > uint64(len(l.leaves)) || index >= size {
		return nil, ErrIndexRange
	}
	return inclusionPath(index, l.leaves[:size]), nil
}

func inclusionPath(m uint64, leaves []Hash) []Hash {
	n := uint64(len(leaves))
	if n <= 1 {
		return nil
	}
	k := largestPow2Below(n)
	if m < k {
		return append(inclusionPath(m, leaves[:k]), rootOf(leaves[k:]))
	}
	return append(inclusionPath(m-k, leaves[k:]), rootOf(leaves[:k]))
}

// ConsistencyProof returns the proof that the log of size oldSize is a prefix
// of the log of size newSize.
func (l *Log) ConsistencyProof(oldSize, newSize uint64) ([]Hash, error) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	if newSize > uint64(len(l.leaves)) || oldSize > newSize {
		return nil, ErrIndexRange
	}
	if oldSize == 0 || oldSize == newSize {
		return nil, nil
	}
	return subProof(oldSize, l.leaves[:newSize], true), nil
}

func subProof(m uint64, leaves []Hash, complete bool) []Hash {
	n := uint64(len(leaves))
	if m == n {
		if complete {
			return nil
		}
		return []Hash{rootOf(leaves)}
	}
	k := largestPow2Below(n)
	if m <= k {
		return append(subProof(m, leaves[:k], complete), rootOf(leaves[k:]))
	}
	return append(subProof(m-k, leaves[k:], false), rootOf(leaves[:k]))
}

// VerifyInclusion checks that leaf is entry index of the log of the given size
// whose root is root, using the audit path. Size only fixes the shape of the
// path, so take size and root together from a signed TreeHead: two sizes can
// share a path shape for the same leaf, and only the head binds size to root.
func VerifyInclusion(leaf Hash, index, size uint64, path []Hash, root Hash) error {
	if index >= size {
		return ErrIndexRange
	}
	fn, sn := index, size-1
	r := leaf
	for _, p := range path {
		if sn == 0 {
			return ErrProof
		}
		if fn&1 == 1 || fn == sn {
			r = nodeHash(p, r)
			if fn&1 == 0 {
				for fn&1 == 0 && fn != 0 {
					fn >>= 1
					sn >>= 1
				}
			}
		} else {
			r = nodeHash(r, p)
		}
		fn >>= 1
		sn >>= 1
	}
	if sn != 0 || r != root {
		return ErrProof
	}
	return nil
}

// VerifyConsistency checks that the log of newSize with root newRoot extends
// the log of oldSize with root oldRoot, using the consistency proof.
func VerifyConsistency(oldSize, newSize uint64, oldRoot, newRoot Hash, proof []Hash) error {
	switch {
	case oldSize > newSize:
		return ErrIndexRange
	case oldSize == newSize:
		if len(proof) != 0 || oldRoot != newRoot {
			return ErrProof
		}
		return nil
	case oldSize == 0:
		if len(proof) != 0 || oldRoot != emptyRoot() {
			return ErrProof
		}
		return nil
	}
	path := proof
	if oldSize&(oldSize-1) == 0 {
		path = append([]Hash{oldRoot}, proof...)
	}
	if len(path) == 0 {
		return ErrProof
	}
	fn, sn := oldSize-1, newSize-1
	for fn&1 == 1 {
		fn >>= 1
		sn >>= 1
	}
	fr, sr := path[0], path[0]
	for _, c := range path[1:] {
		if sn == 0 {
			return ErrProof
		}
		if fn&1 == 1 || fn == sn {
			fr = nodeHash(c, fr)
			sr = nodeHash(c, sr)
			for fn&1 == 0 && fn != 0 {
				fn >>= 1
				sn >>= 1
			}
		} else {
			sr = nodeHash(sr, c)
		}
		fn >>= 1
		sn >>= 1
	}
	if sn != 0 || fr != oldRoot || sr != newRoot {
		return ErrProof
	}
	return nil
}

// TreeHead is a signed statement that the log had Size entries and root Root.
type TreeHead struct {
	Origin string `json:"origin"`
	Size   uint64 `json:"size"`
	Root   Hash   `json:"root"`
	Signer []byte `json:"signer"`
	Sig    []byte `json:"sig"`
}

const headDomain = "joltrin-translog-head-v1"

func headBytes(origin string, size uint64, root Hash) []byte {
	var b bytes.Buffer
	b.WriteString(headDomain)
	b.WriteByte(0)
	var n [8]byte
	binary.BigEndian.PutUint64(n[:], uint64(len(origin)))
	b.Write(n[:])
	b.WriteString(origin)
	binary.BigEndian.PutUint64(n[:], size)
	b.Write(n[:])
	b.Write(root[:])
	return b.Bytes()
}

// SignHead signs the current head. origin names the log so a head for one log
// cannot be replayed as a head for another.
func (l *Log) SignHead(origin string, priv ed25519.PrivateKey) (TreeHead, error) {
	if origin == "" {
		return TreeHead{}, errors.New("translog: origin is required")
	}
	l.mu.RLock()
	size := uint64(len(l.leaves))
	root := rootOf(l.leaves)
	l.mu.RUnlock()
	pub, ok := priv.Public().(ed25519.PublicKey)
	if !ok {
		return TreeHead{}, errors.New("translog: not an ed25519 key")
	}
	return TreeHead{
		Origin: origin, Size: size, Root: root, Signer: pub,
		Sig: ed25519.Sign(priv, headBytes(origin, size, root)),
	}, nil
}

// VerifyHead checks that pub signed the head for the named origin.
func (h TreeHead) VerifyHead(origin string, pub ed25519.PublicKey) error {
	if h.Origin != origin {
		return fmt.Errorf("translog: head is for origin %q, want %q", h.Origin, origin)
	}
	if !bytes.Equal(h.Signer, pub) {
		return errors.New("translog: head signed by a different key")
	}
	if len(pub) != ed25519.PublicKeySize || !ed25519.Verify(pub, headBytes(h.Origin, h.Size, h.Root), h.Sig) {
		return errors.New("translog: bad head signature")
	}
	return nil
}
