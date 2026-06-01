// Package signing provides registry-attestation signatures: the registry signs
// each artifact version's content digest with an Ed25519 key, and publishes its
// public key so any consumer can verify that the digest it received is exactly
// what this registry vouches for.
//
// Signing is deterministic (RFC 8032) and computed at the read boundary, so it
// adds no storage and no write-path coupling — the signature of a given digest
// is stable for a given key. This is the "registry vouches" model; capturing a
// publisher's own signature at publish time (who-authored provenance) is a
// future extension that would store the signature per revision.
package signing

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"log"
)

// Signer signs digests and exposes its public key. The zero value is a disabled
// no-op signer.
type Signer struct {
	priv    ed25519.PrivateKey
	pub     ed25519.PublicKey
	keyID   string
	enabled bool
}

// New builds a Signer. A base64 SIGNING_PRIVATE_KEY (32-byte seed or 64-byte
// key) yields a stable key. With no key, allowEphemeral generates a throwaway
// key (handy locally — signatures verify against the exposed public key but
// reset on restart). No key and no ephemeral => disabled.
func New(privKeyB64 string, allowEphemeral bool) *Signer {
	var priv ed25519.PrivateKey
	switch {
	case privKeyB64 != "":
		raw, err := base64.StdEncoding.DecodeString(privKeyB64)
		if err != nil {
			log.Printf("signing: invalid SIGNING_PRIVATE_KEY (%v); signing disabled", err)
			return &Signer{}
		}
		switch len(raw) {
		case ed25519.SeedSize:
			priv = ed25519.NewKeyFromSeed(raw)
		case ed25519.PrivateKeySize:
			priv = ed25519.PrivateKey(raw)
		default:
			log.Printf("signing: SIGNING_PRIVATE_KEY must be %d or %d bytes; signing disabled", ed25519.SeedSize, ed25519.PrivateKeySize)
			return &Signer{}
		}
	case allowEphemeral:
		_, p, err := ed25519.GenerateKey(nil) // crypto/rand
		if err != nil {
			log.Printf("signing: ephemeral key generation failed (%v); signing disabled", err)
			return &Signer{}
		}
		priv = p
		log.Printf("signing: using an EPHEMERAL ed25519 key — set SIGNING_PRIVATE_KEY for a stable key")
	default:
		return &Signer{}
	}

	pub := priv.Public().(ed25519.PublicKey)
	sum := sha256.Sum256(pub)
	return &Signer{priv: priv, pub: pub, keyID: hex.EncodeToString(sum[:])[:16], enabled: true}
}

// Enabled reports whether signing is active.
func (s *Signer) Enabled() bool { return s != nil && s.enabled }

// KeyID is a short, stable identifier for the public key (sha256 prefix).
func (s *Signer) KeyID() string {
	if s == nil {
		return ""
	}
	return s.keyID
}

// PublicKeyB64 is the base64-encoded raw Ed25519 public key (32 bytes).
func (s *Signer) PublicKeyB64() string {
	if !s.Enabled() {
		return ""
	}
	return base64.StdEncoding.EncodeToString(s.pub)
}

// Sign returns the base64 Ed25519 signature over msg (the digest string), or ""
// when signing is disabled.
func (s *Signer) Sign(msg string) string {
	if !s.Enabled() {
		return ""
	}
	return base64.StdEncoding.EncodeToString(ed25519.Sign(s.priv, []byte(msg)))
}
