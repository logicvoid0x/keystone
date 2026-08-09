// FLAG FOR AUDIT: group element and scalar encodings (SEC 1 compressed points, 32-byte
// big-endian scalars, as in frost-secp256k1).

package frost

import (
	"errors"

	"github.com/btcsuite/btcd/btcec/v2"
)

// Point is a secp256k1 group element.
type Point = btcec.JacobianPoint

// Scalar is an integer modulo the group order.
type Scalar = btcec.ModNScalar

var (
	errPoint    = errors.New("frost: malformed group element")
	errIdentity = errors.New("frost: identity element")
	errScalar   = errors.New("frost: malformed scalar")
)

// ParsePoint decodes a 33-byte compressed point (never the identity).
func ParsePoint(b []byte) (Point, error) {
	var p Point
	if len(b) != 33 {
		return p, errPoint
	}
	pk, err := btcec.ParsePubKey(b)
	if err != nil {
		return p, errPoint
	}
	pk.AsJacobian(&p)
	return p, nil
}

// SerializePoint encodes p as 33 bytes; the identity cannot be encoded.
func SerializePoint(p Point) ([33]byte, error) {
	var out [33]byte
	p.ToAffine()
	if isIdentity(&p) {
		return out, errIdentity
	}
	copy(out[:], btcec.NewPublicKey(&p.X, &p.Y).SerializeCompressed())
	return out, nil
}

// ParseScalar decodes a 32-byte big-endian scalar below the group order.
func ParseScalar(b []byte) (Scalar, error) {
	var s Scalar
	if len(b) != 32 || s.SetByteSlice(b) {
		return s, errScalar
	}
	return s, nil
}

func isIdentity(p *Point) bool {
	return p.Z.IsZero() || (p.X.IsZero() && p.Y.IsZero())
}

// hasOddY reports whether the affine y of p is odd.
func hasOddY(p *Point) bool {
	q := *p
	q.ToAffine()
	q.Y.Normalize()
	return q.Y.IsOdd()
}

func neg(p Point) Point {
	p.ToAffine()
	p.Y.Normalize()
	p.Y.Negate(1).Normalize()
	return p
}

func add(a, b Point) Point {
	var r Point
	btcec.AddNonConst(&a, &b, &r)
	return r
}

func mul(k *Scalar, p Point) Point {
	var r Point
	btcec.ScalarMultNonConst(k, &p, &r)
	return r
}

// MulBase is k·G. Variable time; the decred library has no constant-time scalar
// multiplication (FLAG FOR AUDIT where k is secret).
func MulBase(k *Scalar) Point {
	var r Point
	btcec.ScalarBaseMultNonConst(k, &r)
	return r
}

func equal(a, b Point) bool {
	a.ToAffine()
	b.ToAffine()
	if isIdentity(&a) || isIdentity(&b) {
		return isIdentity(&a) && isIdentity(&b)
	}
	return a.X.Equals(&b.X) && a.Y.Equals(&b.Y)
}

// AddPoints is a + b.
func AddPoints(a, b Point) Point { return add(a, b) }

// NegPoint is −p.
func NegPoint(p Point) Point { return neg(p) }

// EqualPoints reports whether a and b are the same element.
func EqualPoints(a, b Point) bool { return equal(a, b) }

// TapTweak is H_TapTweak(x(P) ‖ root) as 32 bytes (BIP-341; root empty for BIP-86).
func TapTweak(p Point, root []byte) []byte {
	x := xOnly(p)
	h := TaggedHash("TapTweak", x[:], root)
	return h[:]
}

// xOnly is the 32-byte x coordinate of p.
func xOnly(p Point) [32]byte {
	p.ToAffine()
	p.X.Normalize()
	return *p.X.Bytes()
}

// identifierScalar is the scalar of a share index (RFC 9591 identifiers 1..n).
func identifierScalar(id uint32) Scalar {
	var s Scalar
	s.SetInt(id)
	return s
}

func identifierBytes(id uint32) [32]byte {
	s := identifierScalar(id)
	return s.Bytes()
}
