package keeper_test

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	sdkmath "cosmossdk.io/math"

	"github.com/cosmos/cosmos-sdk/runtime"
	storetypes "github.com/cosmos/cosmos-sdk/store/v2/types"
	"github.com/cosmos/cosmos-sdk/testutil"
	sdk "github.com/cosmos/cosmos-sdk/types"
	moduletestutil "github.com/cosmos/cosmos-sdk/types/module/testutil"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"

	"github.com/OWNER/keystone/testutil/fakebank"
	"github.com/OWNER/keystone/x/stablecoin/keeper"
	"github.com/OWNER/keystone/x/stablecoin/types"
)

var (
	admin       = sdk.AccAddress(bytes.Repeat([]byte{0xAD}, 20)).String()
	stranger    = sdk.AccAddress(bytes.Repeat([]byte{0x05}, 20)).String()
	alice       = sdk.AccAddress(bytes.Repeat([]byte{0xA1}, 20))
	errNotAdmin = errors.New("not admin")
)

const pool = "liquidation" // a burner module, as in the app

type fakeAdmin struct{}

func (fakeAdmin) CheckAdmin(_ context.Context, signer string) error {
	if signer != admin {
		return errNotAdmin
	}
	return nil
}

type fixture struct {
	ctx  sdk.Context
	k    *keeper.Keeper
	bank *fakebank.Bank
}

func setup(t *testing.T) *fixture {
	t.Helper()
	key := storetypes.NewKVStoreKey(types.StoreKey)
	tctx := testutil.DefaultContextWithDB(t, key, storetypes.NewTransientStoreKey("transient_test"))
	bank := fakebank.New(map[string][]string{
		types.ModuleName: {authtypes.Minter, authtypes.Burner},
		pool:             {authtypes.Burner},
	})
	k := keeper.NewKeeper(moduletestutil.MakeTestEncodingConfig().Codec, runtime.NewKVStoreService(key), fakeAdmin{}, bank)
	require.NoError(t, k.InitGenesis(tctx.Ctx, types.DefaultGenesis()))
	return &fixture{ctx: tctx.Ctx.WithBlockHeight(1).WithEventManager(sdk.NewEventManager()), k: k, bank: bank}
}

func i(v int64) sdkmath.Int { return sdkmath.NewInt(v) }

func hasEvent(ctx sdk.Context, typ string) bool {
	for _, e := range ctx.EventManager().Events() {
		if e.Type == typ {
			return true
		}
	}
	return false
}

func TestMintBurn(t *testing.T) {
	f := setup(t)
	require.NoError(t, f.k.Mint(f.ctx, alice, i(1000)))
	require.Equal(t, i(1000), f.bank.Balance(alice, types.Denom))
	require.Equal(t, i(1000), f.k.Supply(f.ctx))

	require.NoError(t, f.k.BurnFrom(f.ctx, alice, i(400)))
	require.Equal(t, i(600), f.bank.Balance(alice, types.Denom))
	require.Equal(t, i(600), f.k.Supply(f.ctx))
	require.Error(t, f.k.BurnFrom(f.ctx, alice, i(601)))

	require.NoError(t, f.bank.SendCoinsFromAccountToModule(f.ctx, alice, pool, sdk.NewCoins(sdk.NewInt64Coin(types.Denom, 100))))
	require.NoError(t, f.k.BurnFromModule(f.ctx, pool, i(100)))
	require.Equal(t, i(500), f.k.Supply(f.ctx))

	require.ErrorIs(t, f.k.Mint(f.ctx, alice, i(-1)), types.ErrInvalidAmount)
	require.NoError(t, f.k.Mint(f.ctx, alice, i(0)), "zero is a no-op")
}

func TestSurplusAndShortfall(t *testing.T) {
	f := setup(t)
	require.NoError(t, f.k.AccrueSurplus(f.ctx, i(300)))
	require.Equal(t, i(300), f.k.SurplusBalance(f.ctx))
	require.Equal(t, i(300), f.k.Supply(f.ctx))

	covered, err := f.k.CoverShortfall(f.ctx, pool, i(200))
	require.NoError(t, err)
	require.Equal(t, i(200), covered)
	covered, err = f.k.CoverShortfall(f.ctx, pool, i(500))
	require.NoError(t, err)
	require.Equal(t, i(100), covered, "capped at the surplus balance")
	require.Equal(t, i(300), f.bank.ModuleBalance(pool, types.Denom))

	require.NoError(t, f.k.AddToSurplus(f.ctx, pool, i(50)))
	require.Equal(t, i(50), f.k.SurplusBalance(f.ctx))
}

