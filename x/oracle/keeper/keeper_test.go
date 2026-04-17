package keeper_test

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/cometbft/cometbft/crypto/ed25519"
	"github.com/stretchr/testify/require"

	sdkmath "cosmossdk.io/math"

	"github.com/cosmos/cosmos-sdk/runtime"
	storetypes "github.com/cosmos/cosmos-sdk/store/v2/types"
	"github.com/cosmos/cosmos-sdk/testutil"
	sdk "github.com/cosmos/cosmos-sdk/types"
	moduletestutil "github.com/cosmos/cosmos-sdk/types/module/testutil"

	"github.com/OWNER/keystone/app/protocol/vetypes"
	"github.com/OWNER/keystone/x/oracle/keeper"
	"github.com/OWNER/keystone/x/oracle/types"
	poatypes "github.com/OWNER/keystone/x/poa/types"
)

const usd = 1_000_000

var (
	admin       = sdk.AccAddress(bytes.Repeat([]byte{0xAD}, 20)).String()
	stranger    = sdk.AccAddress(bytes.Repeat([]byte{0x05}, 20)).String()
	errNotAdmin = errors.New("not admin")
)

type fakeAdmin struct{}

func (fakeAdmin) CheckAdmin(_ context.Context, signer string) error {
	if signer != admin {
		return errNotAdmin
	}
	return nil
}

// fakeVals is a fixed set of 4 operators at power 1.
type fakeVals struct{ ops []poatypes.Operator }

func newVals() fakeVals {
	var ops []poatypes.Operator
	for i := byte(1); i <= 4; i++ {
		ops = append(ops, poatypes.Operator{
			OperatorAddress: sdk.AccAddress(bytes.Repeat([]byte{i}, 20)).String(),
			ConsensusPubkey: ed25519.GenPrivKeyFromSecret([]byte{'o', i}).PubKey().Bytes(),
			Power:           1,
		})
	}
	return fakeVals{ops}
}

func (f fakeVals) ActiveValidators(context.Context) ([]poatypes.Operator, error) { return f.ops, nil }
func (f fakeVals) TotalPower(context.Context) (int64, error)                     { return int64(len(f.ops)), nil }

func (f fakeVals) cons(i int) []byte { return f.ops[i].ConsAddress() }

type fixture struct {
	ctx  sdk.Context
	k    *keeper.Keeper
	vals fakeVals
	p    types.Params
}

func setup(t *testing.T) *fixture {
	t.Helper()
	key := storetypes.NewKVStoreKey(types.StoreKey)
	tctx := testutil.DefaultContextWithDB(t, key, storetypes.NewTransientStoreKey("transient_test"))
	vals := newVals()
	k := keeper.NewKeeper(moduletestutil.MakeTestEncodingConfig().Codec, runtime.NewKVStoreService(key), fakeAdmin{}, vals)
	require.NoError(t, k.InitGenesis(tctx.Ctx, types.DefaultGenesis()))
	return &fixture{ctx: tctx.Ctx.WithBlockHeight(1).WithEventManager(sdk.NewEventManager()), k: k, vals: vals, p: types.DefaultParams()}
}

// block applies one aggregate at the next height. prices[i] is operator i's report in
// USD (0 = no payload); nil prices means no operator is in the commit.
func (f *fixture) block(t *testing.T, prices []uint64) types.PriceAggregate {
	t.Helper()
	f.ctx = f.ctx.WithBlockHeight(f.ctx.BlockHeight() + 1).WithEventManager(sdk.NewEventManager())
	ps, err := f.k.GetPriceState(f.ctx)
	require.NoError(t, err)
	var reports []types.Report
	for i, pr := range prices {
		r := types.Report{ConsAddr: f.vals.cons(i), Power: 1}
		if pr > 0 {
			r.Payload = &vetypes.OraclePayload{PriceMicroUsd: pr * usd, NumSources: 3}
		}
		reports = append(reports, r)
	}
	agg := types.AggregatePrice(reports, 4, ps.SpotMicroUsd, f.p)
	require.NoError(t, f.k.ApplyAggregate(f.ctx, agg))
	return agg
}

func (f *fixture) state(t *testing.T) types.PriceState {
	ps, err := f.k.GetPriceState(f.ctx)
	require.NoError(t, err)
	return ps
}

func hasEvent(ctx sdk.Context, typ string) bool {
	for _, e := range ctx.EventManager().Events() {
		if e.Type == typ {
			return true
		}
	}
	return false
}

