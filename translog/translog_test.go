package translog

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"
	"testing"
)

// RFC 6962 reference data: the leaves and roots used by the Certificate
// Transparency test suites.
var rfcLeaves = []string{"", "00", "10", "2021", "3031", "40414243", "5051525354555657", "606162636465666768696a6b6c6d6e6f"}
var rfcRoots = []string{
	"6e340b9cffb37a989ca544e6bb780a2c78901d3fb33738768511a30617afa01d",
	"fac54203e7cc696cf0dfcb42c92a1d9dbaf70ad9e621f4bd8d98662f00e3c125",
	"aeb6bcfe274b70a14fb067a5e5578264db0fa9b51af5e0ba159158f329e06e77",
	"d37ee418976dd95753c1c73862b9398fa2a2cf9b4ff0fdfe8b30cd95209614b7",
	"4e3bbb1f7b478dcfe71fb631631519a3bca12c9aefca1612bfce4c13a86264d4",
	"76e67dadbcdf1e10e1b74ddc608abd2f98dfb16fbce75277b5232a127f2087ef",
	"ddb89be403809e325750d3d263cd78929c2942b7942a34b77e122c9594a74c8c",
	"5dc9da79a70659a9ad559cb701ded9a2ab9d823aad2f4960cfe370eff4604328",
}

func TestRFC6962Roots(t *testing.T) {
	l := New()
	empty, _ := l.Root(0)
	if got := hex.EncodeToString(empty[:]); got != "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855" {
		t.Fatalf("empty root = %s", got)
	}
	for i, s := range rfcLeaves {
		b, _ := hex.DecodeString(s)
		l.Append(b)
		root, err := l.Root(uint64(i + 1))
		if err != nil {
			t.Fatal(err)
		}
		if got := hex.EncodeToString(root[:]); got != rfcRoots[i] {
			t.Fatalf("root of %d leaves = %s, want %s", i+1, got, rfcRoots[i])
		}
	}
}

// naiveRoot recomputes a root from the RFC definition, without sharing code
// with the log.
func naiveRoot(data [][]byte) Hash {
	if len(data) == 0 {
		return emptyRoot()
	}
	if len(data) == 1 {
		return LeafHash(data[0])
	}
	k := 1
	for k*2 < len(data) {
		k *= 2
	}
	return nodeHash(naiveRoot(data[:k]), naiveRoot(data[k:]))
}

func build(n int) (*Log, [][]byte) {
	l := New()
	data := make([][]byte, n)
	for i := range data {
		data[i] = []byte(fmt.Sprintf("receipt-%d", i))
		l.Append(data[i])
	}
	return l, data
}

func TestRootMatchesNaive(t *testing.T) {
	l, data := build(70)
	for n := 0; n <= 70; n++ {
		got, err := l.Root(uint64(n))
		if err != nil || got != naiveRoot(data[:n]) {
			t.Fatalf("size %d: root mismatch (err %v)", n, err)
		}
	}
}

func TestInclusionAllSizes(t *testing.T) {
	l, data := build(70)
	for size := 1; size <= 70; size++ {
		root, _ := l.Root(uint64(size))
		for i := 0; i < size; i++ {
			path, err := l.InclusionProof(uint64(i), uint64(size))
			if err != nil {
				t.Fatal(err)
			}
			if err := VerifyInclusion(LeafHash(data[i]), uint64(i), uint64(size), path, root); err != nil {
				t.Fatalf("size %d index %d: %v", size, i, err)
			}
		}
	}
}

func TestInclusionRejectsTampering(t *testing.T) {
	l, data := build(13)
	root, _ := l.Root(13)
	path, _ := l.InclusionProof(5, 13)
	leaf := LeafHash(data[5])
	if err := VerifyInclusion(LeafHash([]byte("forged")), 5, 13, path, root); err == nil {
		t.Fatal("forged leaf verified")
	}
	if err := VerifyInclusion(leaf, 4, 13, path, root); err == nil {
		t.Fatal("wrong index verified")
	}
	if err := VerifyInclusion(leaf, 5, 6, path, root); err == nil {
		t.Fatal("size with a different tree shape verified")
	}
	if err := VerifyInclusion(leaf, 5, 13, path[:len(path)-1], root); err == nil {
		t.Fatal("short path verified")
	}
	if err := VerifyInclusion(leaf, 5, 13, append(append([]Hash{}, path...), Hash{}), root); err == nil {
		t.Fatal("long path verified")
	}
	bad := append([]Hash{}, path...)
	bad[0][0] ^= 1
	if err := VerifyInclusion(leaf, 5, 13, bad, root); err == nil {
		t.Fatal("flipped path bit verified")
	}
	if err := VerifyInclusion(leaf, 13, 13, path, root); err != ErrIndexRange {
		t.Fatalf("index out of range: %v", err)
	}
}

