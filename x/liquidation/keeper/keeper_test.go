package keeper_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	sdkmath "cosmossdk.io/math"

	"github.com/cosmos/cosmos-sdk/runtime"
	storetypes "github.com/cosmos/cosmos-sdk/store/v2/types"
	"github.com/cosmos/cosmos-sdk/testutil"
	sdk "github.com/cosmos/cosmos-sdk/types"
	moduletestutil "github.com/cosmos/cosmos-sdk/types/module/testutil"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"

	"github.com/logicvoid0x/keystone/testutil/fakebank"
	custodykeeper "github.com/logicvoid0x/keystone/x/custody/keeper"
	custodytypes "github.com/logicvoid0x/keystone/x/custody/types"
	"github.com/logicvoid0x/keystone/x/liquidation/keeper"
	"github.com/logicvoid0x/keystone/x/liquidation/types"
	oracletypes "github.com/logicvoid0x/keystone/x/oracle/types"
	stablecoinkeeper "github.com/logicvoid0x/keystone/x/stablecoin/keeper"
	stablecointypes "github.com/logicvoid0x/keystone/x/stablecoin/types"
	vaultkeeper "github.com/logicvoid0x/keystone/x/vault/keeper"
	vaulttypes "github.com/logicvoid0x/keystone/x/vault/types"
)

const usd = 1_000_000

var (
	admin       = sdk.AccAddress(bytes.Repeat([]byte{0xAD}, 20)).String()
	stranger    = sdk.AccAddress(bytes.Repeat([]byte{0x05}, 20)).String()
	alice       = sdk.AccAddress(bytes.Repeat([]byte{0xA1}, 20))
	bob         = sdk.AccAddress(bytes.Repeat([]byte{0xB0}, 20))
	carol       = sdk.AccAddress(bytes.Repeat([]byte{0xC0}, 20))
	errNotAdmin = errors.New("not admin")
)

type fakeAdmin struct{}

func (fakeAdmin) CheckAdmin(_ context.Context, signer string) error {
	if signer != admin {
		return errNotAdmin
	}
	return nil
}

// fakeOracle serves both x/vault and x/liquidation.
type fakeOracle struct {
	spot   sdkmath.Int
	status oracletypes.PriceStatus
}

func (o *fakeOracle) GetPriceState(context.Context) (oracletypes.PriceState, error) {
	return oracletypes.PriceState{SpotMicroUsd: o.spot, TwapMicroUsd: o.spot, Status: o.status}, nil
}
func (o *fakeOracle) CollateralPrice(context.Context) (sdkmath.Int, bool, error) {
	return o.spot, o.status != oracletypes.PRICE_STATUS_STALE, nil
}
func (o *fakeOracle) LiquidationPrice(context.Context) (sdkmath.Int, bool, error) {
	return o.spot, o.status != oracletypes.PRICE_STATUS_STALE, nil
}
func (o *fakeOracle) IsActiveForLiquidation(context.Context) (bool, error) {
	return o.status != oracletypes.PRICE_STATUS_STALE, nil
}

type fixture struct {
	ctx    sdk.Context
	k      *keeper.Keeper
	vault  *vaultkeeper.Keeper
	sc     *stablecoinkeeper.Keeper
	cust   *custodykeeper.Keeper
	bank   *fakebank.Bank
	oracle *fakeOracle
}

