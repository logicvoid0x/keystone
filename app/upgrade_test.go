package app_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	simtestutil "github.com/cosmos/cosmos-sdk/testutil/sims"

	upgradetypes "github.com/cosmos/cosmos-sdk/x/upgrade/types"

	"github.com/logicvoid0x/keystone/app"
	"github.com/logicvoid0x/keystone/app/protocol"
	"github.com/logicvoid0x/keystone/app/protocol/vetypes"
	"github.com/logicvoid0x/keystone/testutil/apptest"
)

// The M6 upgrade drill in process (design §17): the admin schedules "v2" at height U;
// the binary has its handler, so x/upgrade applies it in block U without a halt; votes
// of heights up to U are VE version 1, from U+1 version 2; prices keep updating across
// the switch.
func TestUpgradeV2SwitchesVEVersion(t *testing.T) {
	// The app starts as the pre-upgrade release (no handler) and registers it at the
	// upgrade height, like a node restarted with the new binary.
	c := apptest.NewWithAppOptions(t, 1, simtestutil.AppOptionsMap{app.FlagDeferUpgradeHandlers: true})
	c.Price.Set(100_000)
	for range 3 {
		c.NextBlock(nil)
	}
	u := c.Height + 3
	plan := upgradetypes.Plan{Name: app.UpgradeV2, Height: u}
	b := c.NextBlock([][]byte{c.SignTx(c.Admin.Key, &upgradetypes.MsgSoftwareUpgrade{Authority: c.Admin.Addr.String(), Plan: plan})})
	require.Zero(t, b.Finalize.TxResults[1].Code, b.Finalize.TxResults[1].Log)

	versions := map[int64]uint32{}
	for c.Height < u+4 {
		if c.Height == u-1 {
			c.App.RegisterUpgradeHandlers()
		}
		b := c.NextBlock(nil)
		// tx0 of block h carries the votes of h−1.
		ext, ok := protocol.DecodeInjected(b.Txs[0])
		require.True(t, ok)
		var ve vetypes.VoteExtension
		require.NoError(t, ve.Unmarshal(ext.Votes[0].VoteExtension))
		versions[b.Height-1] = ve.Version
	}
	done, err := c.App.UpgradeKeeper.GetDoneHeight(c.Context(), app.UpgradeV2)
	require.NoError(t, err)
	require.Equal(t, u, done)
	require.Equal(t, uint32(1), versions[u-1])
	require.Equal(t, uint32(1), versions[u], "the upgrade block's own votes are still version 1")
	require.Equal(t, uint32(2), versions[u+1])
	require.Equal(t, uint32(2), versions[u+2])
	require.Equal(t, uint32(1), c.App.VESchedule(c.Context(), u))
	require.Equal(t, uint32(2), c.App.VESchedule(c.Context(), u+1))
	ps, err := c.App.OracleKeeper.GetPriceState(c.Context())
	require.NoError(t, err)
	require.Equal(t, c.Height, ps.LastUpdateHeight, "the price still updates from version-2 VEs")
}
