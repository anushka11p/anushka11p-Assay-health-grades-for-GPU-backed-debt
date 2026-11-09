package tlog

import (
	"encoding/hex"
	"errors"
	"testing"
)

// The 8 standard leaves from Certificate Transparency's Merkle tree tests, and the
// expected root for each prefix of them. Cross-checked against an independent
// Python implementation of RFC 6962.
var ctLeaves = []string{
	"", "00", "10", "2021", "3031", "40414243", "5051525354555657", "606162636465666768696a6b6c6d6e6f",
}
var ctRoots = []string{
	"6e340b9cffb37a989ca544e6bb780a2c78901d3fb33738768511a30617afa01d",
	"fac54203e7cc696cf0dfcb42c92a1d9dbaf70ad9e621f4bd8d98662f00e3c125",
	"aeb6bcfe274b70a14fb067a5e5578264db0fa9b51af5e0ba159158f329e06e77",
	"d37ee418976dd95753c1c73862b9398fa2a2cf9b4ff0fdfe8b30cd95209614b7",
	"4e3bbb1f7b478dcfe71fb631631519a3bca12c9aefca1612bfce4c13a86264d4",
	"76e67dadbcdf1e10e1b74ddc608abd2f98dfb16fbce75277b5232a127f2087ef",
	"ddb89be403809e325750d3d263cd78929c2942b7942a34b77e122c9594a74c8c",
	"5dc9da79a70659a9ad559cb701ded9a2ab9d823aad2f4960cfe370eff4604328",
}

func leafHashes(n int) []Hash {
	out := make([]Hash, n)
	for i := range n {
		out[i] = LeafHash([]byte{byte(i), byte(i >> 8), 0xA5})
	}
	return out
}

func TestRootMatchesCertificateTransparencyVectors(t *testing.T) {
	var leaves []Hash
	for i, h := range ctLeaves {
		data, _ := hex.DecodeString(h)
		leaves = append(leaves, LeafHash(data))
		got := Root(leaves)
		if hex.EncodeToString(got[:]) != ctRoots[i] {
			t.Errorf("size %d: root %x, want %s", i+1, got, ctRoots[i])
		}
	}
	empty := Root(nil)
	if hex.EncodeToString(empty[:]) != "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855" {
		t.Error("empty tree root should be SHA-256 of nothing")
	}
}

func TestEveryInclusionProofVerifies(t *testing.T) {
	for n := 1; n <= 40; n++ {
		leaves := leafHashes(n)
		root := Root(leaves)
		for m := range n {
			proof, err := InclusionProof(leaves, m)
			if err != nil {
				t.Fatal(err)
			}
			if err := VerifyInclusion(leaves[m], uint64(m), uint64(n), proof, root); err != nil {
				t.Fatalf("n=%d m=%d: valid proof rejected", n, m)
			}
			// The same proof must not work for a different leaf. (Tree size isn't
			// tested alone: size and root always arrive together in a signed tree head.)
			if VerifyInclusion(leaves[(m+1)%n], uint64(m), uint64(n), proof, root) == nil && n > 1 {
				t.Fatalf("n=%d m=%d: accepted the wrong leaf", n, m)
			}
			if len(proof) > 0 {
				bad := append([]Hash(nil), proof...)
				bad[0][0] ^= 1
				if VerifyInclusion(leaves[m], uint64(m), uint64(n), bad, root) == nil {
					t.Fatalf("n=%d m=%d: accepted a tampered proof", n, m)
				}
			}
		}
	}
}

func TestEveryConsistencyProofVerifies(t *testing.T) {
	for n := 1; n <= 40; n++ {
		leaves := leafHashes(n)
		newRoot := Root(leaves)
		for m := 0; m <= n; m++ {
			oldRoot := Root(leaves[:m])
			proof, err := ConsistencyProof(leaves, m)
			if err != nil {
				t.Fatal(err)
			}
			if err := VerifyConsistency(uint64(m), uint64(n), oldRoot, newRoot, proof); err != nil {
				t.Fatalf("m=%d n=%d: valid proof rejected", m, n)
			}
			if m > 0 && m < n {
				// A log that rewrote history: same size, different old root.
				forged := oldRoot
				forged[0] ^= 1
				if VerifyConsistency(uint64(m), uint64(n), forged, newRoot, proof) == nil {
					t.Fatalf("m=%d n=%d: accepted a rewritten old tree", m, n)
				}
			}
		}
	}
}

func TestEditedLeafChangesTheRoot(t *testing.T) {
	leaves := leafHashes(9)
	before := Root(leaves)
	leaves[4] = LeafHash([]byte("edited"))
	if Root(leaves) == before {
		t.Fatal("editing a leaf must change the root")
	}
}

func TestProofIndexOutOfRange(t *testing.T) {
	if _, err := InclusionProof(leafHashes(3), 3); err == nil {
		t.Error("want error")
	}
	if _, err := ConsistencyProof(leafHashes(3), 4); err == nil {
		t.Error("want error")
	}
	if !errors.Is(VerifyInclusion(Hash{}, 5, 5, nil, Hash{}), ErrProof) {
		t.Error("index >= size must fail")
	}
}