func setup(t *testing.T) *fixture {
	t.Helper()
	keys := storetypes.NewKVStoreKeys(types.StoreKey, vaulttypes.StoreKey, stablecointypes.StoreKey, custodytypes.StoreKey)
	ctx := testutil.DefaultContextWithKeys(keys, nil, nil).WithBlockHeight(1).
		WithBlockTime(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)).WithEventManager(sdk.NewEventManager())
	cdc := moduletestutil.MakeTestEncodingConfig().Codec
	bank := fakebank.New(map[string][]string{
		stablecointypes.ModuleName: {authtypes.Minter, authtypes.Burner},
		types.ModuleName:           {authtypes.Burner},
	})
	or := &fakeOracle{spot: sdkmath.NewInt(100_000 * usd), status: oracletypes.PRICE_STATUS_ACTIVE}
	sc := stablecoinkeeper.NewKeeper(cdc, runtime.NewKVStoreService(keys[stablecointypes.StoreKey]), fakeAdmin{}, bank)
	cust := custodykeeper.NewKeeper(cdc, runtime.NewKVStoreService(keys[custodytypes.StoreKey]), fakeAdmin{}, nil)
	vk := vaultkeeper.NewKeeper(cdc, runtime.NewKVStoreService(keys[vaulttypes.StoreKey]), fakeAdmin{}, or, sc, cust)
	k := keeper.NewKeeper(cdc, runtime.NewKVStoreService(keys[types.StoreKey]), fakeAdmin{}, or, vk, sc, cust, bank)
	require.NoError(t, sc.InitGenesis(ctx, stablecointypes.DefaultGenesis()))
	cp := custodytypes.DefaultParams()
	cp.TreasuryCapSat = custodytypes.MaxTreasuryCapSat // room for big test vaults
	require.NoError(t, cust.InitGenesis(ctx, &custodytypes.GenesisState{Params: cp}))
	require.NoError(t, vk.InitGenesis(ctx, vaulttypes.DefaultGenesis()))
	require.NoError(t, k.InitGenesis(ctx, types.DefaultGenesis()))
	return &fixture{ctx: ctx, k: k, vault: vk, sc: sc, cust: cust, bank: bank, oracle: or}
}

func n(v int64) sdkmath.Int { return sdkmath.NewInt(v) }

func kusd(v int64) sdkmath.Int { return n(v * 1_000_000) }

func (f *fixture) vaultWithDebt(t *testing.T, owner sdk.AccAddress, sat int64, debt sdkmath.Int) uint64 {
	t.Helper()
	id, _, err := f.vault.OpenVault(f.ctx, &vaulttypes.MsgOpenVault{Owner: owner.String()})
	require.NoError(t, err)
	_, err = f.vault.CreditCollateral(f.ctx, id, n(sat))
	require.NoError(t, err)
	if debt.IsPositive() {
		require.NoError(t, f.vault.Mint(f.ctx, &vaulttypes.MsgMint{Owner: owner.String(), VaultId: id, Amount: debt}))
	}
	return id
}

// fund gives addr amt KUSD by minting against a large vault.
func (f *fixture) fund(t *testing.T, addr sdk.AccAddress, amt sdkmath.Int) {
	t.Helper()
	f.vaultWithDebt(t, addr, 1_000_000_000, amt)
}

func (f *fixture) deposit(t *testing.T, addr sdk.AccAddress, amt sdkmath.Int) {
	t.Helper()
	require.NoError(t, f.k.DepositPool(f.ctx, &types.MsgDepositPool{Depositor: addr.String(), Amount: amt}))
}

func (f *fixture) liquidate(t *testing.T, price sdkmath.Int) types.LiquidationBatch {
	t.Helper()
	f.oracle.spot = price
	batch, err := f.k.Select(f.ctx, price)
	require.NoError(t, err)
	require.NoError(t, f.k.Execute(f.ctx, batch, price))
	return batch
}

func (f *fixture) nextBlock() {
	f.ctx = f.ctx.WithBlockHeight(f.ctx.BlockHeight() + 1).WithEventManager(sdk.NewEventManager())
}

func eventAttr(ctx sdk.Context, typ, key string) string {
	for _, e := range ctx.EventManager().Events() {
		if e.Type == typ {
			for _, a := range e.Attributes {
				if a.Key == key {
					return a.Value
				}
			}
		}
	}
	return ""
}

