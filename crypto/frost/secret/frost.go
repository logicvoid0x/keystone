// FLAG FOR AUDIT: FROST secret operations (RFC 9591 nonce_generate, sign, dealer shares)
// are the Zcash Foundation frost-secp256k1-tr / frost-core crates'
// (crypto/frost/internal/zf). Sidecar-only: uses crypto/rand, whose bytes are handed to
// the library.

package secret

import (
	"crypto/rand"
	"errors"
	"io"

	"github.com/logicvoid0x/keystone/crypto/frost"
	"github.com/logicvoid0x/keystone/crypto/frost/internal/zf"
)

// Nonces is a signer's secret nonce pair (d, e) for one commitment.
type Nonces struct {
	Hiding  frost.Scalar
	Binding frost.Scalar
}

// NonceGenerate is RFC 9591 nonce_generate: H3(random ‖ share) (frost-core
// `Nonce::new` with these random bytes). The zero scalar if it fails.
func NonceGenerate(_ frost.Suite, random [32]byte, share frost.Scalar) frost.Scalar {
	n, err := zf.NonceGenerate(random, share.Bytes())
	if err != nil {
		return frost.Scalar{}
	}
	s, err := frost.ParseScalar(n[:])
	if err != nil {
		return frost.Scalar{}
	}
	return s
}

// NewNonces draws (d, e) for share from r (crypto/rand when nil), as frost-core
// `SigningNonces::new` does: hiding first, then binding.
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
	n := Nonces{Hiding: NonceGenerate(s, a, share), Binding: NonceGenerate(s, b, share)}
	if n.Hiding.IsZero() || n.Binding.IsZero() {
		return Nonces{}, errors.New("frost: nonce generation failed")
	}
	return n, nil
}

// Commitment is (d·G, e·G) as 33-byte points.
func (n Nonces) Commitment() (hiding, binding [33]byte, err error) {
	if n.Hiding.IsZero() || n.Binding.IsZero() {
		return hiding, binding, errors.New("frost: zero nonce")
	}
	return zf.NonceCommit(n.Hiding.Bytes(), n.Binding.Bytes())
}

// Sign computes signer id's share with frost-secp256k1-tr `round2::sign` (with the
// session's tweak as `sign_with_tweak` applies it). share is the raw share of the group
// key; the nonces must match id's commitment.
func Sign(sg *frost.Signing, id uint32, share frost.Scalar, n Nonces) (frost.Scalar, error) {
	if _, err := sg.Commitment(id); err != nil {
		return frost.Scalar{}, err
	}
	z, err := sg.Session().Sign(uint16(id), share.Bytes(), n.Hiding.Bytes(), n.Binding.Bytes())
	if err != nil {
		return frost.Scalar{}, errors.New("frost: nonces do not match the signer's commitment or the share is invalid")
	}
	return frost.ParseScalar(z[:])
}

// DealtShare is one participant's output of a trusted dealer.
type DealtShare struct {
	ID             uint32
	Secret         frost.Scalar
	VerifyingShare frost.Point
}

// Split shares secret over n participants with threshold t = len(coeffs) + 1, using
// the given higher coefficients a_1..a_{t-1}: s_i = f(i) (frost-core
// `SigningShare::from_coefficients`, as `split` / `generate_secret_shares` compute it).
// Returns the shares and the commitments a_j·G.
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
	if t == 0 || t > n || n > frost.MaxID {
		return groupKey, nil, errors.New("frost: need 1 <= t <= n <= 65535")
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

// eval is f(x) by frost-core; x is a valid share index (1..65535). It panics on a
// library error, which only an invalid index or a bug can cause.
func eval(poly []frost.Scalar, x uint32) frost.Scalar {
	if x == 0 || x > frost.MaxID {
		panic("frost: share index out of range")
	}
	coeffs := make([][32]byte, len(poly))
	for j := range poly {
		coeffs[j] = poly[j].Bytes()
	}
	b, err := zf.EvalPolynomial(coeffs, uint16(x))
	if err != nil {
		panic(err)
	}
	s, err := frost.ParseScalar(b[:])
	if err != nil {
		panic(err)
	}
	return s
}
