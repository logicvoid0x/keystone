package multinode_test

import (
	"testing"

	abci "github.com/cometbft/cometbft/abci/types"
	"github.com/stretchr/testify/require"

	"github.com/logicvoid0x/keystone/app"
	"github.com/logicvoid0x/keystone/app/protocol"
	"github.com/logicvoid0x/keystone/testutil/multinode"
)

// propose asks node 0 for a proposal at the next height and lets node 1 judge it.
func propose(t *testing.T, net *multinode.Net) ([][]byte, abci.ResponseProcessProposal_ProposalStatus) {
	t.Helper()
	h := net.Height + 1
	p := net.Nodes[0]
	prep, err := p.App.PrepareProposal(&abci.RequestPrepareProposal{
		MaxTxBytes: 1 << 20, LocalLastCommit: net.LastExtendedCommit(), Height: h, Time: net.Time, ProposerAddress: p.ConsAddr(),
	})
	require.NoError(t, err)
	res, err := net.Nodes[1].App.ProcessProposal(&abci.RequestProcessProposal{
		Txs: prep.Txs, ProposedLastCommit: net.LastCommit(), Hash: []byte("byz"), Height: h, Time: net.Time, ProposerAddress: p.ConsAddr(),
	})
	require.NoError(t, err)
	return prep.Txs, res.Status
}

// Spec M2-T6: with -tags byzantine, KEYSTONE_BYZ makes PrepareProposal misbehave; in
// release builds the variable is ignored.
func TestByzantineProposer(t *testing.T) {
	net := multinode.New(t, 4)
	net.SetAllPrices(100_000)
	steps(net, 4)

	t.Setenv("KEYSTONE_BYZ", "extra_magic")
	txs, status := propose(t, net)
	if !app.ByzantineEnabled {
		require.Len(t, txs, 1, "release build ignores KEYSTONE_BYZ")
		require.Equal(t, abci.ResponseProcessProposal_ACCEPT, status)
		return
	}
	require.Len(t, txs, 2)
	require.Equal(t, abci.ResponseProcessProposal_REJECT, status)

	cases := map[string]abci.ResponseProcessProposal_ProposalStatus{
		"drop_tx0":       abci.ResponseProcessProposal_REJECT,
		"corrupt_one_ve": abci.ResponseProcessProposal_ACCEPT,
		"corrupt_two_ve": abci.ResponseProcessProposal_REJECT,
		"":               abci.ResponseProcessProposal_ACCEPT,
	}
	for mode, want := range cases {
		t.Setenv("KEYSTONE_BYZ", mode)
		txs, status := propose(t, net)
		require.Equal(t, want, status, mode)
		if mode == "drop_tx0" {
			require.True(t, len(txs) == 0 || !protocol.HasMagic(txs[0]))
		}
	}
}
