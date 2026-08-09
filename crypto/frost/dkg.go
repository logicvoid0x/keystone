// FLAG FOR AUDIT: Pedersen DKG public checks (frost-core keys/dkg.rs): proof of
// knowledge of a_0 and verifiable secret sharing commitments.

package frost

// EvalCommitment is Σ_j id^j·C_j, the public image f(id)·G of the polynomial committed
// to by cs (cs[0] is a_0·G).
func EvalCommitment(cs []Point, id uint32) Point {
	var acc Point
	x := identifierScalar(id)
	pow := identifierScalar(1)
	for j := range cs {
		acc = add(acc, mul(&pow, cs[j]))
		pow.Mul(&x)
	}
	return acc
}

// pokChallenge is HDKG(id ‖ C_0 ‖ R).
func pokChallenge(s Suite, id uint32, c0, r Point) (Scalar, error) {
	idb := identifierBytes(id)
	cb, err := SerializePoint(c0)
	if err != nil {
		return Scalar{}, err
	}
	rb, err := SerializePoint(r)
	if err != nil {
		return Scalar{}, err
	}
	pre := append(append(idb[:], cb[:]...), rb[:]...)
	return s.HDKG(pre), nil
}

// PoKChallenge is the challenge a dealer's proof of knowledge answers (for the signer
// side, which computes μ = k + a_0·c).
func PoKChallenge(s Suite, id uint32, c0, r Point) (Scalar, error) {
	return pokChallenge(s, id, c0, r)
}

// VerifyProofOfKnowledge checks a dealer's proof σ = (R, μ) of a_0 for C_0:
// R == μ·G − c·C_0. The proof is a signature encoding (64 bytes x-only R for Taproot).
func VerifyProofOfKnowledge(s Suite, id uint32, c0 Point, proof []byte) bool {
	var r Point
	var mu Scalar
	var err error
	if s.taproot {
		if len(proof) != 64 {
			return false
		}
		if r, err = ParsePoint(append([]byte{0x02}, proof[:32]...)); err != nil {
			return false
		}
		if mu, err = ParseScalar(proof[32:]); err != nil {
			return false
		}
	} else {
		if len(proof) != 65 {
			return false
		}
		if r, err = ParsePoint(proof[:33]); err != nil {
			return false
		}
		if mu, err = ParseScalar(proof[33:]); err != nil {
			return false
		}
	}
	c, err := pokChallenge(s, id, c0, r)
	if err != nil {
		return false
	}
	return equal(r, add(MulBase(&mu), neg(mul(&c, c0))))
}
