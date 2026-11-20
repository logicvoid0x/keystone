package devdealer_test

import (
	"bytes"
	"math/rand"
	"testing"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/stretchr/testify/require"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/logicvoid0x/keystone/testutil/devdealer"
	custodytypes "github.com/logicvoid0x/keystone/x/custody/types"
)

func addrs(n int) []sdk.AccAddress {
	out := make([]sdk.AccAddress, n)
	for i := range out {
		out[i] = sdk.AccAddress(bytes.Repeat([]byte{byte(n - i)}, 20))
	}
	return out
}

func TestDealThresholdAndEvenY(t *testing.T) {
	for seed := int64(0); seed < 8; seed++ {
		d, err := devdealer.Deal(rand.New(rand.NewSource(seed)), addrs(4))
		require.NoError(t, err)
		require.Equal(t, uint32(3), d.Epoch.T)
		require.Equal(t, byte(0x02), d.Epoch.GroupKey[0])
		_, negated, err := custodytypes.ParseGroupKey(d.Epoch.GroupKey)
		require.NoError(t, err)
		require.False(t, negated)
		// Indices follow address order.
		for i := 1; i < len(d.Operators); i++ {
			require.Negative(t, bytes.Compare(d.Operators[i-1], d.Operators[i]))
		}
		// Any t shares recover the key; their verifying shares match.
		subsets := [][]int{{0, 1, 2}, {1, 2, 3}, {0, 2, 3}, {0, 1, 3}}
		for _, sub := range subsets {
			m := map[uint32][]byte{}
			for _, i := range sub {
				sh := d.Shares[i].Shares[0]
				m[sh.Index] = sh.Secret
				require.Equal(t, d.VerifyingShares[i].Pubkey, sh.VerifyingShare)
			}
			s, err := devdealer.Combine(m)
			require.NoError(t, err)
			b := s.Bytes()
			_, pub := btcecPriv(b[:])
			require.Equal(t, d.Epoch.GroupKey, pub)
		}
	}
}

func TestLocalChainID(t *testing.T) {
	require.True(t, devdealer.LocalChainID("keystone-local-1"))
	require.True(t, devdealer.LocalChainID("x-local-42"))
	require.False(t, devdealer.LocalChainID("keystone-signet-1"))
	require.False(t, devdealer.LocalChainID("keystone-local-"))
	require.False(t, devdealer.LocalChainID("keystone-local-1a"))
	require.Equal(t, 5, devdealer.Threshold(7))
	require.Equal(t, 14, devdealer.Threshold(20))
}

func btcecPriv(b []byte) (any, []byte) {
	_, pub := btcec.PrivKeyFromBytes(b)
	return nil, pub.SerializeCompressed()
}