// Required vector: debt 30048868155, P = 76,000,000,000, penalty 1000 bps →
// seize 43491783 sat; owner keeps 6508217 sat.
func TestPoolLiquidationVector(t *testing.T) {
	f := setup(t)
	f.fund(t, bob, kusd(200_000))
	f.deposit(t, bob, kusd(200_000))
	id := f.vaultWithDebt(t, alice, 50_000_000, kusd(30_000))
	f.ctx = f.ctx.WithBlockTime(f.ctx.BlockTime().Add(30 * 24 * time.Hour))
	require.NoError(t, f.vault.Drip(f.ctx, f.ctx.BlockTime()))
	v, _ := f.vault.GetVault(f.ctx, id)
	debt, _ := f.vault.Debt(f.ctx, v)
	require.Equal(t, "30048868155", debt.String())
	supplyBefore := f.sc.Supply(f.ctx)

	batch := f.liquidate(t, n(76_000_000_000))
	require.Equal(t, []types.Entry{{VaultId: id, Mode: types.MODE_POOL}}, batch.Entries)
	require.Equal(t, "POOL", eventAttr(f.ctx, types.EventTypeVaultLiquidated, types.AttributeKeyMode))
	require.Equal(t, "43491783", eventAttr(f.ctx, types.EventTypeVaultLiquidated, types.AttributeKeySeizedSat))

	v, _ = f.vault.GetVault(f.ctx, id)
	require.True(t, v.Art.IsZero())
	require.Equal(t, "6508217", v.CollateralSat.String())
	require.Equal(t, supplyBefore.Sub(debt), f.sc.Supply(f.ctx), "the pool burns the debt")

	c, g, err := f.k.Position(f.ctx, bob)
	require.NoError(t, err)
	require.InDelta(t, kusd(200_000).Sub(debt).Int64(), c.Int64(), 1, "loss rounds up by at most 1 ukusd")
	require.True(t, c.LTE(kusd(200_000).Sub(debt)))
	require.InDelta(t, 43_491_783, g.Int64(), 1, "sole depositor gains the seized BTC (within rounding)")

	// Claim with a zero withdrawal: gain moves to custody claimable.
	w, paid, err := f.k.WithdrawPool(f.ctx, &types.MsgWithdrawPool{Depositor: bob.String(), Amount: n(0)})
	require.NoError(t, err)
	require.True(t, w.IsZero())
	require.Equal(t, g, paid)
	cl, _ := f.cust.GetClaimable(f.ctx, bob)
	require.Equal(t, g, cl)
}

func TestPoolProRata(t *testing.T) {
	f := setup(t)
	f.fund(t, bob, kusd(100_000))
	f.fund(t, carol, kusd(300_000))
	f.deposit(t, bob, kusd(100_000))
	f.deposit(t, carol, kusd(300_000))
	for i := 0; i < 3; i++ {
		f.vaultWithDebt(t, alice, 50_000_000, kusd(30_000))
	}
	f.liquidate(t, n(76_000*usd))

	cb, gb, _ := f.k.Position(f.ctx, bob)
	cc, gc, _ := f.k.Position(f.ctx, carol)
	total, _ := f.k.TotalDeposits.Get(f.ctx)
	// Naive pro-rata: bob bears 1/4 of 90,000 KUSD.
	require.InDelta(t, kusd(100_000-22_500).Int64(), cb.Int64(), 2)
	require.InDelta(t, kusd(300_000-67_500).Int64(), cc.Int64(), 2)
	require.InDelta(t, gc.Int64(), 3*gb.Int64(), 4)
	require.True(t, cb.Add(cc).LTE(total), "Σ compounded ≤ total deposits")
	require.True(t, total.Sub(cb.Add(cc)).LTE(n(2)), "difference is rounding")
	pending, _ := f.k.PendingSat.Get(f.ctx)
	require.True(t, gb.Add(gc).LTE(pending))
	require.True(t, f.k.ModuleBalance(f.ctx).GTE(total))
}

