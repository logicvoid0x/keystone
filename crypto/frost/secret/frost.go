// FLAG FOR AUDIT: FROST secret operations (RFC 9591 nonce_generate, sign, trusted
// dealer keygen) with the frost-secp256k1-tr parity rules. Sidecar-only: uses
// crypto/rand. Secret scalars use btcec ModNScalar arithmetic (constant time); k·G
// uses the library's only (variable-time) base multiplication.

package secret

import (
	"crypto/rand"
	"errors"
	"io"

	"github.com/OWNER/keystone/crypto/frost"
)

// Nonces is a signer's secret nonce pair (d, e) for one commitment.
type Nonces struct {
	Hiding  frost.Scalar
	Binding frost.Scalar
}

// NonceGenerate is RFC 9591 nonce_generate: H3(random ‖ share).
func NonceGenerate(s frost.Suite, random [32]byte, share frost.Scalar) frost.Scalar {
	sb := share.Bytes()
	return s.H3(append(random[:], sb[:]...))
}

// NewNonces draws (d, e) for share from r (crypto/rand when nil).
func NewNonces(s frost.Suite, r io.Reader, share frost.Scalar) (Nonces, error) {
	if r == nil {
		r = rand.Reader
	}
	var a, b [32]byte
	if _, err := io.ReadFull(r, a[:]); err != nil {
		return Nonces{}, err
	}
	if _, err := io.ReadFull(r, b[:]); err != nil {
		return Nonces{}, err
	}
	return Nonces{Hiding: NonceGenerate(s, a, share), Binding: NonceGenerate(s, b, share)}, nil
}

// Commitment is (d·G, e·G) as 33-byte points.
func (n Nonces) Commitment() (hiding, binding [33]byte, err error) {
	if n.Hiding.IsZero() || n.Binding.IsZero() {
		return hiding, binding, errors.New("frost: zero nonce")
	}
	if hiding, err = frost.SerializePoint(frost.MulBase(&n.Hiding)); err != nil {
		return
	}
	binding, err = frost.SerializePoint(frost.MulBase(&n.Binding))
	return
}

// Sign computes signer id's share z_i = d + e·ρ_i + λ_i·s_i″·c, negating (d, e) when
// the group commitment has odd y. share is the raw share of the group key; the
// key's parity and tweak are applied here. The nonces must match id's commitment.
func Sign(sg *frost.Signing, id uint32, share frost.Scalar, n Nonces) (frost.Scalar, error) {
	c, err := sg.Commitment(id)
	if err != nil {
		return frost.Scalar{}, err
	}
	d, e, err := n.Commitment()
	if err != nil {
		return frost.Scalar{}, err
	}
	cd, _ := frost.SerializePoint(c.Hiding)
	ce, _ := frost.SerializePoint(c.Binding)
	if d != cd || e != ce {
		return frost.Scalar{}, errors.New("frost: nonces do not match the signer's commitment")
	}
	rho, _ := sg.BindingFactor(id)
	lambda, _ := sg.Lambda(id)
	hiding, binding := n.Hiding, n.Binding
	if sg.NegateNonces() {
		hiding.Negate()
		binding.Negate()
	}
	s := sg.Key().SigningShare(share)
	ch := sg.Challenge()
	z := hiding
	z.Add(binding.Mul(&rho))
	s.Mul(&lambda).Mul(&ch)
	z.Add(&s)
	return z, nil
}

// DealtShare is one participant's output of a trusted dealer.
type DealtShare struct {
	ID             uint32
	Secret         frost.Scalar
	VerifyingShare frost.Point
}

// Split shares secret over n participants with threshold t = len(coeffs) + 1, using
// the given higher coefficients a_1..a_{t-1} (frost-core `split` with
// `generate_secret_polynomial`): s_i = f(i). Returns the shares and the commitments
// a_j·G.
func Split(secret frost.Scalar, coeffs []frost.Scalar, n uint32) ([]DealtShare, []frost.Point) {
	poly := append([]frost.Scalar{secret}, coeffs...)
	commits := make([]frost.Point, len(poly))
	for j := range poly {
		commits[j] = frost.MulBase(&poly[j])
	}
	out := make([]DealtShare, 0, n)
	for id := uint32(1); id <= n; id++ {
		s := eval(poly, id)
		out = append(out, DealtShare{ID: id, Secret: s, VerifyingShare: frost.MulBase(&s)})
	}
	return out, commits
}

// GenerateWithDealer draws a fresh t-of-n key from r (crypto/rand when nil). If
// evenY, every coefficient is negated when the group key has odd y, so the key and
// shares are stored already normalized (Keystone's convention, design §9.6).
func GenerateWithDealer(r io.Reader, n, t uint32, evenY bool) (groupKey [33]byte, shares []DealtShare, err error) {
	if r == nil {
		r = rand.Reader
	}
	if t == 0 || t > n {
		return groupKey, nil, errors.New("frost: need 1 <= t <= n")
	}
	poly := make([]frost.Scalar, t)
	for j := range poly {
		if poly[j], err = randomScalar(r); err != nil {
			return groupKey, nil, err
		}
	}
	if groupKey, err = frost.SerializePoint(frost.MulBase(&poly[0])); err != nil {
		return
	}
	if evenY && groupKey[0] == 0x03 {
		for j := range poly {
			poly[j].Negate()
		}
		if groupKey, err = frost.SerializePoint(frost.MulBase(&poly[0])); err != nil {
			return
		}
	}
	shares, _ = Split(poly[0], poly[1:], n)
	return groupKey, shares, nil
}

func randomScalar(r io.Reader) (frost.Scalar, error) {
	var b [32]byte
	for {
		if _, err := io.ReadFull(r, b[:]); err != nil {
			return frost.Scalar{}, err
		}
		var s frost.Scalar
		if s.SetBytes(&b) == 0 && !s.IsZero() {
			return s, nil
		}
	}
}

// eval is f(x) = Σ a_j x^j (Horner).
func eval(poly []frost.Scalar, x uint32) frost.Scalar {
	var xs frost.Scalar
	xs.SetInt(x)
	acc := poly[len(poly)-1]
	for j := len(poly) - 2; j >= 0; j-- {
		acc.Mul(&xs).Add(&poly[j])
	}
	return acc
}
