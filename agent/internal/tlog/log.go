package tlog

import (
	"bufio"
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/anushka11p/Assay-health-grades-for-GPU-backed-debt/agent/internal/report"
)

// Files in a log directory. All three are safe to publish; the log's private key
// is kept elsewhere (~/.assay/log.key) and never stored here.
const (
	EntriesFile = "entries.jsonl" // one Entry per line, in log order
	HeadFile    = "head.json"     // latest signed tree head
	PubKeyFile  = "log.pub"       // the log's public key, for verifiers to pin
)

// Entry is one logged report. The Merkle leaf is the report's 32-byte digest.
type Entry struct {
	Index     uint64 `json:"index"`
	Digest    string `json:"digest"` // "sha256:<hex>", the report signature's digest
	ReportID  string `json:"report_id"`
	KeyID     string `json:"key_id"` // which agent key signed the report
	CreatedAt string `json:"created_at"`
}

// TreeHead is the log operator's signed statement: "my log has TreeSize entries
// and this RootHash". Publishing it commits the log to its whole history.
type TreeHead struct {
	TreeSize     uint64 `json:"tree_size"`
	RootHash     string `json:"root_hash"` // hex
	Timestamp    string `json:"timestamp"`
	LogKeyID     string `json:"log_key_id"`
	LogPublicKey string `json:"log_public_key"`      // base64
	Signature    string `json:"signature,omitempty"` // base64 Ed25519 over the canonical head without this field
}

// Proof shows one report is in the log described by TreeHead.
type Proof struct {
	LeafIndex uint64   `json:"leaf_index"`
	Digest    string   `json:"digest"`
	AuditPath []string `json:"audit_path"` // hex hashes, leaf to root
	TreeHead  TreeHead `json:"tree_head"`
}

// Log is a transparency log stored in a directory.
type Log struct {
	dir     string
	entries []Entry
	leaves  []Hash
	index   map[Hash]uint64 // leaf -> position, to refuse duplicates
}

// Open loads the log in dir; a missing or empty directory is an empty log.
func Open(dir string) (*Log, error) {
	l := &Log{dir: dir, index: map[Hash]uint64{}}
	f, err := os.Open(filepath.Join(dir, EntriesFile))
	if errors.Is(err, os.ErrNotExist) {
		return l, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for line := 1; sc.Scan(); line++ {
		if strings.TrimSpace(sc.Text()) == "" {
			continue
		}
		var e Entry
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			return nil, fmt.Errorf("%s line %d: %w", EntriesFile, line, err)
		}
		if e.Index != uint64(len(l.entries)) {
			return nil, fmt.Errorf("%s line %d: index %d out of order", EntriesFile, line, e.Index)
		}
		leaf, err := leafFor(e.Digest)
		if err != nil {
			return nil, fmt.Errorf("%s line %d: %w", EntriesFile, line, err)
		}
		l.add(e, leaf)
	}
	return l, sc.Err()
}

func (l *Log) add(e Entry, leaf Hash) {
	l.index[leaf] = e.Index
	l.entries = append(l.entries, e)
	l.leaves = append(l.leaves, leaf)
}

// leafFor turns a "sha256:<hex>" digest into the Merkle leaf hash.
func leafFor(digest string) (Hash, error) {
	raw, err := hex.DecodeString(strings.TrimPrefix(digest, "sha256:"))
	if !strings.HasPrefix(digest, "sha256:") || err != nil || len(raw) != sha256.Size {
		return Hash{}, fmt.Errorf("bad digest %q", digest)
	}
	return LeafHash(raw), nil
}

// Size is the number of entries.
func (l *Log) Size() uint64 { return uint64(len(l.entries)) }

// RootHash is the current Merkle root.
func (l *Log) RootHash() Hash { return Root(l.leaves) }

// Append adds a signed report. The caller must verify the report's signature
// first. Returns the entry and false if the report was already logged.
func (l *Log) Append(r report.Report) (Entry, bool, error) {
	if r.Signature == nil {
		return Entry{}, false, errors.New("report is not signed")
	}
	leaf, err := leafFor(r.Signature.Digest)
	if err != nil {
		return Entry{}, false, err
	}
	if i, ok := l.index[leaf]; ok {
		return l.entries[i], false, nil
	}
	e := Entry{Index: l.Size(), Digest: r.Signature.Digest, ReportID: r.ReportID, KeyID: r.Signature.KeyID, CreatedAt: r.CreatedAt}
	l.add(e, leaf)
	return e, true, nil
}

// Save writes entries.jsonl.
func (l *Log) Save() error {
	if err := os.MkdirAll(l.dir, 0o755); err != nil {
		return err
	}
	var buf bytes.Buffer
	for _, e := range l.entries {
		line, _ := json.Marshal(e)
		buf.Write(line)
		buf.WriteByte('\n')
	}
	return writeAtomic(filepath.Join(l.dir, EntriesFile), buf.Bytes())
}

