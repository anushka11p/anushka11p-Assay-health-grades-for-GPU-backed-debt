package tlog

import (
	"crypto/ed25519"
	"fmt"
	"strings"
	"testing"

	"github.com/anushka11p/Assay-health-grades-for-GPU-backed-debt/agent/internal/report"
	"github.com/anushka11p/Assay-health-grades-for-GPU-backed-debt/agent/internal/sign"
)

func key(seed byte) ed25519.PrivateKey {
	s := make([]byte, ed25519.SeedSize)
	s[0] = seed
	return ed25519.NewKeyFromSeed(s)
}

func signedReport(t *testing.T, id string) report.Report {
	t.Helper()
	r := report.Report{SchemaVersion: 1, ReportID: id, CreatedAt: "2026-11-09T12:00:00Z",
		GPUs: []report.GPU{}, HostProbes: []report.ProbeResult{}}
	if err := sign.Sign(&r, key(1)); err != nil {
		t.Fatal(err)
	}
	return r
}

// logWith creates a log in a temp dir holding n reports, saved and re-opened from disk.
func logWith(t *testing.T, n int) (*Log, []report.Report) {
	t.Helper()
	dir := t.TempDir()
	l, _ := Open(dir)
	var rs []report.Report
	for i := range n {
		r := signedReport(t, fmt.Sprintf("r%d", i))
		if _, added, err := l.Append(r); err != nil || !added {
			t.Fatalf("append %d: added=%v err=%v", i, added, err)
		}
		rs = append(rs, r)
	}
	if err := l.Save(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	return reopened, rs
}

func TestAppendSaveReopen(t *testing.T) {
	l, rs := logWith(t, 5)
	if l.Size() != 5 {
		t.Fatalf("size %d after reopen", l.Size())
	}
	// Logging the same report again is a no-op.
	e, added, _ := l.Append(rs[2])
	if added || e.Index != 2 {
		t.Fatalf("duplicate append: added=%v index=%d", added, e.Index)
	}
}

func TestProveAndVerifyEveryReport(t *testing.T) {
	l, rs := logWith(t, 7)
	head, _ := l.SignHead(key(9), "2026-11-09T12:00:00Z")
	logPub := key(9).Public().(ed25519.PublicKey)
	for i, r := range rs {
		p, err := l.Prove(r.Signature.Digest, head)
		if err != nil {
			t.Fatal(err)
		}
		if err := VerifyProof(r.Signature.Digest, p, logPub); err != nil {
			t.Fatalf("report %d: %v", i, err)
		}
	}
}

func TestProofRejectsForgeries(t *testing.T) {
	l, rs := logWith(t, 4)
	head, _ := l.SignHead(key(9), "t")
	logPub := key(9).Public().(ed25519.PublicKey)
	p, _ := l.Prove(rs[1].Signature.Digest, head)

	if err := VerifyProof(rs[2].Signature.Digest, p, logPub); err == nil {
		t.Error("proof for report 1 accepted for report 2")
	}
	bad := p
	bad.LeafIndex = 3
	if err := VerifyProof(rs[1].Signature.Digest, bad, logPub); err == nil {
		t.Error("wrong position accepted")
	}
	bad = p
	bad.TreeHead.TreeSize = 99 // changing the head breaks its signature
	if err := VerifyProof(rs[1].Signature.Digest, bad, logPub); err == nil || !strings.Contains(err.Error(), "signature") {
		t.Errorf("edited tree head accepted: %v", err)
	}
	if err := VerifyProof(rs[1].Signature.Digest, p, key(8).Public().(ed25519.PublicKey)); err == nil {
		t.Error("head from a different log key accepted when pinned")
	}
}

func TestProveNeedsCurrentHead(t *testing.T) {
	l, rs := logWith(t, 3)
	old, _ := l.SignHead(key(9), "t")
	l.Append(signedReport(t, "new"))
	if _, err := l.Prove(rs[0].Signature.Digest, old); err == nil {
		t.Fatal("proving against a stale head must fail")
	}
}

func TestLogOnlyGrows(t *testing.T) {
	l, _ := logWith(t, 3)
	oldHead, _ := l.SignHead(key(9), "t1")
	for i := range 6 {
		l.Append(signedReport(t, fmt.Sprintf("later%d", i)))
	}
	newHead, _ := l.SignHead(key(9), "t2")

	proof, _ := l.Consistency(oldHead.TreeSize)
	oldRoot, _ := parseHash(oldHead.RootHash)
	newRoot, _ := parseHash(newHead.RootHash)
	if err := VerifyConsistency(oldHead.TreeSize, newHead.TreeSize, oldRoot, newRoot, proof); err != nil {
		t.Fatalf("honest growth rejected: %v", err)
	}
}

func TestCorruptEntriesFileIsRejected(t *testing.T) {
	dir := t.TempDir()
	l, _ := Open(dir)
	l.Append(signedReport(t, "a"))
	l.Save()
	// Swap in an entry with a skipped index, as if a line had been deleted.
	_ = writeAtomic(dir+"/"+EntriesFile, []byte(`{"index":1,"digest":"sha256:`+strings.Repeat("ab", 32)+`"}`+"\n"))
	if _, err := Open(dir); err == nil || !strings.Contains(err.Error(), "out of order") {
		t.Fatalf("got %v", err)
	}
}
