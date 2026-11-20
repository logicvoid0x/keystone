package app_test

import (
	"testing"

	abci "github.com/cometbft/cometbft/abci/types"
	"github.com/stretchr/testify/require"

	sdkmath "cosmossdk.io/math"

	"github.com/logicvoid0x/keystone/testutil/apptest"
	custodykeeper "github.com/logicvoid0x/keystone/x/custody/keeper"
	custodytypes "github.com/logicvoid0x/keystone/x/custody/types"
	liqtypes "github.com/logicvoid0x/keystone/x/liquidation/types"
	oracletypes "github.com/logicvoid0x/keystone/x/oracle/types"
	vaulttypes "github.com/logicvoid0x/keystone/x/vault/types"
)

func kusdAmt(v int64) sdkmath.Int { return sdkmath.NewInt(v * 1_000_000) }

func requireOK(t *testing.T, b apptest.Block) {
	t.Helper()
	for i, r := range b.Finalize.TxResults[1:] {
		require.Equal(t, uint32(0), r.Code, "height %d tx %d: %s", b.Height, i+1, r.Log)
	}
}

func findEvent(evs []abci.Event, typ string) (map[string]string, bool) {
	for _, e := range evs {
		if e.Type == typ {
			m := map[string]string{}
			for _, a := range e.Attributes {
				m[a.Key] = a.Value
			}
			return m, true
		}
	}
	return nil, false
}

// TestM1Flow is the demo-M1 scenario in process (acceptance check 3 without the
// mockfeed process): price flows through VEs, a 24% drop liquidates vault 1 through
// the pool in the block where spot first reaches 77,378.09375, and STALE pauses mints.
func TestM1Flow(t *testing.T) {
	c := apptest.New(t, 1)
	A, B := c.Users[0], c.Users[1]
	c.Price.Set(100_000)
	for i := 0; i < 3; i++ {
		c.NextBlock(nil)
	}
	ps, err := c.App.OracleKeeper.GetPriceState(c.Context())
	require.NoError(t, err)
	require.Equal(t, oracletypes.PRICE_STATUS_ACTIVE, ps.Status)
	require.Equal(t, sdkmath.NewInt(100_000_000_000), ps.SpotMicroUsd)

	// 1. A opens vault 1 and deposits 0.5 BTC (fake regtest chain); A mints 30,000 KUSD.
	requireOK(t, c.NextBlock([][]byte{c.SignTx(A.Key, &vaulttypes.MsgOpenVault{Owner: A.Addr.String()})}))
	c.Fund(1, 50_000_000)
	require.Equal(t, sdkmath.NewInt(50_000_000), c.Collateral(1))
	requireOK(t, c.NextBlock([][]byte{c.SignTx(A.Key, &vaulttypes.MsgMint{Owner: A.Addr.String(), VaultId: 1, Amount: kusdAmt(30_000)})}))

	// 2. B opens vault 2; deposits 2 BTC; mints 100,000 KUSD; deposits 50,000 into the pool.
	requireOK(t, c.NextBlock([][]byte{c.SignTx(B.Key, &vaulttypes.MsgOpenVault{Owner: B.Addr.String()})}))
	c.Fund(2, 200_000_000)
	requireOK(t, c.NextBlock([][]byte{
		c.SignTx(B.Key, &vaulttypes.MsgMint{Owner: B.Addr.String(), VaultId: 2, Amount: kusdAmt(100_000)}),
		c.SignTx(B.Key, &liqtypes.MsgDepositPool{Depositor: B.Addr.String(), Amount: kusdAmt(50_000)}),
	}))

	// 3–4. Feed drops to $76,000: the spot walks down 5% per block; vault 1 is
	// liquidated through the pool in the block where spot first reaches 77,378.09375.
	c.Price.Set(76_000)
	want := []int64{95_000_000_000, 90_250_000_000, 85_737_500_000, 81_450_625_000, 77_378_093_750, 76_000_000_000}
	var liqAt int64
	var liq map[string]string
	c.NextBlock(nil) // this block's tx0 carries the previous height's $100,000 VEs
	for _, w := range want {
		b := c.NextBlock(nil)
		ps, err := c.App.OracleKeeper.GetPriceState(c.Context())
		require.NoError(t, err)
		require.Equal(t, sdkmath.NewInt(w), ps.SpotMicroUsd, "height %d", b.Height)
		if ev, ok := findEvent(b.Finalize.Events, liqtypes.EventTypeVaultLiquidated); ok {
			require.Zero(t, liqAt, "liquidated once")
			liqAt, liq = w, ev
		}
	}
	require.Equal(t, int64(77_378_093_750), liqAt)
	require.Equal(t, "1", liq[liqtypes.AttributeKeyID])
	require.Equal(t, "POOL", liq[liqtypes.AttributeKeyMode])

	// 5. Vault 1 debt is 0; B's claimable BTC equals the seized amount (within rounding).
	v1, err := c.App.VaultKeeper.GetVault(c.Context(), 1)
	require.NoError(t, err)
	debt, err := c.App.VaultKeeper.Debt(c.Context(), v1)
	require.NoError(t, err)
	require.True(t, debt.IsZero())
	requireOK(t, c.NextBlock([][]byte{c.SignTx(B.Key, &liqtypes.MsgWithdrawPool{Depositor: B.Addr.String(), Amount: sdkmath.ZeroInt()})}))
	q := custodykeeper.NewQuerier(c.App.CustodyKeeper)
	cl, err := q.Claimable(c.Context(), &custodytypes.QueryClaimableRequest{Address: B.Addr.String()})
	require.NoError(t, err)
	seized, ok := sdkmath.NewIntFromString(liq[liqtypes.AttributeKeySeizedSat])
	require.True(t, ok)
	require.InDelta(t, seized.Int64(), cl.ClaimableSat.Int64(), 1)

	// 6. Feed down: after 20 blocks STALE and MsgMint is rejected; back up: ACTIVE.
	c.Price.Down()
	for i := 0; i < 21; i++ {
		c.NextBlock(nil)
	}
	ps, _ = c.App.OracleKeeper.GetPriceState(c.Context())
	require.Equal(t, oracletypes.PRICE_STATUS_STALE, ps.Status)
	b := c.NextBlock([][]byte{c.SignTx(B.Key, &vaulttypes.MsgMint{Owner: B.Addr.String(), VaultId: 2, Amount: kusdAmt(1_000)})})
	require.NotEqual(t, uint32(0), b.Finalize.TxResults[1].Code)
	require.Contains(t, b.Finalize.TxResults[1].Log, vaulttypes.ErrPriceStatus.Error())

	c.Price.Set(76_000)
	active := false
	for i := 0; i < 120 && !active; i++ {
		c.NextBlock(nil)
		ps, _ = c.App.OracleKeeper.GetPriceState(c.Context())
		active = ps.Status == oracletypes.PRICE_STATUS_ACTIVE
	}
	require.True(t, active, "status returns to ACTIVE (status %s)", ps.Status)
}
