package multinode_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/OWNER/keystone/app/protocol"
	"github.com/OWNER/keystone/testutil/multinode"
	oracletypes "github.com/OWNER/keystone/x/oracle/types"
)

func TestFourNodesAgree(t *testing.T) {
	net := multinode.New(t, 4)
	prices := []uint64{100_000, 100_010, 99_985, 100_025}
	for i, nd := range net.Nodes {
		nd.Price.Set(prices[i])
	}
	for h := 0; h < 50; h++ {
		r := net.Step(multinode.StepOpts{})
		if r.Height >= 2 {
			_, ok := protocol.DecodeInjected(r.Txs[0])
			require.True(t, ok, "height %d has tx0", r.Height)
		}
	}
	ps, err := net.Ref().App.OracleKeeper.GetPriceState(net.Context())
	require.NoError(t, err)
	require.Equal(t, oracletypes.PRICE_STATUS_ACTIVE, ps.Status)
	// Lower median of 99,985 / 100,000 / 100,010 / 100,025.
	require.Equal(t, "100000000000", ps.SpotMicroUsd.String())
}

func TestProposersRotate(t *testing.T) {
	net := multinode.New(t, 4)
	seen := map[int]bool{}
	for h := 0; h < 8; h++ {
		seen[net.Step(multinode.StepOpts{}).Accepted().Proposer] = true
	}
	require.Len(t, seen, 4)
}

func TestDiffStoresReportsDifference(t *testing.T) {
	net := multinode.New(t, 2)
	net.SetAllPrices(100_000)
	net.Step(multinode.StepOpts{})
	require.Empty(t, multinode.DiffStores(net.Nodes[0].App, net.Nodes[1].App))

	// Plant a difference directly in node 1's committed store.
	a := net.Nodes[1].App
	a.CommitMultiStore().GetKVStore(a.GetKey("oracle")).Set([]byte{0xFF, 0x01}, []byte{0x02})
	d := multinode.DiffStores(net.Nodes[0].App, net.Nodes[1].App)
	require.Contains(t, d, "store oracle: key FF01 only in node B")
}
