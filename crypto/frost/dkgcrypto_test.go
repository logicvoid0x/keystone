package frost_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/logicvoid0x/keystone/crypto/frost"
	"github.com/logicvoid0x/keystone/crypto/frost/secret"
)

func TestProofOfKnowledgeWithContext(t *testing.T) {
	ctx := frost.PoKContext("keystone-local-1", 1)
	for i := 0; i < 20; i++ {
		a0, err := secret.RandomScalar(nil)
		require.NoError(t, err)
		proof, err := secret.ProofOfKnowledge(nil, frost.Taproot, ctx, 3, a0)
		require.NoError(t, err)
		c0 := frost.MulBase(&a0)
		require.True(t, frost.VerifyProofOfKnowledge(frost.Taproot, ctx, 3, c0, proof))
		require.False(t, frost.VerifyProofOfKnowledge(frost.Taproot, ctx, 2, c0, proof), "bound to the dealer")
		require.False(t, frost.VerifyProofOfKnowledge(frost.Taproot, frost.PoKContext("keystone-local-1", 2), 3, c0, proof), "bound to the epoch")
		require.False(t, frost.VerifyProofOfKnowledge(frost.Taproot, frost.PoKContext("other-chain", 1), 3, c0, proof), "bound to the chain")
		other, _ := secret.RandomScalar(nil)
		require.False(t, frost.VerifyProofOfKnowledge(frost.Taproot, ctx, 3, frost.MulBase(&other), proof), "rogue key")
	}
}

func TestDLEQAndShareEncryption(t *testing.T) {
	xr, _ := secret.RandomScalar(nil) // recipient encryption key
	xd, _ := secret.RandomScalar(nil) // dealer encryption key
	ar, ad := frost.MulBase(&xr), frost.MulBase(&xd)

	// Dealer encrypts to the recipient with ECDH(xd, A_r).
	share, _ := secret.RandomScalar(nil)
	aad := frost.ShareAD(1, 2, 3)
	key, err := frost.ShareKey(frost.ECDH(&xd, ar), aad)
	require.NoError(t, err)
	ct, err := secret.SealShare(nil, key, aad, share)
	require.NoError(t, err)

	// The recipient reveals S = xr·A_d with a DLEQ proof; anyone can then decrypt.
	s, proof, err := secret.ProveDLEQ(nil, xr, ad)
	require.NoError(t, err)
	require.True(t, frost.VerifyDLEQ(ar, ad, s, proof))
	k2, err := frost.ShareKey(s, aad)
	require.NoError(t, err)
	got, err := frost.OpenShare(k2, aad, ct)
	require.NoError(t, err)
	require.True(t, got.Equals(&share))

	// Wrong statement, tampered proof, wrong associated data.
	require.False(t, frost.VerifyDLEQ(ad, ar, s, proof))
	bad := append([]byte{}, proof...)
	bad[63] ^= 1
	require.False(t, frost.VerifyDLEQ(ar, ad, s, bad))
	_, err = frost.OpenShare(k2, frost.ShareAD(1, 2, 4), ct)
	require.Error(t, err)
	other, _ := secret.RandomScalar(nil)
	s2 := frost.ECDH(&other, ad)
	require.False(t, frost.VerifyDLEQ(ar, ad, s2, proof), "a false ECDH secret does not verify")
}