func TestBadDebtAndHeal(t *testing.T) {
	f := setup(t)
	require.NoError(t, f.k.RecordBadDebt(f.ctx, i(1000), "auction"))
	require.True(t, hasEvent(f.ctx, types.EventTypeBadDebt))
	sin, _ := f.k.GetSin(f.ctx)
	require.Equal(t, i(1000), sin)

	// Partial heal: surplus 400 < sin 1000.
	require.NoError(t, f.k.AccrueSurplus(f.ctx, i(400)))
	require.NoError(t, f.k.Heal(f.ctx))
	sin, _ = f.k.GetSin(f.ctx)
	require.Equal(t, i(600), sin)
	require.True(t, f.k.SurplusBalance(f.ctx).IsZero())
	require.True(t, f.k.Supply(f.ctx).IsZero(), "healed KUSD is burned")
	require.True(t, hasEvent(f.ctx, types.EventTypeHealed))

	// Full heal: surplus 1000 > sin 600.
	require.NoError(t, f.k.AccrueSurplus(f.ctx, i(1000)))
	require.NoError(t, f.k.Heal(f.ctx))
	sin, _ = f.k.GetSin(f.ctx)
	require.True(t, sin.IsZero())
	require.Equal(t, i(400), f.k.SurplusBalance(f.ctx))

	// Nothing to heal: no-op.
	require.NoError(t, f.k.Heal(f.ctx))
	require.Equal(t, i(400), f.k.SurplusBalance(f.ctx))
}

func TestBurnAgainstSin(t *testing.T) {
	f := setup(t)
	require.NoError(t, f.k.Mint(f.ctx, alice, i(500)))
	require.NoError(t, f.bank.SendCoinsFromAccountToModule(f.ctx, alice, pool, sdk.NewCoins(sdk.NewInt64Coin(types.Denom, 500))))
	require.NoError(t, f.k.RecordBadDebt(f.ctx, i(300), "auction"))
	require.ErrorIs(t, f.k.BurnAgainstSin(f.ctx, pool, i(301)), types.ErrInvalidAmount)
	require.NoError(t, f.k.BurnAgainstSin(f.ctx, pool, i(300)))
	sin, _ := f.k.GetSin(f.ctx)
	require.True(t, sin.IsZero())
	require.Equal(t, i(200), f.k.Supply(f.ctx))
}

func TestMintPause(t *testing.T) {
	f := setup(t)
	p := types.DefaultParams()
	p.SinPauseBlocks = 3
	require.NoError(t, f.k.Params.Set(f.ctx, p))

	require.NoError(t, f.k.Mint(f.ctx, alice, i(10_000)))
	require.NoError(t, f.k.RecordBadDebt(f.ctx, i(500), "auction")) // exactly 5%: not above
	require.NoError(t, f.k.EndBlocker(f.ctx))
	paused, _ := f.k.AreMintsPaused(f.ctx)
	require.False(t, paused)

	require.NoError(t, f.k.RecordBadDebt(f.ctx, i(1), "auction")) // above 5%
	for h := int64(2); h <= 4; h++ {
		f.ctx = f.ctx.WithBlockHeight(h).WithEventManager(sdk.NewEventManager())
		require.NoError(t, f.k.EndBlocker(f.ctx))
		paused, _ = f.k.AreMintsPaused(f.ctx)
		require.False(t, paused, "height %d", h)
	}
	f.ctx = f.ctx.WithBlockHeight(5).WithEventManager(sdk.NewEventManager())
	require.NoError(t, f.k.EndBlocker(f.ctx))
	paused, _ = f.k.AreMintsPaused(f.ctx)
	require.True(t, paused, "over the threshold for SinPauseBlocks blocks")
	require.True(t, hasEvent(f.ctx, types.EventTypeMintsPaused))

	// Healing below the threshold unpauses.
	require.NoError(t, f.k.AccrueSurplus(f.ctx, i(501)))
	f.ctx = f.ctx.WithBlockHeight(6)
	require.NoError(t, f.k.EndBlocker(f.ctx))
	paused, _ = f.k.AreMintsPaused(f.ctx)
	require.False(t, paused)
}

func TestQueries(t *testing.T) {
	f := setup(t)
	require.NoError(t, f.k.AccrueSurplus(f.ctx, i(7)))
	require.NoError(t, f.k.RecordBadDebt(f.ctx, i(9), "auction"))
	q := keeper.NewQuerier(f.k)
	s, err := q.Surplus(f.ctx, &types.QuerySurplusRequest{})
	require.NoError(t, err)
	require.Equal(t, i(7), s.Surplus)
	b, err := q.BadDebt(f.ctx, &types.QueryBadDebtRequest{})
	require.NoError(t, err)
	require.Equal(t, i(9), b.Sin)
	require.False(t, b.MintsPaused)
}

func TestGenesisRoundTrip(t *testing.T) {
	f := setup(t)
	gs, err := f.k.ExportGenesis(f.ctx)
	require.NoError(t, err)
	require.Equal(t, types.DefaultGenesis(), gs)
	require.ErrorIs(t, f.k.InitGenesis(f.ctx, &types.GenesisState{}), types.ErrInvalidParams)
}

func TestUpdateParams(t *testing.T) {
	f := setup(t)
	ms := keeper.NewMsgServerImpl(f.k)
	next := types.DefaultParams()
	next.SinPauseBlocks++
	_, err := ms.UpdateParams(f.ctx, &types.MsgUpdateParams{Admin: stranger, Params: next})
	require.ErrorIs(t, err, errNotAdmin)
	_, err = ms.UpdateParams(f.ctx, &types.MsgUpdateParams{Admin: admin, Params: types.Params{}})
	require.ErrorIs(t, err, types.ErrInvalidParams)
	_, err = ms.UpdateParams(f.ctx, &types.MsgUpdateParams{Admin: admin, Params: next})
	require.NoError(t, err)
	got, err := f.k.GetParams(f.ctx)
	require.NoError(t, err)
	require.Equal(t, next, got)
}
