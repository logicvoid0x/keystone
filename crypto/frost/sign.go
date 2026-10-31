// FLAG FOR AUDIT: FROST signing (binding factors, group commitment, Lagrange
// coefficients, challenge, share verification, aggregation, BIP-340 verification) is
// the Zcash Foundation frost-secp256k1-tr crate's (crypto/frost/internal/zf). Public
// data only: used on-chain by x/custody.

package frost

import (
	"errors"
	"fmt"

	"github.com/OWNER/keystone/crypto/frost/internal/zf"
)

// Commitment is a signer's round-one nonce commitment pair (D_i, E_i).
type Commitment struct {
	ID      uint32
	Hiding  Point
	Binding Point
}

// ParseCommitment decodes a commitment from 33-byte hiding and binding points.
func ParseCommitment(id uint32, hiding, binding []byte) (Commitment, error) {
	d, err := ParsePoint(hiding)
	if err != nil {
		return Commitment{}, err
	}
	e, err := ParsePoint(binding)
	if err != nil {
		return Commitment{}, err
	}
	return Commitment{ID: id, Hiding: d, Binding: e}, nil
}

// Signing is the public state of one signing operation: the key, the message and the
// commitments of exactly the participating signers.
type Signing struct {
	key         Key
	commitments []Commitment // sorted by ID
	sess        zf.Session
}

// MaxID is the largest share index (frost-core identifiers are 16-bit).
const MaxID = 0xFFFF

func shortID(id uint32) (uint16, error) {
	if id == 0 || id > MaxID {
		return 0, fmt.Errorf("frost: share index %d out of range", id)
	}
	return uint16(id), nil
}

// NewSigning prepares a signing operation. commitments must be sorted by strictly
// increasing nonzero ID, and the group commitment must not be the identity.
func NewSigning(key Key, msg []byte, commitments []Commitment) (*Signing, error) {
	if len(commitments) == 0 {
		return nil, errors.New("frost: no commitments")
	}
	s := &Signing{key: key, commitments: append([]Commitment(nil), commitments...)}
	s.sess = zf.Session{GroupKey: key.groupKey, Tweak: key.tweak, Msg: append([]byte(nil), msg...)}
	for i, c := range commitments {
		if i > 0 && commitments[i-1].ID >= c.ID {
			return nil, errors.New("frost: commitments not sorted by distinct nonzero id")
		}
		id, err := shortID(c.ID)
		if err != nil {
			return nil, err
		}
		d, err := SerializePoint(c.Hiding)
		if err != nil {
			return nil, err
		}
		e, err := SerializePoint(c.Binding)
		if err != nil {
			return nil, err
		}
		s.sess.Commitments = append(append(append(s.sess.Commitments, byte(id>>8), byte(id)), d[:]...), e[:]...)
	}
	if err := s.sess.Check(); err != nil {
		return nil, errIdentity
	}
	return s, nil
}

func (s *Signing) index(id uint32) (int, error) {
	for i, c := range s.commitments {
		if c.ID == id {
			return i, nil
		}
	}
	return 0, fmt.Errorf("frost: signer %d not in the signing set", id)
}

// Key is the key being signed for.
func (s *Signing) Key() Key { return s.key }

// Message is the signed message.
func (s *Signing) Message() []byte { return s.sess.Msg }

// Session is the encoded session, for crypto/frost/secret.
func (s *Signing) Session() *zf.Session { return &s.sess }

// Commitment returns signer id's commitment.
func (s *Signing) Commitment(id uint32) (Commitment, error) {
	i, err := s.index(id)
	if err != nil {
		return Commitment{}, err
	}
	return s.commitments[i], nil
}

// BindingFactorInput returns ρ_i's preimage.
func (s *Signing) BindingFactorInput(id uint32) ([]byte, error) {
	in, _, err := s.bindingFactor(id)
	return in[:], err
}

// BindingFactor returns ρ_i.
func (s *Signing) BindingFactor(id uint32) (Scalar, error) {
	_, rho, err := s.bindingFactor(id)
	if err != nil {
		return Scalar{}, err
	}
	return ParseScalar(rho[:])
}

func (s *Signing) bindingFactor(id uint32) (in [zf.BindingInputLen]byte, rho [32]byte, err error) {
	if _, err = s.index(id); err != nil {
		return
	}
	return s.sess.BindingFactor(uint16(id))
}

// VerifyShares checks a combined share z = Σ_{i∈ids} z_i of the signers ids against
// their raw verifying shares ys (of the group key, same order as ids):
// z·G == Σ R_i + c·Σ λ_i·Y_i″.
func (s *Signing) VerifyShares(ids []uint32, ys []Point, z Scalar) bool {
	if len(ids) == 0 || len(ids) != len(ys) {
		return false
	}
	short := make([]uint16, len(ids))
	enc := make([][33]byte, len(ys))
	for k, id := range ids {
		if _, err := s.index(id); err != nil {
			return false
		}
		short[k] = uint16(id)
		var err error
		if enc[k], err = SerializePoint(ys[k]); err != nil {
			return false
		}
	}
	return s.sess.VerifyShares(short, enc, z.Bytes())
}

// Aggregate returns the 64-byte BIP-340 signature from shares summing to z (one per
// signer, or one combined share per operator). It fails if the result does not verify.
func (s *Signing) Aggregate(zs []Scalar) ([]byte, error) {
	b := make([][32]byte, len(zs))
	for i := range zs {
		b[i] = zs[i].Bytes()
	}
	sig, err := s.sess.Aggregate(b)
	if err != nil {
		return nil, err
	}
	return sig[:], nil
}

// Verify is BIP-340 verification of sig under x(Q) for a message of any length.
func Verify(key Key, msg, sig []byte) bool { return zf.Verify(key.vk, msg, sig) }
