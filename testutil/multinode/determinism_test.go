package multinode_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/OWNER/keystone/app"
	"github.com/OWNER/keystone/testutil/multinode"
)

// TestDeterminism10k is spec M2-T3: 4 nodes, each with its own noisy prices, run the
// random workload; app hashes are compared every height (Step fails on a mismatch with a
// state diff) and every invariant is checked on every node after every height. 1,000
// heights by default; 10,000 with -tags long.
func TestDeterminism10k(t *testing.T) {
	start := time.Now()
	// No dealer: the epoch-1 DKG runs through transactions, and at height 250 the admin
	// removes node3, so custody moves to a new epoch among the other three (spec M5
	// acceptance 1).
	net := multinode.NewWithOptions(t, 4, multinode.Options{DKG: true})
	w := multinode.NewWorkload(net, 42)
	w.RemoveAt = 250
	for h := 0; h < determinismHeights; h++ {
		w.Step()
		// Builds with -tags invariants already check (and panic) in every node's EndBlock.
		if !app.InvariantsEnabled {
			for _, nd := range net.Nodes {
				require.NoError(t, nd.App.CheckInvariants(nd.App.NewContext(true)), "node %d height %d", nd.Index, net.Height)
			}
		}
	}
	t.Logf("%d heights in %s: %+v", determinismHeights, time.Since(start), w.Stats)
	require.Positive(t, w.Stats.Liquidations, "at least one liquidation")
	require.Positive(t, w.Stats.StaleBlocks, "at least one STALE period")
	require.Positive(t, w.Stats.Txs)
	require.Positive(t, w.Stats.Deposits, "deposits are attested and credited")
	require.Positive(t, w.Stats.BtcReorgs, "the fake chain reorgs")
	require.Positive(t, w.Stats.Confirmed, "withdrawals are signed, broadcast and confirmed")
	require.GreaterOrEqual(t, w.Stats.Epochs, 2, "epoch 1 by DKG, epoch 2 after the membership change")
	require.Positive(t, w.Stats.Sweeps, "epoch-1 UTXOs are swept to epoch 2")
}
