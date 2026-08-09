// FLAG FOR AUDIT: FROST ciphersuite hashes (RFC 9591 §6.5, Zcash Foundation
// frost-secp256k1 / frost-secp256k1-tr). Must match the reference byte for byte.

package frost

import (
	"crypto/sha256"

	"github.com/btcsuite/btcd/btcec/v2"
)

// Suite is a FROST(secp256k1, SHA-256) ciphersuite: the RFC 9591 one or the Taproot
// (BIP-340) variant of frost-secp256k1-tr, which Keystone uses.
type Suite struct {
	context string
	taproot bool
}

var (
	// Taproot is FROST-secp256k1-SHA256-TR-v1 (frost-secp256k1-tr): x-only challenge,
	// even-y group key and group commitment, 64-byte BIP-340 signatures.
	Taproot = Suite{context: "FROST-secp256k1-SHA256-TR-v1", taproot: true}
	// RFC9591 is FROST-secp256k1-SHA256-v1 (frost-secp256k1). Only used to check the
	// shared code against the plain RFC 9591 vectors.
	RFC9591 = Suite{context: "FROST-secp256k1-SHA256-v1"}
)

// IsTaproot reports whether s is the BIP-340 variant.
func (s Suite) IsTaproot() bool { return s.taproot }

// H1 is the binding-factor hash.
func (s Suite) H1(m []byte) btcec.ModNScalar { return hashToScalar(s.context+"rho", m) }

// H2 is the challenge hash: BIP0340/challenge for Taproot, hash_to_field otherwise.
func (s Suite) H2(m []byte) btcec.ModNScalar {
	if s.taproot {
		return reduce32(TaggedHash("BIP0340/challenge", m))
	}
	return hashToScalar(s.context+"chal", m)
}

// H3 is the nonce hash.
func (s Suite) H3(m []byte) btcec.ModNScalar { return hashToScalar(s.context+"nonce", m) }

// H4 is the message hash.
func (s Suite) H4(m []byte) [32]byte { return sha(s.context, "msg", m) }

// H5 is the commitment-list hash.
func (s Suite) H5(m []byte) [32]byte { return sha(s.context, "com", m) }

// HDKG is the DKG proof-of-knowledge challenge hash.
func (s Suite) HDKG(m []byte) btcec.ModNScalar { return hashToScalar(s.context+"dkg", m) }

func sha(context, label string, m []byte) [32]byte {
	h := sha256.New()
	h.Write([]byte(context))
	h.Write([]byte(label))
	h.Write(m)
	var out [32]byte
	h.Sum(out[:0])
	return out
}

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
func reduce32(b [32]byte) btcec.ModNScalar {
	var s btcec.ModNScalar
	s.SetBytes(&b)
	return s
}

// twoTo256ModN is 2^256 mod n.
var twoTo256ModN = func() btcec.ModNScalar {
	var s btcec.ModNScalar
	b := [32]byte{15: 0x01, 16: 0x45, 17: 0x51, 18: 0x23, 19: 0x19, 20: 0x50, 21: 0xb7, 22: 0x5f, 23: 0xc4, 24: 0x40, 25: 0x2d, 26: 0xa1, 27: 0x73, 28: 0x2f, 29: 0xc9, 30: 0xbe, 31: 0xbf}
	s.SetBytes(&b)
	return s
}()

// hashToScalar is hash_to_field(msg, count = 1) for secp256k1 (RFC 9380 §5.2):
// expand_message_xmd with SHA-256 to L = 48 bytes, reduced modulo n. Constant time in
// the input (it hashes secret shares for nonces).
func hashToScalar(dst string, msg []byte) btcec.ModNScalar {
	u := expandMessageXMD(msg, []byte(dst), 48)
	var hi, lo [32]byte
	copy(hi[16:], u[:16])
	copy(lo[:], u[16:])
	var h, l btcec.ModNScalar
	h.SetBytes(&hi)
	l.SetBytes(&lo)
	h.Mul(&twoTo256ModN).Add(&l)
	return h
}

// expandMessageXMD is RFC 9380 §5.3.1 with SHA-256 (b_in_bytes 32, s_in_bytes 64), for
// len(dst) ≤ 255 and n ≤ 255·32.
func expandMessageXMD(msg, dst []byte, n int) []byte {
	ell := (n + 31) / 32
	dstPrime := append(append([]byte{}, dst...), byte(len(dst)))
	h := sha256.New()
	h.Write(make([]byte, 64))
	h.Write(msg)
	h.Write([]byte{byte(n >> 8), byte(n), 0})
	h.Write(dstPrime)
	b0 := h.Sum(nil)

	out := make([]byte, 0, ell*32)
	prev := make([]byte, 32)
	for i := 1; i <= ell; i++ {
		x := make([]byte, 32)
		for j := range x { // b0 for i = 1 (prev is zero), else b0 xor b_{i-1}
			x[j] = b0[j] ^ prev[j]
		}
		h.Reset()
		h.Write(x)
		h.Write([]byte{byte(i)})
		h.Write(dstPrime)
		prev = h.Sum(nil)
		out = append(out, prev...)
	}
	return out[:n]
}
