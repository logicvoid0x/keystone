package multinode_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"cosmossdk.io/collections"

	"github.com/logicvoid0x/keystone/testutil/multinode"
)

// Design §12 "one operator's bitcoind stalls": its attestations stop and the quorum
// comes from the others (3 of 4 > 2/3).
func TestOneBitcoindDown(t *testing.T) {
	net := withdrawNet(t) // vault 1 funded with all watchers up
	net.Nodes[3].BitcoinDown = true
	before := collateral(t, net)
	net.Fund(1, 10_000_000)
	require.Equal(t, before+10_000_000, collateral(t, net))
}

// Design §12 "bitcoind on more than 1/3 of operators stalls": deposits wait (their
// tallies stay below 2/3), withdrawals still sign; once the nodes recover the deposit is
// credited on re-attestation.
func TestTwoBitcoindsDown(t *testing.T) {
	net := withdrawNet(t)
	net.Nodes[2].BitcoinDown, net.Nodes[3].BitcoinDown = true, true
	before := collateral(t, net)
	txid := net.Bitcoin.Send(net.DepositScript(1), 10_000_000)
	net.Bitcoin.Mine(1)
	net.Step(multinode.StepOpts{Txs: [][]byte{withdrawTx(net, 20_000_000, 1)}})
	b := runToSigned(t, net, 1, 40)
	verifySigned(t, net, b)
	has, err := net.Ref().App.CustodyKeeper.Deposits.Has(net.Context(), collections.Join(txid[:], uint32(0)))
	require.NoError(t, err)
	require.False(t, has, "2 of 4 attestations are not a quorum")
	require.Equal(t, before-20_000_000, collateral(t, net))

	net.Nodes[2].BitcoinDown, net.Nodes[3].BitcoinDown = false, false
	for range 4 {
		net.Step(multinode.StepOpts{})
	}
	has, err = net.Ref().App.CustodyKeeper.Deposits.Has(net.Context(), collections.Join(txid[:], uint32(0)))
	require.NoError(t, err)
	require.True(t, has, "credited once the watchers are back")
}

func collateral(t *testing.T, net *multinode.Net) int64 {
	t.Helper()
	v, err := net.Ref().App.VaultKeeper.GetVault(net.Context(), 1)
	require.NoError(t, err)
	return v.CollateralSat.Int64()
}
