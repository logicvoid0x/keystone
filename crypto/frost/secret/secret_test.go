package secret_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/logicvoid0x/keystone/crypto/frost/secret"
)

func TestSealOpen(t *testing.T) {
	e, err := secret.Seal([]byte("pw"), []byte("hello"), []byte("ad"))
	require.NoError(t, err)
	pt, err := secret.Open([]byte("pw"), e, []byte("ad"))
	require.NoError(t, err)
	require.Equal(t, "hello", string(pt))

	_, err = secret.Open([]byte("wrong"), e, []byte("ad"))
	require.ErrorIs(t, err, secret.ErrDecrypt)
	_, err = secret.Open([]byte("pw"), e, []byte("other"))
	require.ErrorIs(t, err, secret.ErrDecrypt)
	e.Ciphertext[0] ^= 1
	_, err = secret.Open([]byte("pw"), e, []byte("ad"))
	require.ErrorIs(t, err, secret.ErrDecrypt)
}

func TestSharesFile(t *testing.T) {
	home := t.TempDir()
	in := secret.EpochShares{Epoch: 1, GroupKey: []byte{2, 1}, N: 4, T: 3, Shares: []secret.Share{{Index: 2, Secret: []byte{9}, VerifyingShare: []byte{3, 4}}}}
	require.NoError(t, secret.WriteShares(home, []byte("pw"), in))
	out, err := secret.ReadShares(home, []byte("pw"), 1)
	require.NoError(t, err)
	require.Equal(t, in, out)
	_, err = secret.ReadShares(home, []byte("bad"), 1)
	require.Error(t, err)
	_, err = secret.ReadShares(home, []byte("pw"), 2)
	require.Error(t, err)
}
