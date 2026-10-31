// FLAG FOR AUDIT: even-y normalization and the Taproot tweak are frost-secp256k1-tr's
// (`into_even_y`, `Tweak`), applied with Keystone's stored tweak scalar
// (crypto/frost/internal/zf).

package frost

import "github.com/OWNER/keystone/crypto/frost/internal/zf"

// Key is the key a session signs for: group key P (either parity) and an optional
// BIP-341 tweak t. The verifying key is Q = even(P) + t·G (even(P) untweaked), as
// frost-secp256k1-tr `sign_with_tweak` / `aggregate_with_tweak` derive it from
// t = H_TapTweak(x(P) ‖ root).
type Key struct {
	groupKey [33]byte
	tweak    []byte
	vk       [33]byte // 02 ‖ x(Q)
}

// NewKey builds the key for group key P (33 bytes) and an optional 32-byte tweak t
// (nil: untweaked) in ciphersuite s (Taproot).
func NewKey(_ Suite, groupKey, tweak []byte) (Key, error) {
	if _, err := ParsePoint(groupKey); err != nil {
		return Key{}, err
	}
	var k Key
	copy(k.groupKey[:], groupKey)
	if tweak != nil {
		if _, err := ParseScalar(tweak); err != nil {
			return Key{}, err
		}
		k.tweak = append([]byte(nil), tweak...)
	}
	vk, err := zf.Key(k.groupKey, k.tweak)
	if err != nil {
		return Key{}, errIdentity // Q is the identity
	}
	k.vk = vk
	return k, nil
}

// Suite is the key's ciphersuite.
func (k Key) Suite() Suite { return Taproot }

// VerifyingKey is the 33-byte verifying key 02 ‖ x(Q).
func (k Key) VerifyingKey() [33]byte { return k.vk }

// XOnly is x(Q), the BIP-340 public key the signature verifies under.
func (k Key) XOnly() [32]byte {
	var x [32]byte
	copy(x[:], k.vk[1:])
	return x
}