func TestConsistencyAllPairs(t *testing.T) {
	l, _ := build(40)
	for newSize := 0; newSize <= 40; newSize++ {
		newRoot, _ := l.Root(uint64(newSize))
		for oldSize := 0; oldSize <= newSize; oldSize++ {
			oldRoot, _ := l.Root(uint64(oldSize))
			proof, err := l.ConsistencyProof(uint64(oldSize), uint64(newSize))
			if err != nil {
				t.Fatal(err)
			}
			if err := VerifyConsistency(uint64(oldSize), uint64(newSize), oldRoot, newRoot, proof); err != nil {
				t.Fatalf("%d -> %d: %v", oldSize, newSize, err)
			}
		}
	}
}

func TestConsistencyDetectsRewrittenHistory(t *testing.T) {
	honest, _ := build(20)
	oldRoot, _ := honest.Root(12)

	// A log that rewrote entry 3 and then grew to 20 entries.
	forked := New()
	for i := 0; i < 20; i++ {
		if i == 3 {
			forked.Append([]byte("rewritten"))
			continue
		}
		forked.Append([]byte(fmt.Sprintf("receipt-%d", i)))
	}
	newRoot, _ := forked.Root(20)
	proof, _ := forked.ConsistencyProof(12, 20)
	if err := VerifyConsistency(12, 20, oldRoot, newRoot, proof); err == nil {
		t.Fatal("rewritten history passed the consistency check")
	}
	if err := VerifyConsistency(20, 12, newRoot, oldRoot, nil); err != ErrIndexRange {
		t.Fatalf("shrinking log: %v", err)
	}
}

func TestRangeErrors(t *testing.T) {
	l, _ := build(3)
	if _, err := l.Root(4); err != ErrIndexRange {
		t.Fatal("root beyond size")
	}
	if _, err := l.InclusionProof(3, 3); err != ErrIndexRange {
		t.Fatal("index == size")
	}
	if _, err := l.InclusionProof(0, 4); err != ErrIndexRange {
		t.Fatal("size beyond log")
	}
	if _, err := l.ConsistencyProof(2, 4); err != ErrIndexRange {
		t.Fatal("new size beyond log")
	}
	if _, err := l.ConsistencyProof(3, 2); err != ErrIndexRange {
		t.Fatal("old > new")
	}
}

func TestSignedHead(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	otherPub, _, _ := ed25519.GenerateKey(rand.Reader)
	l, _ := build(9)
	h, err := l.SignHead("joltrin/receipts/acme", priv)
	if err != nil {
		t.Fatal(err)
	}
	if h.Size != 9 {
		t.Fatalf("size %d", h.Size)
	}
	if err := h.VerifyHead("joltrin/receipts/acme", pub); err != nil {
		t.Fatal(err)
	}
	if err := h.VerifyHead("joltrin/receipts/other", pub); err == nil {
		t.Fatal("head replayed under another origin")
	}
	if err := h.VerifyHead("joltrin/receipts/acme", otherPub); err == nil {
		t.Fatal("head accepted for another key")
	}
	tampered := h
	tampered.Size = 8
	if err := tampered.VerifyHead("joltrin/receipts/acme", pub); err == nil {
		t.Fatal("tampered size verified")
	}
	tampered = h
	tampered.Root[0] ^= 1
	if err := tampered.VerifyHead("joltrin/receipts/acme", pub); err == nil {
		t.Fatal("tampered root verified")
	}
	if _, err := l.SignHead("", priv); err == nil {
		t.Fatal("empty origin accepted")
	}
}

func TestConcurrentAppend(t *testing.T) {
	l := New()
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				l.Append([]byte(fmt.Sprintf("%d-%d", g, i)))
				_, _ = l.Root(l.Size())
			}
		}(g)
	}
	wg.Wait()
	if l.Size() != 400 {
		t.Fatalf("size %d", l.Size())
	}
}