func TestScaleChangeAndEpochReset(t *testing.T) {
	f := setup(t)
	f.fund(t, bob, kusd(100_000))
	f.deposit(t, bob, kusd(100_000))
	// A vault whose debt is the pool minus 1 ukusd: P drops below 1e9 → scale change.
	id := f.vaultWithDebt(t, alice, 300_000_000, kusd(100_000).SubRaw(1))
	_ = id
	f.liquidate(t, n(40_000*usd))
	sc, _ := f.k.Scalars.Get(f.ctx)
	require.Equal(t, uint64(1), sc.Scale, "scale changed")
	require.Equal(t, uint64(0), sc.Epoch)
	c, g, err := f.k.Position(f.ctx, bob)
	require.NoError(t, err)
	require.True(t, c.LTE(n(1)), "about 1 ukusd left: %s", c)
	require.True(t, g.IsPositive(), "gain across the scale change")

	// Bob claims, then tops up; a liquidation consuming the whole pool resets the epoch.
	f.fund(t, carol, kusd(50_000))
	f.deposit(t, carol, kusd(50_000))
	f.oracle.spot = n(100_000 * usd)
	_, _, err = f.k.WithdrawPool(f.ctx, &types.MsgWithdrawPool{Depositor: bob.String(), Amount: n(0)})
	require.NoError(t, err)
	total, _ := f.k.TotalDeposits.Get(f.ctx)
	f.vaultWithDebt(t, alice, 300_000_000, total)
	f.liquidate(t, n(20_000*usd))
	sc, _ = f.k.Scalars.Get(f.ctx)
	require.Equal(t, uint64(1), sc.Epoch, "pool emptied: new epoch")
	cc, gc, _ := f.k.Position(f.ctx, carol)
	require.True(t, cc.IsZero())
	require.True(t, gc.IsPositive(), "gains from the old epoch are still payable")
	total, _ = f.k.TotalDeposits.Get(f.ctx)
	require.True(t, total.IsZero())
}

func TestAuctionFallback(t *testing.T) {
	f := setup(t)
	f.fund(t, carol, kusd(100_000))
	id := f.vaultWithDebt(t, alice, 50_000_000, kusd(30_000)) // empty pool: auction
	batch := f.liquidate(t, n(76_000*usd))
	require.Equal(t, types.MODE_AUCTION, batch.Entries[0].Mode)
	sin, _ := f.sc.GetSin(f.ctx)
	require.Equal(t, kusd(30_000), sin)
	v, _ := f.vault.GetVault(f.ctx, id)
	require.True(t, v.CollateralSat.IsZero())

	a, err := f.k.GetAuction(f.ctx, 1)
	require.NoError(t, err)
	require.Equal(t, kusd(33_000), a.Tab)
	require.Equal(t, n(91_200*usd), a.StartPrice, "spot × 1.20")

	p := types.DefaultParams()
	f.nextBlock()
	require.Equal(t, n(90_288*usd), keeper.AuctionPrice(a, f.ctx.BlockHeight(), p), "× 0.99 per block")

	// Price above max: rejected.
	take := func(maxSat int64, maxPrice sdkmath.Int) (sdkmath.Int, sdkmath.Int, error) {
		return f.k.TakeCollateral(f.ctx, &types.MsgTakeCollateral{Taker: carol.String(), AuctionId: 1, MaxSat: n(maxSat), MaxPrice: maxPrice})
	}
	_, _, err = take(1_000_000, n(90_000*usd))
	require.ErrorIs(t, err, types.ErrPriceTooHigh)

	// Partial take: 0.1 BTC.
	sat, paid, err := take(10_000_000, n(100_000*usd))
	require.NoError(t, err)
	require.Equal(t, n(10_000_000), sat)
	require.Equal(t, n(9_028_800_000), paid)
	sin, _ = f.sc.GetSin(f.ctx)
	require.Equal(t, kusd(30_000).Sub(paid), sin, "proceeds burn sin first")

	// Take the rest: capped at the tab; leftover collateral returns to the vault.
	sat, paid, err = take(1_000_000_000, n(100_000*usd))
	require.NoError(t, err)
	require.Equal(t, kusd(33_000).Sub(n(9_028_800_000)), paid)
	_, err = f.k.GetAuction(f.ctx, 1)
	require.ErrorIs(t, err, types.ErrAuctionNotFound)
	sin, _ = f.sc.GetSin(f.ctx)
	require.True(t, sin.IsZero())
	require.Equal(t, kusd(3_000), f.sc.SurplusBalance(f.ctx), "penalty to surplus")
	v, _ = f.vault.GetVault(f.ctx, id)
	require.Equal(t, n(40_000_000).Sub(sat), v.CollateralSat, "leftover back to the vault")
	cl, _ := f.cust.GetClaimable(f.ctx, carol)
	require.Equal(t, n(10_000_000).Add(sat), cl)
}

