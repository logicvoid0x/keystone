package multinode_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/OWNER/keystone/testutil/multinode"
)

// TestDeterminism10k is spec M2-T3: 4 nodes, each with its own noisy prices, run the
// random workload; app hashes are compared every height (Step fails on a mismatch with a
// state diff) and every invariant is checked on every node after every height. 1,000
// heights by default; 10,000 with -tags long.
func TestDeterminism10k(t *testing.T) {
	start := time.Now()
	net := multinode.New(t, 4)
	w := multinode.NewWorkload(net, 42)
	for h := 0; h < determinismHeights; h++ {
		w.Step()
		for _, nd := range net.Nodes {
			require.NoError(t, nd.App.CheckInvariants(nd.App.NewContext(true)), "node %d height %d", nd.Index, net.Height)
		}
	}
	t.Logf("%d heights in %s: %+v", determinismHeights, time.Since(start), w.Stats)
	require.Positive(t, w.Stats.Liquidations, "at least one liquidation")
	require.Positive(t, w.Stats.StaleBlocks, "at least one STALE period")
	require.Positive(t, w.Stats.Txs)
}
