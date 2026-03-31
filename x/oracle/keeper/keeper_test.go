package keeper_test

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/cosmos/cosmos-sdk/runtime"
	storetypes "github.com/cosmos/cosmos-sdk/store/v2/types"
	"github.com/cosmos/cosmos-sdk/testutil"
	sdk "github.com/cosmos/cosmos-sdk/types"
	moduletestutil "github.com/cosmos/cosmos-sdk/types/module/testutil"

	"github.com/OWNER/keystone/x/oracle/keeper"
	"github.com/OWNER/keystone/x/oracle/types"
)

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

func setup(t *testing.T) (sdk.Context, *keeper.Keeper) {
	t.Helper()
	key := storetypes.NewKVStoreKey(types.StoreKey)
	tctx := testutil.DefaultContextWithDB(t, key, storetypes.NewTransientStoreKey("transient_test"))
	k := keeper.NewKeeper(moduletestutil.MakeTestEncodingConfig().Codec, runtime.NewKVStoreService(key), fakeAdmin{})
	require.NoError(t, k.InitGenesis(tctx.Ctx, types.DefaultGenesis()))
	return tctx.Ctx, k
}

func TestGenesisRoundTrip(t *testing.T) {
	ctx, k := setup(t)
	gs, err := k.ExportGenesis(ctx)
	require.NoError(t, err)
	require.Equal(t, types.DefaultGenesis(), gs)
}

func TestInitGenesisRejectsInvalidParams(t *testing.T) {
	ctx, k := setup(t)
	require.ErrorIs(t, k.InitGenesis(ctx, &types.GenesisState{}), types.ErrInvalidParams)
}

func TestUpdateParams(t *testing.T) {
	ctx, k := setup(t)
	ms := keeper.NewMsgServerImpl(k)
	next := types.DefaultParams()
	next.StaleBlocks++

	_, err := ms.UpdateParams(ctx, &types.MsgUpdateParams{Admin: stranger, Params: next})
	require.ErrorIs(t, err, errNotAdmin)

	_, err = ms.UpdateParams(ctx, &types.MsgUpdateParams{Admin: admin, Params: types.Params{}})
	require.ErrorIs(t, err, types.ErrInvalidParams)

	_, err = ms.UpdateParams(ctx, &types.MsgUpdateParams{Admin: "nope", Params: next})
	require.ErrorIs(t, err, types.ErrInvalidAddress)

	_, err = ms.UpdateParams(ctx, &types.MsgUpdateParams{Admin: admin, Params: next})
	require.NoError(t, err)
	got, err := k.GetParams(ctx)
	require.NoError(t, err)
	require.Equal(t, next, got)

	resp, err := keeper.NewQuerier(k).Params(ctx, &types.QueryParamsRequest{})
	require.NoError(t, err)
	require.Equal(t, next, resp.Params)
}
