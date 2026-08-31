// FLAG FOR AUDIT: DKG secret operations (design §9.3): dealer polynomials, proofs of
// knowledge, share encryption and DLEQ proofs for complaints. Sidecar-only.

package secret

import (
	"crypto/rand"
	"io"

	"github.com/OWNER/keystone/crypto/frost"
)

func reader(r io.Reader) io.Reader {
	if r == nil {
		return rand.Reader
	}
	return r
}

// NewPolynomial draws a dealer's t random coefficients a_0..a_{t−1}.
func NewPolynomial(r io.Reader, t uint32) ([]frost.Scalar, error) {
	poly := make([]frost.Scalar, t)
	for j := range poly {
		s, err := randomScalar(reader(r))
		if err != nil {
			return nil, err
		}
		poly[j] = s
	}
	return poly, nil
}

// EvalPolynomial is f(x).
func EvalPolynomial(poly []frost.Scalar, x uint32) frost.Scalar { return eval(poly, x) }

// Commitments are a_j·G for each coefficient.
func Commitments(poly []frost.Scalar) []frost.Point {
	out := make([]frost.Point, len(poly))
	for j := range poly {
		out[j] = frost.MulBase(&poly[j])
	}
	return out
}

// ProofOfKnowledge proves knowledge of a_0 for C_0 = a_0·G (frost-core
// compute_proof_of_knowledge with the challenge over ctx): k random, R = k·G (negated
// to even y for Taproot, as generate_nonce does), μ = k + a_0·c.
func ProofOfKnowledge(r io.Reader, s frost.Suite, ctx []byte, id uint32, a0 frost.Scalar) ([]byte, error) {
	k, err := randomScalar(reader(r))
	if err != nil {
		return nil, err
	}
	rp := frost.MulBase(&k)
	rb, err := frost.SerializePoint(rp)
	if err != nil {
		return nil, err
	}
	if s.IsTaproot() && rb[0] == 0x03 {
		k.Negate()
		rp = frost.NegPoint(rp)
		rb[0] = 0x02
	}
	c, err := frost.PoKChallenge(s, ctx, id, frost.MulBase(&a0), rp)
	if err != nil {
		return nil, err
	}
	mu := a0
	mu.Mul(&c).Add(&k)
	mb := mu.Bytes()
	if s.IsTaproot() {
		return append(rb[1:], mb[:]...), nil
	}
	return append(rb[:], mb[:]...), nil
}

// SealShare encrypts share for a recipient under key with a random nonce.
func SealShare(r io.Reader, key, ad []byte, share frost.Scalar) ([]byte, error) {
	nonce := make([]byte, frost.ShareNonceLen)
	if _, err := io.ReadFull(reader(r), nonce); err != nil {
		return nil, err
	}
	return frost.SealShareWithNonce(key, ad, nonce, share)
}

// ProveDLEQ returns S = x·B and a proof c ‖ z that log_G(x·G) = log_B(S).
func ProveDLEQ(r io.Reader, x frost.Scalar, b frost.Point) (frost.Point, []byte, error) {
	k, err := randomScalar(reader(r))
	if err != nil {
		return frost.Point{}, nil, err
	}
	a := frost.MulBase(&x)
	s := frost.MulPoint(&x, b)
	c, err := frost.DLEQChallenge(a, b, s, frost.MulBase(&k), frost.MulPoint(&k, b))
	if err != nil {
		return frost.Point{}, nil, err
	}
	z := x
	z.Mul(&c).Add(&k)
	cb, zb := c.Bytes(), z.Bytes()
	return s, append(cb[:], zb[:]...), nil
}

// RandomScalar draws a nonzero scalar (keys).
func RandomScalar(r io.Reader) (frost.Scalar, error) { return randomScalar(reader(r)) }
