package frost

import "crypto/sha256"

// Suite is the FROST ciphersuite. Keystone uses one: FROST(secp256k1, SHA-256) in the
// Taproot (BIP-340) variant, implemented by the Zcash Foundation frost-secp256k1-tr
// crate (crypto/frost/internal/zf).
type Suite struct{}

// Taproot is FROST-secp256k1-SHA256-TR-v1 (frost-secp256k1-tr): x-only challenge,
// even-y group key and group commitment, 64-byte BIP-340 signatures.
var Taproot = Suite{}

// TaggedHash is the BIP-340 tagged hash SHA256(SHA256(tag) ‖ SHA256(tag) ‖ m).
func TaggedHash(tag string, m ...[]byte) [32]byte {
	th := sha256.Sum256([]byte(tag))
	h := sha256.New()
	h.Write(th[:])
	h.Write(th[:])
	for _, b := range m {
		h.Write(b)
	}
	var out [32]byte
	h.Sum(out[:0])
	return out
}

// reduce32 interprets b as a big-endian integer modulo the group order.
func reduce32(b [32]byte) Scalar {
	var s Scalar
	s.SetBytes(&b)
	return s
}