func TestAuctionReset(t *testing.T) {
	f := setup(t)
	f.vaultWithDebt(t, alice, 50_000_000, kusd(30_000))
	f.liquidate(t, n(76_000*usd))
	p := types.DefaultParams()
	// 0.99^51 < 0.6: after 51 blocks the price is below 60% of the start.
	for i := 0; i < 50; i++ {
		f.nextBlock()
		require.NoError(t, f.k.Execute(f.ctx, types.LiquidationBatch{}, f.oracle.spot))
		require.Empty(t, eventAttr(f.ctx, types.EventTypeAuctionReset, types.AttributeKeyID))
	}
	f.nextBlock()
	f.oracle.spot = n(70_000 * usd)
	require.NoError(t, f.k.Execute(f.ctx, types.LiquidationBatch{}, f.oracle.spot))
	require.Equal(t, "1", eventAttr(f.ctx, types.EventTypeAuctionReset, types.AttributeKeyID))
	a, _ := f.k.GetAuction(f.ctx, 1)
	require.Equal(t, n(84_000*usd), a.StartPrice, "reset at current spot × 1.20")
	require.Equal(t, f.ctx.BlockHeight(), a.StartHeight)
	_ = p
}

func TestWithdrawBlocked(t *testing.T) {
	f := setup(t)
	f.fund(t, bob, kusd(1_000))
	f.deposit(t, bob, kusd(1_000))
	f.vaultWithDebt(t, alice, 50_000_000, kusd(30_000))
	w := func(amt sdkmath.Int) error {
		_, _, err := f.k.WithdrawPool(f.ctx, &types.MsgWithdrawPool{Depositor: bob.String(), Amount: amt})
		return err
	}
	f.oracle.spot = n(76_000 * usd)
	require.ErrorIs(t, w(kusd(1)), types.ErrWithdrawBlocked, "a vault is liquidatable")
	require.NoError(t, w(n(0)), "claiming gains is allowed")
	f.oracle.spot = n(100_000 * usd)
	f.oracle.status = oracletypes.PRICE_STATUS_STALE
	require.ErrorIs(t, w(kusd(1)), types.ErrWithdrawBlocked, "price stale")
	f.oracle.status = oracletypes.PRICE_STATUS_ACTIVE
	require.NoError(t, w(kusd(1_000)))
	has, _ := f.k.Deposits.Has(f.ctx, bob)
	require.False(t, has)
	require.ErrorIs(t, w(n(0)), types.ErrNoDeposit)
}

func TestSelectRules(t *testing.T) {
	f := setup(t)
	f.fund(t, bob, kusd(50_000))
	f.deposit(t, bob, kusd(50_000))
	a := f.vaultWithDebt(t, alice, 50_000_000, kusd(30_000))
	b := f.vaultWithDebt(t, alice, 50_000_000, kusd(29_000))
	c := f.vaultWithDebt(t, alice, 50_000_000, kusd(28_000))

	batch, err := f.k.Select(f.ctx, n(70_000*usd))
	require.NoError(t, err)
	require.Equal(t, []types.Entry{
		{VaultId: a, Mode: types.MODE_POOL},    // pool 50,000 → 20,000 left
		{VaultId: b, Mode: types.MODE_AUCTION}, // 29,000 > 20,000
		{VaultId: c, Mode: types.MODE_AUCTION},
	}, batch.Entries)

	p := types.DefaultParams()
	p.MaxPerBlock = 2
	require.NoError(t, f.k.Params.Set(f.ctx, p))
	batch, _ = f.k.Select(f.ctx, n(70_000*usd))
	require.Len(t, batch.Entries, 2, "MaxPerBlock")

	f.oracle.status = oracletypes.PRICE_STATUS_STALE
	batch, _ = f.k.Select(f.ctx, n(70_000*usd))
	require.Empty(t, batch.Entries, "STALE: empty batch")
}

