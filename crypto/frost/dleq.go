// FLAG FOR AUDIT: DKG share encryption and complaint proofs (design §9.3 steps 2–3,
// §13 A10). Public operations: the chain verifies DLEQ proofs and decrypts a disputed
// share once its ECDH secret is revealed.

package frost

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/sha256"
	"encoding/binary"
	"errors"
)

// DLEQTag is the BIP-340 tag of the DLEQ challenge.
const DLEQTag = "Keystone/dleq"

// DLEQChallenge is c = H_tag(A ‖ B ‖ S ‖ R1 ‖ R2) mod n for the statement
// log_G(A) = log_B(S) (all points compressed).
func DLEQChallenge(a, b, s, r1, r2 Point) (Scalar, error) {
	var pre []byte
	for _, p := range []Point{a, b, s, r1, r2} {
		bz, err := SerializePoint(p)
		if err != nil {
			return Scalar{}, err
		}
		pre = append(pre, bz[:]...)
	}
	return reduce32(TaggedHash(DLEQTag, pre)), nil
}

// VerifyDLEQ checks proof = c ‖ z (64 bytes) that log_G(A) = log_B(S): with
// R1 = z·G − c·A and R2 = z·B − c·S, c must equal DLEQChallenge(A, B, S, R1, R2).
// A complainer uses it to show S = x·B for its encryption key A = x·G.
func VerifyDLEQ(a, b, s Point, proof []byte) bool {
	if len(proof) != 64 {
		return false
	}
	c, err := ParseScalar(proof[:32])
	if err != nil {
		return false
	}
	z, err := ParseScalar(proof[32:])
	if err != nil {
		return false
	}
	r1 := add(MulBase(&z), neg(mul(&c, a)))
	r2 := add(mul(&z, b), neg(mul(&c, s)))
	r1.ToAffine()
	r2.ToAffine()
	if isIdentity(&r1) || isIdentity(&r2) {
		return false
	}
	want, err := DLEQChallenge(a, b, s, r1, r2)
	return err == nil && want.Equals(&c)
}

// ShareAD is the associated data of a DKG share ciphertext: epoch (8 bytes) ‖ dealer
// index (4) ‖ recipient index (4), big-endian.
func ShareAD(epoch uint64, dealer, recipient uint32) []byte {
	var ad [16]byte
	binary.BigEndian.PutUint64(ad[0:], epoch)
	binary.BigEndian.PutUint32(ad[8:], dealer)
	binary.BigEndian.PutUint32(ad[12:], recipient)
	return ad[:]
}

// ShareKey derives the AES-256 key of a share ciphertext from the ECDH secret S
// (HKDF-SHA256, ikm = compressed S, info = "Keystone/dkg-share" ‖ ad).
func ShareKey(s Point, ad []byte) ([]byte, error) {
	sb, err := SerializePoint(s)
	if err != nil {
		return nil, err
	}
	return hkdf.Key(sha256.New, sb[:], nil, "Keystone/dkg-share"+string(ad), 32)
}

// ShareNonceLen is the AES-GCM nonce length that prefixes each ciphertext.
const ShareNonceLen = 12

// OpenShare decrypts a share ciphertext (nonce ‖ AES-256-GCM(share)) with the key and
// associated data, and decodes the share scalar.
func OpenShare(key, ad, ct []byte) (Scalar, error) {
	if len(ct) < ShareNonceLen {
		return Scalar{}, errors.New("frost: short share ciphertext")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return Scalar{}, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return Scalar{}, err
	}
	pt, err := gcm.Open(nil, ct[:ShareNonceLen], ct[ShareNonceLen:], ad)
	if err != nil {
		return Scalar{}, err
	}
	return ParseScalar(pt)
}

// SealShareWithNonce encrypts a share under key with the given nonce (the secret side
// draws the nonce; this is the deterministic part).
func SealShareWithNonce(key, ad, nonce []byte, share Scalar) ([]byte, error) {
	if len(nonce) != ShareNonceLen {
		return nil, errors.New("frost: bad nonce length")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	sb := share.Bytes()
	return gcm.Seal(append([]byte{}, nonce...), nonce, sb[:], ad), nil
}

// ECDH is x·P, the shared secret point.
func ECDH(x *Scalar, p Point) Point { return mul(x, p) }

// MulPoint is k·P.
func MulPoint(k *Scalar, p Point) Point { return mul(k, p) }
