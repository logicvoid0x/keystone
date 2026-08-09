// FLAG FOR AUDIT: even-y normalization and Taproot tweak handling, as in
// frost-secp256k1-tr (`into_even_y`, `Tweak for KeyPackage / PublicKeyPackage`,
// `pre_sign`, `pre_aggregate`).

package frost

import "errors"

// Key is the key a session signs for. For the Taproot suite with group key P and
// tweak t (Q = P' + t·G, P' = P normalized to even y):
//
//	effective signing share   s_i″ = g_Q·(g_P·s_i + t)
//	effective verifying share Y_i″ = g_Q·(g_P·Y_i + t·G)
//	verifying key             g_Q·Q (even y)
//
// with g_X = −1 when X has odd y, else 1. Without a tweak t = 0 (frost-secp256k1-tr
// `sign` / `aggregate`); with one, this is `sign_with_tweak` / `aggregate_with_tweak`,
// where the tweak is spread over the shares (Σ λ_i = 1). For the RFC 9591 suite the
// key is P unchanged.
type Key struct {
	suite   Suite
	negP    bool
	tweaked bool
	tweak   Scalar
	tG      Point
	negQ    bool
	vk      Point // verifying key, affine
}

// NewKey builds the key for group key P (33 bytes, either parity) and an optional
// 32-byte BIP-341 tweak t (nil: untweaked; the Taproot suite only).
func NewKey(s Suite, groupKey, tweak []byte) (Key, error) {
	p, err := ParsePoint(groupKey)
	if err != nil {
		return Key{}, err
	}
	k := Key{suite: s}
	if !s.taproot {
		if tweak != nil {
			return Key{}, errors.New("frost: tweak needs the Taproot suite")
		}
		p.ToAffine()
		k.vk = p
		return k, nil
	}
	if hasOddY(&p) {
		k.negP = true
		p = neg(p)
	}
	q := p
	if tweak != nil {
		t, err := ParseScalar(tweak)
		if err != nil {
			return Key{}, err
		}
		k.tweaked, k.tweak, k.tG = true, t, MulBase(&t)
		q = add(p, k.tG)
		q.ToAffine()
		if isIdentity(&q) {
			return Key{}, errIdentity
		}
	}
	if hasOddY(&q) {
		k.negQ = true
		q = neg(q)
	}
	q.ToAffine()
	k.vk = q
	return k, nil
}

// Suite is the key's ciphersuite.
func (k Key) Suite() Suite { return k.suite }

// VerifyingKey is the 33-byte verifying key (02 ‖ x(Q) for Taproot).
func (k Key) VerifyingKey() [33]byte {
	b, _ := SerializePoint(k.vk) // never the identity
	return b
}

// XOnly is x(Q), the BIP-340 public key the signature verifies under.
func (k Key) XOnly() [32]byte { return xOnly(k.vk) }

// VerifyingShare maps a verifying share Y_i of the group key to Y_i″.
func (k Key) VerifyingShare(y Point) Point {
	if !k.suite.taproot {
		return y
	}
	if k.negP {
		y = neg(y)
	}
	if k.tweaked {
		y = add(y, k.tG)
	}
	if k.negQ {
		y = neg(y)
	}
	return y
}

// SigningShare maps a secret share s_i of the group key to s_i″. Constant time in s
// (the parities and the tweak are public).
func (k Key) SigningShare(s Scalar) Scalar {
	if !k.suite.taproot {
		return s
	}
	if k.negP {
		s.Negate()
	}
	if k.tweaked {
		s.Add(&k.tweak)
	}
	if k.negQ {
		s.Negate()
	}
	return s
}
