// FLAG FOR AUDIT: RFC 9591 signing (binding factors, group commitment, Lagrange
// coefficients, challenge, share verification, aggregation) with the
// frost-secp256k1-tr parity rules. Public data only: used on-chain by x/custody.

package frost

import (
	"errors"
	"fmt"
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

// Signing is the public state of one signing operation, prepared from the key, the
// message and the commitments of exactly the participating signers.
type Signing struct {
	key         Key
	msg         []byte
	commitments []Commitment // sorted by ID
	rhoInput    [][]byte
	rho         []Scalar
	lambda      []Scalar
	shares      []Point // R_i = D_i + ρ_i·E_i, negated when R has odd y (Taproot)
	r           Point   // group commitment, even y for Taproot
	rOdd        bool
	c           Scalar
}

// NewSigning computes binding factors, the group commitment, Lagrange coefficients and
// the challenge. commitments must be sorted by strictly increasing nonzero ID.
func NewSigning(key Key, msg []byte, commitments []Commitment) (*Signing, error) {
	if len(commitments) == 0 {
		return nil, errors.New("frost: no commitments")
	}
	for i, c := range commitments {
		if c.ID == 0 || (i > 0 && commitments[i-1].ID >= c.ID) {
			return nil, errors.New("frost: commitments not sorted by distinct nonzero id")
		}
		if isIdentity(&c.Hiding) || isIdentity(&c.Binding) {
			return nil, errIdentity
		}
	}
	s := &Signing{key: key, msg: append([]byte(nil), msg...), commitments: append([]Commitment(nil), commitments...)}

	// Binding factor inputs: vk ‖ H4(msg) ‖ H5(encode_group_commitments) ‖ id.
	var enc []byte
	for _, c := range commitments {
		id := identifierBytes(c.ID)
		d, err := SerializePoint(c.Hiding)
		if err != nil {
			return nil, err
		}
		e, err := SerializePoint(c.Binding)
		if err != nil {
			return nil, err
		}
		enc = append(append(append(enc, id[:]...), d[:]...), e[:]...)
	}
	vk := key.VerifyingKey()
	h4, h5 := key.suite.H4(msg), key.suite.H5(enc)
	prefix := append(append(append([]byte{}, vk[:]...), h4[:]...), h5[:]...)
	var r Point
	s.rhoInput = make([][]byte, len(commitments))
	s.rho = make([]Scalar, len(commitments))
	s.shares = make([]Point, len(commitments))
	for i, c := range commitments {
		id := identifierBytes(c.ID)
		s.rhoInput[i] = append(append([]byte{}, prefix...), id[:]...)
		s.rho[i] = key.suite.H1(s.rhoInput[i])
		s.shares[i] = add(c.Hiding, mul(&s.rho[i], c.Binding))
		r = add(r, s.shares[i])
	}
	r.ToAffine()
	if isIdentity(&r) {
		return nil, errIdentity
	}
	if key.suite.taproot && hasOddY(&r) {
		s.rOdd = true
		r = neg(r)
		for i := range s.shares {
			s.shares[i] = neg(s.shares[i])
		}
	}
	s.r = r

	s.lambda = make([]Scalar, len(commitments))
	for i, c := range commitments {
		s.lambda[i] = lagrange(commitments, c.ID)
	}
	s.c = challenge(key.suite, r, key.vk, msg)
	return s, nil
}

// lagrange is λ_i = Π_{j≠i} x_j / (x_j − x_i) at 0. IDs are distinct, so the
// denominator is nonzero.
func lagrange(cs []Commitment, id uint32) Scalar {
	num, den := identifierScalar(1), identifierScalar(1)
	xi := identifierScalar(id)
	xi.Negate()
	for _, c := range cs {
		if c.ID == id {
			continue
		}
		xj := identifierScalar(c.ID)
		num.Mul(&xj)
		d := xj
		d.Add(&xi)
		den.Mul(&d)
	}
	den.InverseNonConst()
	return *num.Mul(&den)
}

// challenge is H2(R ‖ vk ‖ msg); Taproot hashes x-only R and vk (BIP-340).
func challenge(s Suite, r, vk Point, msg []byte) Scalar {
	var pre []byte
	if s.taproot {
		rx, vx := xOnly(r), xOnly(vk)
		pre = append(append(pre, rx[:]...), vx[:]...)
	} else {
		rb, _ := SerializePoint(r)
		vb, _ := SerializePoint(vk)
		pre = append(append(pre, rb[:]...), vb[:]...)
	}
	return s.H2(append(pre, msg...))
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
func (s *Signing) Message() []byte { return s.msg }

// Commitment returns signer id's commitment.
func (s *Signing) Commitment(id uint32) (Commitment, error) {
	i, err := s.index(id)
	if err != nil {
		return Commitment{}, err
	}
	return s.commitments[i], nil
}

// BindingFactorInput and BindingFactor return ρ_i's preimage and value.
func (s *Signing) BindingFactorInput(id uint32) ([]byte, error) {
	i, err := s.index(id)
	if err != nil {
		return nil, err
	}
	return s.rhoInput[i], nil
}

// BindingFactor returns ρ_i.
func (s *Signing) BindingFactor(id uint32) (Scalar, error) {
	i, err := s.index(id)
	if err != nil {
		return Scalar{}, err
	}
	return s.rho[i], nil
}

// Lambda returns signer id's Lagrange coefficient over the signing set.
func (s *Signing) Lambda(id uint32) (Scalar, error) {
	i, err := s.index(id)
	if err != nil {
		return Scalar{}, err
	}
	return s.lambda[i], nil
}

// Challenge is c.
func (s *Signing) Challenge() Scalar { return s.c }

// NegateNonces reports whether signers must negate (d, e): the group commitment has
// odd y (Taproot only).
func (s *Signing) NegateNonces() bool { return s.rOdd }

// VerifyShares checks a combined share z = Σ_{i∈ids} z_i of the signers ids against
// their raw verifying shares ys (of the group key, same order as ids):
// z·G == Σ R_i' + c·Σ λ_i·Y_i″.
func (s *Signing) VerifyShares(ids []uint32, ys []Point, z Scalar) bool {
	if len(ids) == 0 || len(ids) != len(ys) {
		return false
	}
	var rhs Point
	for k, id := range ids {
		i, err := s.index(id)
		if err != nil {
			return false
		}
		for _, prev := range ids[:k] {
			if prev == id {
				return false
			}
		}
		cl := s.c
		cl.Mul(&s.lambda[i])
		rhs = add(rhs, add(s.shares[i], mul(&cl, s.key.VerifyingShare(ys[k]))))
	}
	return equal(MulBase(&z), rhs)
}

// Signature is an aggregated signature (R, z).
type Signature struct {
	R Point
	Z Scalar
}

// Aggregate sums the shares of every signer in the set: z = Σ z_i.
func (s *Signing) Aggregate(zs []Scalar) Signature {
	var z Scalar
	for i := range zs {
		z.Add(&zs[i])
	}
	return Signature{R: s.r, Z: z}
}

// Bytes serializes a signature: x(R) ‖ z (64 bytes) for Taproot, R ‖ z (65) otherwise.
func (sig Signature) Bytes(s Suite) []byte {
	zb := sig.Z.Bytes()
	if s.taproot {
		rx := xOnly(sig.R)
		return append(rx[:], zb[:]...)
	}
	rb, _ := SerializePoint(sig.R)
	return append(rb[:], zb[:]...)
}

// Verify checks a signature against the key: z·G == R + c·vk. For Taproot this is
// BIP-340 verification for x(vk) with a message of any length.
func Verify(key Key, msg, sig []byte) bool {
	var r Point
	var z Scalar
	var err error
	if key.suite.taproot {
		if len(sig) != 64 {
			return false
		}
		r, err = ParsePoint(append([]byte{0x02}, sig[:32]...))
		if err != nil {
			return false
		}
		if z, err = ParseScalar(sig[32:]); err != nil {
			return false
		}
	} else {
		if len(sig) != 65 {
			return false
		}
		if r, err = ParsePoint(sig[:33]); err != nil {
			return false
		}
		if z, err = ParseScalar(sig[33:]); err != nil {
			return false
		}
	}
	c := challenge(key.suite, r, key.vk, msg)
	return equal(MulBase(&z), add(r, mul(&c, key.vk)))
}