// SignHead signs the current tree with the log's key.
func (l *Log) SignHead(priv ed25519.PrivateKey, timestamp string) (TreeHead, error) {
	pub := priv.Public().(ed25519.PublicKey)
	root := l.RootHash()
	h := TreeHead{
		TreeSize:     l.Size(),
		RootHash:     hex.EncodeToString(root[:]),
		Timestamp:    timestamp,
		LogKeyID:     keyID(pub),
		LogPublicKey: base64.StdEncoding.EncodeToString(pub),
	}
	msg, err := headBytes(h)
	if err != nil {
		return TreeHead{}, err
	}
	h.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(priv, msg))
	return h, nil
}

// Prove builds an inclusion proof for the report with this digest, against head
// (which must describe the current tree).
func (l *Log) Prove(digest string, head TreeHead) (Proof, error) {
	leaf, err := leafFor(digest)
	if err != nil {
		return Proof{}, err
	}
	i, ok := l.index[leaf]
	if !ok {
		return Proof{}, errors.New("report is not in the log (run: assay log append)")
	}
	root := l.RootHash()
	if head.TreeSize != l.Size() || head.RootHash != hex.EncodeToString(root[:]) {
		return Proof{}, errors.New("tree head is out of date with the log (run: assay log append to re-sign)")
	}
	path, err := InclusionProof(l.leaves, int(i))
	if err != nil {
		return Proof{}, err
	}
	return Proof{LeafIndex: i, Digest: digest, AuditPath: hexes(path), TreeHead: head}, nil
}

// Consistency proves the current tree extends the older tree of size oldSize.
func (l *Log) Consistency(oldSize uint64) ([]Hash, error) {
	return ConsistencyProof(l.leaves, int(oldSize))
}

// VerifyHead checks a tree head's signature. If trusted is non-nil, the head
// must also be signed by exactly that log key.
func VerifyHead(h TreeHead, trusted ed25519.PublicKey) error {
	raw, err := base64.StdEncoding.DecodeString(h.LogPublicKey)
	if err != nil || len(raw) != ed25519.PublicKeySize {
		return errors.New("tree head has a malformed log key")
	}
	pub := ed25519.PublicKey(raw)
	sig, err := base64.StdEncoding.DecodeString(h.Signature)
	if err != nil {
		return errors.New("tree head signature is not valid base64")
	}
	msg, err := headBytes(h)
	if err != nil {
		return err
	}
	if !ed25519.Verify(pub, msg, sig) {
		return errors.New("tree head signature is invalid")
	}
	if h.LogKeyID != keyID(pub) {
		return errors.New("tree head log_key_id doesn't match its key")
	}
	if trusted != nil && !pub.Equal(trusted) {
		return fmt.Errorf("tree head signed by log key %s, but the trusted log key is %s", keyID(pub), keyID(trusted))
	}
	return nil
}

// VerifyProof checks that digest is included in the tree described by p.TreeHead,
// and that the head is properly signed (by trusted, if given).
func VerifyProof(digest string, p Proof, trusted ed25519.PublicKey) error {
	if err := VerifyHead(p.TreeHead, trusted); err != nil {
		return err
	}
	if p.Digest != digest {
		return errors.New("proof is for a different report")
	}
	leaf, err := leafFor(digest)
	if err != nil {
		return err
	}
	root, err := parseHash(p.TreeHead.RootHash)
	if err != nil {
		return err
	}
	path := make([]Hash, len(p.AuditPath))
	for i, s := range p.AuditPath {
		if path[i], err = parseHash(s); err != nil {
			return err
		}
	}
	if err := VerifyInclusion(leaf, p.LeafIndex, p.TreeHead.TreeSize, path, root); err != nil {
		return errors.New("report is not in the log at the claimed position")
	}
	return nil
}

// ReadHead / WriteHead load and store head.json.
func ReadHead(dir string) (TreeHead, error) {
	var h TreeHead
	data, err := os.ReadFile(filepath.Join(dir, HeadFile))
	if err != nil {
		return h, err
	}
	return h, json.Unmarshal(data, &h)
}

func WriteHead(dir string, h TreeHead) error {
	data, _ := json.MarshalIndent(h, "", "  ")
	return writeAtomic(filepath.Join(dir, HeadFile), append(data, '\n'))
}

// headBytes is what a tree head signature covers: the canonical head without
// its signature.
func headBytes(h TreeHead) ([]byte, error) {
	h.Signature = ""
	return report.Canonical(h)
}

func keyID(pub ed25519.PublicKey) string {
	sum := sha256.Sum256(pub)
	return hex.EncodeToString(sum[:8])
}

func hexes(hs []Hash) []string {
	out := make([]string, len(hs))
	for i, h := range hs {
		out[i] = hex.EncodeToString(h[:])
	}
	return out
}

func parseHash(s string) (Hash, error) {
	var h Hash
	raw, err := hex.DecodeString(s)
	if err != nil || len(raw) != len(h) {
		return h, fmt.Errorf("bad hash %q", s)
	}
	copy(h[:], raw)
	return h, nil
}

// writeAtomic writes via a temp file and rename, so a crash can't leave a
// half-written log behind.
func writeAtomic(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
