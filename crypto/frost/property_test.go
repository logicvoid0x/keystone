package frost_test

import (
	"bytes"
	"crypto/rand"
	"math/big"
	"testing"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcec/v2/schnorr"
	"github.com/btcsuite/btcd/txscript/v2"
	"github.com/stretchr/testify/require"

	"github.com/logicvoid0x/keystone/crypto/frost"
	"github.com/logicvoid0x/keystone/crypto/frost/secret"
)

func randInt(t *testing.T, n int) int {
	t.Helper()
	v, err := rand.Int(rand.Reader, big.NewInt(int64(n)))
	require.NoError(t, err)
	return int(v.Int64())
}

// TestRandomThresholdSigning: random 3-of-4 keys (both parities), random signer sets of
// size 3 and 4, untweaked, BIP-86 and script-root tweaks. Every share verifies, the
// aggregate is a valid BIP-340 signature for the BIP-341 output key, and a tampered
// share is caught.
func TestRandomThresholdSigning(t *testing.T) {
	sawOddP, sawOddQ := false, false
	for iter := 0; iter < 60; iter++ {
		evenY := iter%2 == 0
		gk, shares, err := secret.GenerateWithDealer(nil, 4, 3, evenY)
		require.NoError(t, err)
		if gk[0] == 0x03 {
			sawOddP = true
		}
		pk, err := btcec.ParsePubKey(gk[:])
		require.NoError(t, err)
		gp, err := frost.ParsePoint(gk[:])
		require.NoError(t, err)

		var tweak []byte
		var want []byte // x(Q) per BIP-341
		switch iter % 3 {
		case 0:
			want = schnorr.SerializePubKey(pk)
		case 1:
			tweak = frost.TapTweak(gp, nil)
			want = schnorr.SerializePubKey(txscript.ComputeTaprootKeyNoScript(pk))
		case 2:
			root := make([]byte, 32)
			_, _ = rand.Read(root)
			tweak = frost.TapTweak(gp, root)
			want = schnorr.SerializePubKey(txscript.ComputeTaprootOutputKey(pk, root))
		}
		key, err := frost.NewKey(frost.Taproot, gk[:], tweak)
		require.NoError(t, err)
		x := key.XOnly()
		require.Equal(t, want, x[:])
		if tweak != nil {
			q := frost.AddPoints(maybeNeg(gp, gk[0] == 0x03), frost.MulBase(mustScalar(t, tweak)))
			qb, _ := frost.SerializePoint(q)
			if qb[0] == 0x03 {
				sawOddQ = true
			}
		}

		// Signer set: 3 or 4 of the 4 shares.
		ids := []uint32{1, 2, 3, 4}
		if iter%4 != 3 {
			drop := randInt(t, 4)
			ids = append(ids[:drop:drop], ids[drop+1:]...)
		}
		msg := make([]byte, 32)
		_, _ = rand.Read(msg)
		var cs []frost.Commitment
		nonces := map[uint32]secret.Nonces{}
		for _, id := range ids {
			n, err := secret.NewNonces(frost.Taproot, nil, shares[id-1].Secret)
			require.NoError(t, err)
			d, e, err := n.Commitment()
			require.NoError(t, err)
			c, err := frost.ParseCommitment(id, d[:], e[:])
			require.NoError(t, err)
			cs = append(cs, c)
			nonces[id] = n
		}
		sg, err := frost.NewSigning(key, msg, cs)
		require.NoError(t, err)
		var zs []frost.Scalar
		for _, id := range ids {
			z, err := secret.Sign(sg, id, shares[id-1].Secret, nonces[id])
			require.NoError(t, err)
			require.True(t, sg.VerifyShares([]uint32{id}, []frost.Point{shares[id-1].VerifyingShare}, z))
			// A share computed with the wrong secret fails verification.
			other := shares[id%4].Secret
			bad, err := secret.Sign(sg, id, other, nonces[id])
			require.NoError(t, err)
			require.False(t, sg.VerifyShares([]uint32{id}, []frost.Point{shares[id-1].VerifyingShare}, bad))
			zs = append(zs, z)
		}
		// Combined verification of two signers' summed shares.
		var z01 frost.Scalar
		z01.Add(&zs[0]).Add(&zs[1])
		require.True(t, sg.VerifyShares(ids[:2], []frost.Point{shares[ids[0]-1].VerifyingShare, shares[ids[1]-1].VerifyingShare}, z01))

		sig, err := sg.Aggregate(zs)
		require.NoError(t, err)
		require.True(t, frost.Verify(key, msg, sig))
		parsed, err := schnorr.ParseSignature(sig)
		require.NoError(t, err)
		xpk, err := schnorr.ParsePubKey(want)
		require.NoError(t, err)
		require.True(t, parsed.Verify(msg, xpk), "iter %d", iter)
		require.False(t, bytes.Equal(sig[:32], make([]byte, 32)))
	}
	require.True(t, sawOddP, "no odd-y group key drawn")
	require.True(t, sawOddQ, "no odd-y output key drawn")
}

func mustScalar(t *testing.T, b []byte) *frost.Scalar {
	t.Helper()
	s, err := frost.ParseScalar(b)
	require.NoError(t, err)
	return &s
}

func maybeNeg(p frost.Point, n bool) frost.Point {
	if n {
		return frost.NegPoint(p)
	}
	return p
}