func all(usdPrice uint64) []uint64 { return []uint64{usdPrice, usdPrice, usdPrice, usdPrice} }

func TestGenesisIsStale(t *testing.T) {
	f := setup(t)
	ps := f.state(t)
	require.Equal(t, types.PRICE_STATUS_STALE, ps.Status)
	_, ok, err := f.k.LiquidationPrice(f.ctx)
	require.NoError(t, err)
	require.False(t, ok)
	_, ok, err = f.k.CollateralPrice(f.ctx)
	require.NoError(t, err)
	require.False(t, ok)
	active, err := f.k.IsActiveForLiquidation(f.ctx)
	require.NoError(t, err)
	require.False(t, active)

	gs, err := f.k.ExportGenesis(f.ctx)
	require.NoError(t, err)
	require.Equal(t, types.DefaultGenesis(), gs)
}

func TestFirstUpdateActivates(t *testing.T) {
	f := setup(t)
	f.block(t, all(100_000))
	ps := f.state(t)
	require.Equal(t, sdkmath.NewInt(100_000*usd), ps.SpotMicroUsd)
	require.Equal(t, sdkmath.NewInt(100_000*usd), ps.TwapMicroUsd)
	require.Equal(t, types.PRICE_STATUS_ACTIVE, ps.Status)
	require.True(t, hasEvent(f.ctx, types.EventTypePriceUpdated))
	require.True(t, hasEvent(f.ctx, types.EventTypePriceStatus))

	lp, ok, err := f.k.LiquidationPrice(f.ctx)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, sdkmath.NewInt(100_000*usd), lp)
}

func TestStaleAfterStaleBlocks(t *testing.T) {
	f := setup(t)
	f.block(t, all(100_000))
	for i := 1; i < int(f.p.StaleBlocks); i++ {
		f.block(t, nil)
		require.NotEqual(t, types.PRICE_STATUS_STALE, f.state(t).Status, "block %d", i)
	}
	f.block(t, nil) // 20th block without an update
	require.Equal(t, types.PRICE_STATUS_STALE, f.state(t).Status)
	active, _ := f.k.IsActiveForLiquidation(f.ctx)
	require.False(t, active)

	f.block(t, all(100_000))
	require.Equal(t, types.PRICE_STATUS_ACTIVE, f.state(t).Status)
}

func TestTwapAndVolatile(t *testing.T) {
	f := setup(t)
	f.block(t, all(100_000))
	f.block(t, all(100_000))
	// Walk down with the clamp: TWAP lags, then status turns VOLATILE.
	var sawVolatile bool
	for i := 0; i < 8; i++ {
		f.block(t, all(60_000))
		ps := f.state(t)
		if ps.Status == types.PRICE_STATUS_VOLATILE {
			sawVolatile = true
			diff := ps.TwapMicroUsd.Sub(ps.SpotMicroUsd)
			require.True(t, diff.MulRaw(10_000).GT(ps.TwapMicroUsd.MulRaw(int64(f.p.VolatileBps))))
		}
	}
	require.True(t, sawVolatile)

	// TWAP over filled entries; a block without an update repeats the last spot.
	resp, err := keeper.NewQuerier(f.k).Twap(f.ctx, &types.QueryTwapRequest{})
	require.NoError(t, err)
	require.Equal(t, uint32(10), resp.Blocks)
	f.block(t, nil)
	resp, err = keeper.NewQuerier(f.k).Twap(f.ctx, &types.QueryTwapRequest{})
	require.NoError(t, err)
	require.Equal(t, uint32(11), resp.Blocks)
}

func TestTwapRingWraps(t *testing.T) {
	f := setup(t)
	f.p.TwapBlocks = 3
	require.NoError(t, f.k.Params.Set(f.ctx, f.p))
	f.block(t, all(100_000)) // 100
	f.block(t, all(104_000)) // 104 (clamp allows +5%)
	f.block(t, all(108_000)) // 108
	f.block(t, all(112_000)) // ring now 104, 108, 112
	ps := f.state(t)
	require.Equal(t, sdkmath.NewInt(108_000*usd), ps.TwapMicroUsd)
	meta, err := f.k.TwapMeta.Get(f.ctx)
	require.NoError(t, err)
	require.Equal(t, uint32(3), meta.Count)
}

