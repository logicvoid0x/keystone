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

	"github.com/OWNER/keystone/x/custody/keeper"
	"github.com/OWNER/keystone/x/custody/types"
)

var (
	admin       = sdk.AccAddress(bytes.Repeat([]byte{0xAD}, 20)).String()
	stranger    = sdk.AccAddress(bytes.Repeat([]byte{0x05}, 20)).String()
	alice       = sdk.AccAddress(bytes.Repeat([]byte{0xA1}, 20))
	bob         = sdk.AccAddress(bytes.Repeat([]byte{0xB0}, 20))
	errNotAdmin = errors.New("not admin")
)

type fakeAdmin struct{}

func (fakeAdmin) CheckAdmin(_ context.Context, signer string) error {
	if signer != admin {
		return errNotAdmin
	}
	return nil
}

func setup(t *testing.T) (sdk.Context, *keeper.Keeper) {
	t.Helper()
	key := storetypes.NewKVStoreKey(types.StoreKey)
	tctx := testutil.DefaultContextWithDB(t, key, storetypes.NewTransientStoreKey("transient_test"))
	k := keeper.NewKeeper(moduletestutil.MakeTestEncodingConfig().Codec, runtime.NewKVStoreService(key), fakeAdmin{})
	require.NoError(t, k.InitGenesis(tctx.Ctx, types.DefaultGenesis()))
	return tctx.Ctx, k
}

func i(v int64) sdkmath.Int { return sdkmath.NewInt(v) }

func TestClaimable(t *testing.T) {
	ctx, k := setup(t)
	require.NoError(t, k.CreditClaimable(ctx, alice, i(100)))
	require.NoError(t, k.CreditClaimable(ctx, alice, i(50)))
	require.NoError(t, k.CreditClaimable(ctx, bob, i(7)))
	v, err := k.GetClaimable(ctx, alice)
	require.NoError(t, err)
	require.Equal(t, i(150), v)

	require.ErrorIs(t, k.DebitClaimable(ctx, alice, i(151)), types.ErrInsufficient)
	require.NoError(t, k.DebitClaimable(ctx, alice, i(150)))
	v, _ = k.GetClaimable(ctx, alice)
	require.True(t, v.IsZero())
	has, _ := k.Claimable.Has(ctx, alice)
	require.False(t, has, "zero balances are removed")

	total, err := k.TotalClaimable(ctx)
	require.NoError(t, err)
	require.Equal(t, i(7), total)

	require.ErrorIs(t, k.CreditClaimable(ctx, alice, i(-1)), types.ErrInvalidAmount)

	resp, err := keeper.NewQuerier(k).Claimable(ctx, &types.QueryClaimableRequest{Address: bob.String()})
	require.NoError(t, err)
	require.Equal(t, i(7), resp.ClaimableSat)
	_, err = keeper.NewQuerier(k).Claimable(ctx, &types.QueryClaimableRequest{Address: "x"})
	require.Error(t, err)
}

func TestCreditedAndCap(t *testing.T) {
	ctx, k := setup(t)
	p := types.DefaultParams() // cap 1e9 sat
	h, err := k.CapHeadroom(ctx)
	require.NoError(t, err)
	require.Equal(t, p.TreasuryCapSat, h)

	require.NoError(t, k.AddCredited(ctx, i(900_000_000)))
	h, _ = k.CapHeadroom(ctx)
	require.Equal(t, i(100_000_000), h)
	require.ErrorIs(t, k.AddCredited(ctx, i(100_000_001)), types.ErrOverCap)
	require.NoError(t, k.AddCredited(ctx, i(100_000_000)))
	h, _ = k.CapHeadroom(ctx)
	require.True(t, h.IsZero())

	require.NoError(t, k.SubCredited(ctx, i(1)))
	require.ErrorIs(t, k.SubCredited(ctx, i(1_000_000_000)), types.ErrUnderflow)

	// Lowering the cap below credited: headroom is zero, not negative.
	p.TreasuryCapSat = i(10)
	require.NoError(t, k.Params.Set(ctx, p))
	h, _ = k.CapHeadroom(ctx)
	require.True(t, h.IsZero())

	require.NoError(t, k.CreditClaimable(ctx, alice, i(5)))
	c, err := k.CustodiedSat(ctx)
	require.NoError(t, err)
	require.Equal(t, i(999_999_999+5), c)
	cr, _ := k.GetCredited(ctx)
	require.Equal(t, i(999_999_999), cr)
	require.NoError(t, k.ApplyOps(ctx))
}

func TestGenesisRoundTrip(t *testing.T) {
	ctx, k := setup(t)
	gs, err := k.ExportGenesis(ctx)
	require.NoError(t, err)
	require.Equal(t, types.DefaultGenesis(), gs)
	require.ErrorIs(t, k.InitGenesis(ctx, &types.GenesisState{}), types.ErrInvalidParams)
}

func TestUpdateParams(t *testing.T) {
	ctx, k := setup(t)
	ms := keeper.NewMsgServerImpl(k)
	next := types.DefaultParams()
	next.NoncePool++
	_, err := ms.UpdateParams(ctx, &types.MsgUpdateParams{Admin: stranger, Params: next})
	require.ErrorIs(t, err, errNotAdmin)
	_, err = ms.UpdateParams(ctx, &types.MsgUpdateParams{Admin: admin, Params: types.Params{}})
	require.ErrorIs(t, err, types.ErrInvalidParams)
	_, err = ms.UpdateParams(ctx, &types.MsgUpdateParams{Admin: admin, Params: next})
	require.NoError(t, err)
	got, err := k.GetParams(ctx)
	require.NoError(t, err)
	require.Equal(t, next, got)
}
