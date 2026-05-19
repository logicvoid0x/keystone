package app_test

import (
	"testing"

	abci "github.com/cometbft/cometbft/abci/types"
	"github.com/prometheus/client_golang/prometheus"
	promtest "github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/require"

	"github.com/OWNER/keystone/app"
	"github.com/OWNER/keystone/app/protocol"
	"github.com/OWNER/keystone/testutil/apptest"
)

func TestMetrics(t *testing.T) {
	c := apptest.New(t, 1)
	reg := prometheus.NewRegistry()
	m, err := app.NewMetrics(reg)
	require.NoError(t, err)
	_, err = app.NewMetrics(reg)
	require.Error(t, err, "registering twice is reported, not a panic")
	c.App.EnableMetrics(m)

	c.Price.Set(100_000)
	for i := 0; i < 4; i++ {
		c.NextBlock(nil)
	}
	require.Equal(t, 100_000_000_000.0, promtest.ToFloat64(m.OracleSpot))
	require.Equal(t, 1.0, promtest.ToFloat64(m.OracleStatus), "ACTIVE")
	require.Equal(t, 10_000.0, promtest.ToFloat64(m.OracleParticipationBps))
	require.Equal(t, 0.0, promtest.ToFloat64(m.VaultCount))

	// A proposal without tx0 after the enable height is rejected and counted by reason.
	res, err := c.App.ProcessProposal(&abci.RequestProcessProposal{Height: c.Height + 1, Txs: nil})
	require.NoError(t, err)
	require.Equal(t, abci.ResponseProcessProposal_REJECT, res.Status)
	require.Equal(t, 1.0, promtest.ToFloat64(m.ProcessProposalRejects.WithLabelValues(protocol.RejectMissingTx0)))

	families, err := reg.Gather()
	require.NoError(t, err)
	names := map[string]bool{}
	for _, f := range families {
		names[f.GetName()] = true
		if f.GetName() == "keystone_extend_vote_ms" {
			require.Equal(t, uint64(4), f.GetMetric()[0].GetHistogram().GetSampleCount(), "one ExtendVote per block")
		}
	}
	for _, n := range []string{
		"keystone_extend_vote_ms", "keystone_ve_bytes", "keystone_process_proposal_ms", "keystone_process_proposal_rejects",
		"keystone_preblocker_ms", "keystone_oracle_spot", "keystone_oracle_participation_bps", "keystone_oracle_status",
		"keystone_vault_count", "keystone_debt_total", "keystone_pool_balance",
	} {
		require.True(t, names[n], n)
	}
}