func TestMisses(t *testing.T) {
	f := setup(t)
	q := keeper.NewQuerier(f.k)
	misses := func(i int) uint32 {
		r, err := q.MissCounter(f.ctx, &types.QueryMissCounterRequest{Validator: f.vals.ops[i].OperatorAddress})
		require.NoError(t, err)
		return r.Misses
	}
	// Op 3 reports nothing; op 2 is a 3% outlier; ops 0 and 1 are fine.
	f.block(t, []uint64{100_000, 100_000, 103_000, 0})
	require.Equal(t, uint32(0), misses(0))
	require.Equal(t, uint32(0), misses(1))
	require.Equal(t, uint32(1), misses(2))
	require.Equal(t, uint32(1), misses(3))

	// Not in the commit: not a miss.
	f.block(t, nil)
	require.Equal(t, uint32(1), misses(3))

	_, err := q.MissCounter(f.ctx, &types.QueryMissCounterRequest{Validator: stranger})
	require.Error(t, err)
}

func TestMissThresholdResetsWindow(t *testing.T) {
	f := setup(t)
	f.p.MissWindow, f.p.MissAlert = 10, 3
	require.NoError(t, f.k.Params.Set(f.ctx, f.p))
	for i := 0; i < 3; i++ {
		f.block(t, []uint64{100_000, 100_000, 100_000, 0})
		require.False(t, hasEvent(f.ctx, types.EventTypeOracleMissThreshold))
	}
	f.block(t, []uint64{100_000, 100_000, 100_000, 0}) // 4th miss > MissAlert
	require.True(t, hasEvent(f.ctx, types.EventTypeOracleMissThreshold))
	n, err := f.k.Misses(f.ctx, f.vals.cons(3))
	require.NoError(t, err)
	require.Equal(t, uint32(0), n, "window resets")

	// The window slides: a slot is overwritten after MissWindow blocks.
	for i := 0; i < 10; i++ {
		f.block(t, []uint64{100_000, 100_000, 100_000, 0})
	}
	n, err = f.k.Misses(f.ctx, f.vals.cons(3))
	require.NoError(t, err)
	require.LessOrEqual(t, n, f.p.MissAlert)
}

func TestCollateralPriceIsMinOfSpotAndTwap(t *testing.T) {
	f := setup(t)
	f.block(t, all(100_000))
	f.block(t, all(110_000)) // spot 105,000; TWAP 102,500
	cp, ok, err := f.k.CollateralPrice(f.ctx)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, sdkmath.NewInt(102_500*usd), cp)
	lp, _, _ := f.k.LiquidationPrice(f.ctx)
	require.Equal(t, sdkmath.NewInt(105_000*usd), lp)
}

func TestQueries(t *testing.T) {
	f := setup(t)
	f.block(t, all(100_000))
	q := keeper.NewQuerier(f.k)
	pr, err := q.Price(f.ctx, &types.QueryPriceRequest{})
	require.NoError(t, err)
	require.Equal(t, sdkmath.NewInt(100_000*usd), pr.Price.SpotMicroUsd)
	pp, err := q.Params(f.ctx, &types.QueryParamsRequest{})
	require.NoError(t, err)
	require.Equal(t, f.p, pp.Params)
}

func TestInitGenesisRejectsInvalidParams(t *testing.T) {
	f := setup(t)
	require.ErrorIs(t, f.k.InitGenesis(f.ctx, &types.GenesisState{}), types.ErrInvalidParams)
}

func TestUpdateParams(t *testing.T) {
	f := setup(t)
	f.block(t, all(100_000))
	ms := keeper.NewMsgServerImpl(f.k)
	next := types.DefaultParams()
	next.TwapBlocks = 50

	_, err := ms.UpdateParams(f.ctx, &types.MsgUpdateParams{Admin: stranger, Params: next})
	require.ErrorIs(t, err, errNotAdmin)
	_, err = ms.UpdateParams(f.ctx, &types.MsgUpdateParams{Admin: admin, Params: types.Params{}})
	require.ErrorIs(t, err, types.ErrInvalidParams)
	_, err = ms.UpdateParams(f.ctx, &types.MsgUpdateParams{Admin: "nope", Params: next})
	require.ErrorIs(t, err, types.ErrInvalidAddress)

	_, err = ms.UpdateParams(f.ctx, &types.MsgUpdateParams{Admin: admin, Params: next})
	require.NoError(t, err)
	got, err := f.k.GetParams(f.ctx)
	require.NoError(t, err)
	require.Equal(t, next, got)
	meta, err := f.k.TwapMeta.Get(f.ctx)
	require.NoError(t, err)
	require.Equal(t, uint32(0), meta.Count, "TWAP ring cleared when TwapBlocks changes")
}
