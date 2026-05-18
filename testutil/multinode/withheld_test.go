//go:build devcredit

package multinode_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	sdkmath "cosmossdk.io/math"

	"github.com/OWNER/keystone/app/protocol"
	"github.com/OWNER/keystone/app/protocol/vetypes"
	"github.com/OWNER/keystone/sidecar/price"
	"github.com/OWNER/keystone/testutil/multinode"
	liqtypes "github.com/OWNER/keystone/x/liquidation/types"
	oracletypes "github.com/OWNER/keystone/x/oracle/types"
	vaulttypes "github.com/OWNER/keystone/x/vault/types"
)

func spotAndStatus(t *testing.T, net *multinode.Net) (string, oracletypes.PriceStatus) {
	t.Helper()
	ps, err := net.Ref().App.OracleKeeper.GetPriceState(net.Context())
	require.NoError(t, err)
	return ps.SpotMicroUsd.String(), ps.Status
}

func steps(net *multinode.Net, n int) {
	for i := 0; i < n; i++ {
		net.Step(multinode.StepOpts{})
	}
}

// 1 of 4 nodes sends no oracle payload: 3/4 > 2/3, so the price still updates.
func TestOneNodeWithholdsPrice(t *testing.T) {
	net := multinode.New(t, 4)
	net.SetAllPrices(100_000)
	steps(net, 3)
	for i, nd := range net.Nodes {
		if i == 3 {
			nd.Price.Down()
		} else {
			nd.Price.Set(101_000)
		}
	}
	steps(net, 3)
	spot, status := spotAndStatus(t, net)
	require.Equal(t, "101000000000", spot)
	require.Equal(t, oracletypes.PRICE_STATUS_ACTIVE, status)
}

// 2 of 4 send none: no update; STALE after 20 blocks; liquidations pause even for a
// vault that is liquidatable at the held price; recovery when they return.
func TestTwoNodesWithholdPrice(t *testing.T) {
	net := multinode.New(t, 4)
	net.SetAllPrices(90_000)
	steps(net, 3)
	user := net.Users[0]
	r := net.Step(multinode.StepOpts{Txs: [][]byte{net.SignTx(user.Key, &vaulttypes.MsgOpenVault{Owner: user.Addr.String()})}})
	require.Equal(t, uint32(0), r.Finalize[0].TxResults[1].Code)
	net.Step(multinode.StepOpts{Txs: [][]byte{net.SignTx(net.Admin.Key, &vaulttypes.MsgDevCreditCollateral{
		Admin: net.Admin.Addr.String(), VaultId: 1, Sat: sdkmath.NewInt(50_000_000)})}})
	r = net.Step(multinode.StepOpts{Txs: [][]byte{net.SignTx(user.Key, &vaulttypes.MsgMint{
		Owner: user.Addr.String(), VaultId: 1, Amount: sdkmath.NewInt(29_500_000_000)})}})
	require.Equal(t, uint32(0), r.Finalize[0].TxResults[1].Code, r.Finalize[0].TxResults[1].Log)

	// Nodes 0 and 1 stop reporting; nodes 2 and 3 report a different price.
	net.Nodes[0].Price.Down()
	net.Nodes[1].Price.Down()
	net.Nodes[2].Price.Set(95_000)
	net.Nodes[3].Price.Set(95_000)
	heldSpot, _ := spotAndStatus(t, net)
	staleAt := 0
	for i := 1; i <= 25; i++ {
		net.Step(multinode.StepOpts{})
		spot, status := spotAndStatus(t, net)
		require.Equal(t, heldSpot, spot, "no update with 2/4 reporting")
		if status == oracletypes.PRICE_STATUS_STALE && staleAt == 0 {
			staleAt = i
		}
	}
	require.GreaterOrEqual(t, staleAt, 20, "STALE only after 20 blocks without an update")
	require.LessOrEqual(t, staleAt, 22)

	// Ten years of fees in one block make the vault liquidatable at the held spot, yet
	// nothing is liquidated while STALE.
	net.Time = net.Time.Add(10 * 365 * 24 * time.Hour)
	for i := 0; i < 3; i++ {
		r := net.Step(multinode.StepOpts{})
		require.Empty(t, eventAttr(r.Finalize[0].Events, liqtypes.EventTypeVaultLiquidated, liqtypes.AttributeKeyID))
	}
	v, err := net.Ref().App.VaultKeeper.GetVault(net.Context(), 1)
	require.NoError(t, err)
	liq, err := net.Ref().App.VaultKeeper.IsLiquidatable(net.Context(), v, sdkmath.NewInt(90_000_000_000))
	require.NoError(t, err)
	require.True(t, liq, "liquidatable at the held price")

	// The two nodes return: the price updates, status recovers and the vault is liquidated.
	net.SetAllPrices(90_000)
	liquidated := false
	for i := 0; i < 5 && !liquidated; i++ {
		r := net.Step(multinode.StepOpts{})
		liquidated = eventAttr(r.Finalize[0].Events, liqtypes.EventTypeVaultLiquidated, liqtypes.AttributeKeyID) == "1"
	}
	_, status := spotAndStatus(t, net)
	require.NotEqual(t, oracletypes.PRICE_STATUS_STALE, status)
	require.True(t, liquidated, "liquidation resumes after recovery")
}

type stuckSource struct{}

func (stuckSource) Snapshot() (*vetypes.OraclePayload, bool) {
	time.Sleep(time.Second)
	return &vetypes.OraclePayload{PriceMicroUsd: 1_000_000_000_000, NumSources: 3}, true
}

// A node's price sidecar hangs: ExtendVote gives up at the 200 ms deadline, its VE is
// minimal (no oracle payload), and nothing else changes.
func TestExtendVoteTimeout(t *testing.T) {
	net := multinode.New(t, 4)
	net.SetAllPrices(100_000)
	steps(net, 3)
	slow := net.Nodes[2]
	slow.App.SetPriceSource(price.WithDeadline(stuckSource{}, price.ExtendVoteDeadline))
	for i, nd := range net.Nodes {
		if i != 2 {
			nd.Price.Set(100_500)
		}
	}
	steps(net, 3)
	spot, status := spotAndStatus(t, net)
	require.Equal(t, "100500000000", spot, "the 3 other nodes still update the price")
	require.Equal(t, oracletypes.PRICE_STATUS_ACTIVE, status)

	// The slow node's VE in the last commit carries no oracle payload.
	ext := net.LastExtendedCommit()
	found := false
	for _, v := range ext.Votes {
		if string(v.Validator.Address) == string(slow.ConsAddr()) {
			ve, err := protocol.DecodeVE(v.VoteExtension, net.Height)
			require.NoError(t, err)
			require.Nil(t, ve.Oracle, "minimal VE")
			found = true
		}
	}
	require.True(t, found)
}
