// Package tlog is an append-only transparency log of Assay reports: a Merkle tree
// as specified in RFC 6962 (Certificate Transparency), the design Sigstore also uses.
//
// Each report's signed digest becomes a leaf. The tree's root hash summarises every
// leaf, and the log operator signs it (a "signed tree head"). Two proofs follow:
//
//   - Inclusion: a handful of hashes (about log2(N)) proving one report is in the
//     tree with a given root, without downloading the whole log.
//   - Consistency: proof that a newer tree still contains an older tree unchanged,
//     so nobody can quietly delete or edit a report after it was logged.
package tlog

import (
	"crypto/sha256"
	"errors"
	"math/bits"
)

// Hash is a SHA-256 hash.
type Hash [32]byte

// Leaves and inner nodes are hashed with different one-byte prefixes, so a leaf
// can never be passed off as an inner node (a "second preimage" attack).
const (
	leafPrefix = 0x00
	nodePrefix = 0x01
)

// LeafHash is the hash of a leaf's data: SHA-256(0x00 || data).
func LeafHash(data []byte) Hash {
	return sha256.Sum256(append([]byte{leafPrefix}, data...))
}

// nodeHash combines two children: SHA-256(0x01 || left || right).
func nodeHash(left, right Hash) Hash {
	buf := make([]byte, 0, 65)
	buf = append(buf, nodePrefix)
	buf = append(buf, left[:]...)
	buf = append(buf, right[:]...)
	return sha256.Sum256(buf)
}

// split is the largest power of two smaller than n (n >= 2): where RFC 6962 cuts
// a tree into its left and right subtrees.
func split(n int) int {
	return 1 << (bits.Len(uint(n-1)) - 1)
}

// Root is the Merkle tree hash (MTH) of the given leaf hashes.
// The empty tree's root is SHA-256 of nothing.
func Root(leaves []Hash) Hash {
	switch len(leaves) {
	case 0:
		return sha256.Sum256(nil)
	case 1:
		return leaves[0]
	}
	k := split(len(leaves))
	return nodeHash(Root(leaves[:k]), Root(leaves[k:]))
}

// InclusionProof is PATH(m, D[n]) from RFC 6962 section 2.1.1: the sibling
// hashes needed to recompute the root from leaf m.
func InclusionProof(leaves []Hash, m int) ([]Hash, error) {
	if m < 0 || m >= len(leaves) {
		return nil, errors.New("leaf index out of range")
	}
	return path(m, leaves), nil
}

func path(m int, d []Hash) []Hash {
	if len(d) <= 1 {
		return nil
	}
	k := split(len(d))
	if m < k {
		return append(path(m, d[:k]), Root(d[k:]))
	}
	return append(path(m-k, d[k:]), Root(d[:k]))
}

// ConsistencyProof is PROOF(m, D[n]) from RFC 6962 section 2.1.2: hashes
// proving the first m leaves of this tree form the older tree of size m.
func ConsistencyProof(leaves []Hash, m int) ([]Hash, error) {
	if m < 0 || m > len(leaves) {
		return nil, errors.New("old tree size out of range")
	}
	if m == 0 || m == len(leaves) {
		return nil, nil
	}
	return subproof(m, leaves, true), nil
}

func subproof(m int, d []Hash, complete bool) []Hash {
	n := len(d)
	if m == n {
		if complete {
			return nil
		}
		return []Hash{Root(d)}
	}
	k := split(n)
	if m <= k {
		return append(subproof(m, d[:k], complete), Root(d[k:]))
	}
	return append(subproof(m-k, d[k:], false), Root(d[:k]))
}

// ErrProof means a proof doesn't check out.
var ErrProof = errors.New("proof does not match the tree")

// VerifyInclusion checks that leaf (a leaf hash) sits at index in a tree of the
// given size with the given root. Algorithm from RFC 9162 section 2.1.3.2.
func VerifyInclusion(leaf Hash, index, size uint64, proof []Hash, root Hash) error {
	if index >= size {
		return ErrProof
	}
	fn, sn, r := index, size-1, leaf
	for _, p := range proof {
		if sn == 0 {
			return ErrProof
		}
		if fn&1 == 1 || fn == sn {
			r = nodeHash(p, r)
			for fn&1 == 0 && fn != 0 {
				fn >>= 1
				sn >>= 1
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

// VerifyConsistency checks that a tree of size `second` with root secondRoot
// extends the tree of size `first` with root firstRoot. RFC 9162 section 2.1.4.2.
func VerifyConsistency(first, second uint64, firstRoot, secondRoot Hash, proof []Hash) error {
	switch {
	case first > second:
		return ErrProof
	case first == second:
		if len(proof) != 0 || firstRoot != secondRoot {
			return ErrProof
		}
		return nil
	case first == 0:
		// Every tree extends the empty tree.
		if len(proof) != 0 {
			return ErrProof
		}
		return nil
	case len(proof) == 0:
		return ErrProof
	}

	if first&(first-1) == 0 { // first is a power of two: its root is part of the path
		proof = append([]Hash{firstRoot}, proof...)
	}
	fn, sn := first-1, second-1
	for fn&1 == 1 {
		fn >>= 1
		sn >>= 1
	}
	fr, sr := proof[0], proof[0]
	for _, c := range proof[1:] {
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
	if fr != firstRoot || sr != secondRoot || sn != 0 {
		return ErrProof
	}
	return nil
}