// Totality: an entry that is no longer liquidatable is skipped with an event.
func TestExecuteSkipsStaleEntry(t *testing.T) {
	f := setup(t)
	f.fund(t, bob, kusd(50_000))
	f.deposit(t, bob, kusd(50_000))
	id := f.vaultWithDebt(t, alice, 50_000_000, kusd(30_000))
	batch, _ := f.k.Select(f.ctx, n(70_000*usd))
	require.NoError(t, f.k.Execute(f.ctx, batch, n(100_000*usd)))
	require.Equal(t, fmt.Sprint(id), eventAttr(f.ctx, types.EventTypeLiquidationSkipped, types.AttributeKeyID))
	v, _ := f.vault.GetVault(f.ctx, id)
	require.Equal(t, kusd(30_000), v.Art, "untouched")

	f.nextBlock()
	require.NoError(t, f.k.Execute(f.ctx, types.LiquidationBatch{Entries: []types.Entry{{VaultId: 99, Mode: types.MODE_POOL}}}, n(70_000*usd)))
	require.Equal(t, "99", eventAttr(f.ctx, types.EventTypeLiquidationSkipped, types.AttributeKeyID))
}

// Pool step 3: a vault under 100% is topped up from surplus.
func TestShortfallFromSurplus(t *testing.T) {
	f := setup(t)
	f.fund(t, bob, kusd(50_000))
	f.deposit(t, bob, kusd(50_000))
	require.NoError(t, f.sc.AccrueSurplus(f.ctx, kusd(10_000))) // 8,000 to surplus, 2,000 to stakers
	id := f.vaultWithDebt(t, alice, 50_000_000, kusd(30_000))
	f.liquidate(t, n(50_000*usd)) // collateral worth 25,000 < 30,000 debt
	v, _ := f.vault.GetVault(f.ctx, id)
	require.True(t, v.CollateralSat.IsZero(), "all collateral seized")
	require.Equal(t, kusd(3_000), f.sc.SurplusBalance(f.ctx), "surplus covered 5,000 of its 8,000")
	c, _, _ := f.k.Position(f.ctx, bob)
	require.InDelta(t, kusd(25_000).Int64(), c.Int64(), 2, "depositors lose only the collateral value")
}

func TestQueriesAndParams(t *testing.T) {
	f := setup(t)
	f.fund(t, bob, kusd(1_000))
	f.deposit(t, bob, kusd(1_000))
	q := keeper.NewQuerier(f.k)
	pool, err := q.Pool(f.ctx, &types.QueryPoolRequest{})
	require.NoError(t, err)
	require.Equal(t, kusd(1_000), pool.TotalDeposits)
	pd, err := q.PoolDeposit(f.ctx, &types.QueryPoolDepositRequest{Address: bob.String()})
	require.NoError(t, err)
	require.Equal(t, kusd(1_000), pd.Compounded)
	auctions, err := q.Auctions(f.ctx, &types.QueryAuctionsRequest{})
	require.NoError(t, err)
	require.Empty(t, auctions.Auctions)
	_, err = q.Auction(f.ctx, &types.QueryAuctionRequest{Id: 7})
	require.Error(t, err)

	gs, err := f.k.ExportGenesis(f.ctx)
	require.NoError(t, err)
	require.Equal(t, types.DefaultGenesis(), gs)
	ms := keeper.NewMsgServerImpl(f.k)
	next := types.DefaultParams()
	next.MaxPerBlock++
	_, err = ms.UpdateParams(f.ctx, &types.MsgUpdateParams{Admin: stranger, Params: next})
	require.ErrorIs(t, err, errNotAdmin)
	_, err = ms.UpdateParams(f.ctx, &types.MsgUpdateParams{Admin: admin, Params: next})
	require.NoError(t, err)
}
