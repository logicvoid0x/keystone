// FLAG FOR AUDIT: Pedersen DKG public checks by frost-core (crypto/frost/internal/zf):
// verifiable secret sharing and the proof of knowledge of a_0 with Keystone's context.

package frost

import (
	"encoding/binary"

	"github.com/OWNER/keystone/crypto/frost/internal/zf"
)

// EvalCommitment is Σ_j id^j·C_j, the public image f(id)·G of the polynomial committed
// to by cs (cs[0] is a_0·G). It is the zero Point when it cannot be computed.
func EvalCommitment(cs []Point, id uint32) Point {
	short, err := shortID(id)
	if err != nil {
		return Point{}
	}
	enc := make([][33]byte, len(cs))
	for j := range cs {
		if enc[j], err = SerializePoint(cs[j]); err != nil {
			return Point{}
		}
	}
	y, err := zf.EvalCommitment(enc, short)
	if err != nil {
		return Point{}
	}
	p, err := ParsePoint(y[:])
	if err != nil {
		return Point{}
	}
	return p
}

// PoKContext is Keystone's proof-of-knowledge context: chain_id ‖ epoch (8 bytes BE).
// The challenge is HDKG(ctx ‖ id ‖ C_0 ‖ R); frost-core's has an empty context.
func PoKContext(chainID string, epoch uint64) []byte {
	var e [8]byte
	binary.BigEndian.PutUint64(e[:], epoch)
	return append([]byte(chainID), e[:]...)
}

// VerifyProofOfKnowledge checks a dealer's proof σ = (R, μ) of a_0 for C_0 (64 bytes:
// x-only R ‖ μ): R == μ·G − c·C_0, with the challenge over ctx (nil for frost-core).
func VerifyProofOfKnowledge(_ Suite, ctx []byte, id uint32, c0 Point, proof []byte) bool {
	short, err := shortID(id)
	if err != nil {
		return false
	}
	cb, err := SerializePoint(c0)
	if err != nil {
		return false
	}
	return zf.VerifyKnowledge(ctx, short, cb, proof)
}
