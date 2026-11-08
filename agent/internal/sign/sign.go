// Package sign signs and verifies Assay reports with Ed25519.
//
// What gets signed: the report's canonical bytes (see report.Canonical) with the
// signature field removed. Changing any value, even one digit, breaks the signature.
//
// What a signature proves:
//   - On its own: the report is unmodified since some key signed it.
//   - With a pinned public key (one you already trust): that specific agent install
//     signed it. Without pinning, a forger could edit a report and re-sign it with
//     their own key, so always pin when it matters.
//
// It does NOT prove which physical GPU was measured; that needs hardware
// attestation (see docs/superpowers/specs, threat model).
package sign

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/anushka11p/Assay-health-grades-for-GPU-backed-debt/agent/internal/report"
)

const (
	PrivateKeyFile = "agent.key"
	PublicKeyFile  = "agent.pub"
)

// ErrTampered means the report's contents don't match its signature.
var ErrTampered = errors.New("report has been modified since it was signed")

// GenerateKey makes a new random Ed25519 key pair.
func GenerateKey() (ed25519.PrivateKey, error) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	return priv, err
}

// KeyID is a short, human-friendly fingerprint of a public key.
func KeyID(pub ed25519.PublicKey) string {
	sum := sha256.Sum256(pub)
	return hex.EncodeToString(sum[:8])
}

// SaveKeyPair writes agent.key (private, owner-only) and agent.pub into dir.
// It refuses to overwrite an existing key unless force is true, because losing a
// key means old reports can no longer be tied to this install.
func SaveKeyPair(dir string, priv ed25519.PrivateKey, force bool) error {
	keyPath := filepath.Join(dir, PrivateKeyFile)
	if _, err := os.Stat(keyPath); err == nil && !force {
		return fmt.Errorf("%s already exists (use --force to replace it)", keyPath)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	privDER, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return err
	}
	pubDER, err := x509.MarshalPKIXPublicKey(priv.Public())
	if err != nil {
		return err
	}
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privDER}), 0o600); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, PublicKeyFile), pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubDER}), 0o644)
}

// LoadPrivateKey reads a PEM private key written by SaveKeyPair.
func LoadPrivateKey(path string) (ed25519.PrivateKey, error) {
	der, err := readPEM(path, "PRIVATE KEY")
	if err != nil {
		return nil, err
	}
	k, err := x509.ParsePKCS8PrivateKey(der)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	priv, ok := k.(ed25519.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("%s: not an Ed25519 key", path)
	}
	return priv, nil
}

// LoadPublicKey reads a PEM public key written by SaveKeyPair.
func LoadPublicKey(path string) (ed25519.PublicKey, error) {
	der, err := readPEM(path, "PUBLIC KEY")
	if err != nil {
		return nil, err
	}
	k, err := x509.ParsePKIXPublicKey(der)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	pub, ok := k.(ed25519.PublicKey)
	if !ok {
		return nil, fmt.Errorf("%s: not an Ed25519 key", path)
	}
	return pub, nil
}

func readPEM(path, wantType string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(data)
	if block == nil || block.Type != wantType {
		return nil, fmt.Errorf("%s: expected a PEM %q block", path, wantType)
	}
	return block.Bytes, nil
}

// signedBytes is what gets signed: the canonical report without its signature.
func signedBytes(r report.Report) ([]byte, error) {
	r.Signature = nil // r is a copy, so the caller's report is untouched
	return report.Canonical(r)
}

// Sign attaches a signature to r, replacing any existing one.
func Sign(r *report.Report, priv ed25519.PrivateKey) error {
	msg, err := signedBytes(*r)
	if err != nil {
		return err
	}
	pub := priv.Public().(ed25519.PublicKey)
	sum := sha256.Sum256(msg)
	r.Signature = &report.Signature{
		Algorithm: "ed25519",
		KeyID:     KeyID(pub),
		PublicKey: base64.StdEncoding.EncodeToString(pub),
		Digest:    "sha256:" + hex.EncodeToString(sum[:]),
		Value:     base64.StdEncoding.EncodeToString(ed25519.Sign(priv, msg)),
	}
	return nil
}

// Result describes a successful verification.
type Result struct {
	KeyID  string
	Digest string
	Pinned bool // true if the signer matched the trusted key passed in
}

// Verify checks r's signature. If trusted is non-nil, the report must also have
// been signed by exactly that key. Any mismatch returns an error.
func Verify(r report.Report, trusted ed25519.PublicKey) (Result, error) {
	s := r.Signature
	if s == nil {
		return Result{}, errors.New("report is not signed")
	}
	if s.Algorithm != "ed25519" {
		return Result{}, fmt.Errorf("unsupported signature algorithm %q", s.Algorithm)
	}
	pubBytes, err := base64.StdEncoding.DecodeString(s.PublicKey)
	if err != nil || len(pubBytes) != ed25519.PublicKeySize {
		return Result{}, errors.New("signature has a malformed public key")
	}
	pub := ed25519.PublicKey(pubBytes)
	sig, err := base64.StdEncoding.DecodeString(s.Value)
	if err != nil {
		return Result{}, errors.New("signature value is not valid base64")
	}

	msg, err := signedBytes(r)
	if err != nil {
		return Result{}, err
	}
	if !ed25519.Verify(pub, msg, sig) {
		return Result{}, ErrTampered
	}
	sum := sha256.Sum256(msg)
	if s.Digest != "sha256:"+hex.EncodeToString(sum[:]) {
		return Result{}, ErrTampered
	}
	if s.KeyID != KeyID(pub) {
		return Result{}, errors.New("key_id does not match the public key")
	}
	if trusted != nil && !pub.Equal(trusted) {
		return Result{}, fmt.Errorf("signed by key %s, but the trusted key is %s", KeyID(pub), KeyID(trusted))
	}
	return Result{KeyID: s.KeyID, Digest: s.Digest, Pinned: trusted != nil}, nil
}
