package keeper_test

import (
	"bytes"
	"context"
	"errors"
	"math/rand"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	sdkmath "cosmossdk.io/math"

	"github.com/cosmos/cosmos-sdk/runtime"
	storetypes "github.com/cosmos/cosmos-sdk/store/v2/types"
	"github.com/cosmos/cosmos-sdk/testutil"
	sdk "github.com/cosmos/cosmos-sdk/types"
	moduletestutil "github.com/cosmos/cosmos-sdk/types/module/testutil"

	"github.com/OWNER/keystone/testutil/devdealer"
	"github.com/OWNER/keystone/x/custody/keeper"
	"github.com/OWNER/keystone/x/custody/types"
	poatypes "github.com/OWNER/keystone/x/poa/types"
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
	ctx, k := setupWith(t, types.DefaultGenesis())
	return ctx, k
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

	cr, _ := k.GetCredited(ctx)
	require.Equal(t, i(999_999_999), cr)
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

const localChain = "keystone-local-1"

// fakeVals is the x/poa validator set: power 1 per operator.
type fakeVals struct{ ops []poatypes.Operator }

func (f *fakeVals) ActiveValidators(context.Context) ([]poatypes.Operator, error) { return f.ops, nil }

func (f *fakeVals) GetOperator(_ context.Context, addr sdk.AccAddress) (poatypes.Operator, error) {
	for _, op := range f.ops {
		if op.OperatorAddress == addr.String() {
			return op, nil
		}
	}
	return poatypes.Operator{}, errors.New("unknown operator")
}

func setupWith(t *testing.T, gs *types.GenesisState) (sdk.Context, *keeper.Keeper) {
	t.Helper()
	return setupWithVals(t, gs, &fakeVals{})
}

func setupWithVals(t *testing.T, gs *types.GenesisState, vals *fakeVals) (sdk.Context, *keeper.Keeper) {
	t.Helper()
	key := storetypes.NewKVStoreKey(types.StoreKey)
	tctx := testutil.DefaultContextWithDB(t, key, storetypes.NewTransientStoreKey("transient_test"))
	ctx := tctx.Ctx.WithChainID(localChain).WithEventManager(sdk.NewEventManager())
	k := keeper.NewKeeper(moduletestutil.MakeTestEncodingConfig().Codec, runtime.NewKVStoreService(key), fakeAdmin{}, vals)
	require.NoError(t, k.InitGenesis(ctx, gs))
	return ctx, k
}

// dealtGenesis is a regtest custody genesis with a dealer epoch for 4 operators.
func dealtGenesis(t *testing.T) *types.GenesisState {
	t.Helper()
	ops := []sdk.AccAddress{alice, bob, sdk.AccAddress(bytes.Repeat([]byte{0xC0}, 20)), sdk.AccAddress(bytes.Repeat([]byte{0xD0}, 20))}
	d, err := devdealer.Deal(rand.New(rand.NewSource(1)), ops)
	require.NoError(t, err)
	gs := types.DefaultGenesis()
	gs.Params.BitcoinNetwork = types.NetworkRegtest
	d.Apply(gs)
	return gs
}

func TestGenesisEpochs(t *testing.T) {
	gs := dealtGenesis(t)
	ctx, k := setupWith(t, gs)
	e, ok, err := k.GetActiveEpoch(ctx)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, uint32(3), e.T)
	out, err := k.ExportGenesis(ctx)
	require.NoError(t, err)
	require.Equal(t, gs, out)

	// Genesis epochs only on localnet chain ids.
	key := storetypes.NewKVStoreKey(types.StoreKey)
	tctx := testutil.DefaultContextWithDB(t, key, storetypes.NewTransientStoreKey("t2"))
	k2 := keeper.NewKeeper(moduletestutil.MakeTestEncodingConfig().Codec, runtime.NewKVStoreService(key), fakeAdmin{}, &fakeVals{})
	require.ErrorContains(t, k2.InitGenesis(tctx.Ctx.WithChainID("keystone-signet-1"), gs), "localnet")
	require.NoError(t, k2.InitGenesis(tctx.Ctx.WithChainID("keystone-signet-1"), types.DefaultGenesis()))
}

func TestGenesisValidateEpochs(t *testing.T) {
	cases := map[string]func(*types.GenesisState){
		"odd y key": func(gs *types.GenesisState) {
			gs.Epochs[0].GroupKey = append([]byte{0x03}, gs.Epochs[0].GroupKey[1:]...)
		},
		"active mismatch": func(gs *types.GenesisState) { gs.ActiveEpoch = 2 },
		"t > n":           func(gs *types.GenesisState) { gs.Epochs[0].T = 5 },
		"missing share":   func(gs *types.GenesisState) { gs.ShareIndices = gs.ShareIndices[1:] },
		"unsorted shares": func(gs *types.GenesisState) {
			gs.ShareIndices[0], gs.ShareIndices[1] = gs.ShareIndices[1], gs.ShareIndices[0]
		},
		"bad verifying key":  func(gs *types.GenesisState) { gs.VerifyingShares[0].Pubkey = []byte{1, 2} },
		"index out of range": func(gs *types.GenesisState) { gs.VerifyingShares[3].Index = 9 },
		"epoch zero":         func(gs *types.GenesisState) { gs.Epochs[0].Number = 0 },
	}
	for name, mut := range cases {
		t.Run(name, func(t *testing.T) {
			gs := dealtGenesis(t)
			mut(gs)
			require.ErrorIs(t, gs.Validate(), types.ErrInvalidGenesis)
		})
	}
	require.NoError(t, dealtGenesis(t).Validate())
}

func TestRegisterVaultAndDepositAddress(t *testing.T) {
	ctx, k := setupWith(t, dealtGenesis(t))
	q := keeper.NewQuerier(k)

	addr1, err := k.RegisterVault(ctx, 1)
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(addr1, "bcrt1p"), addr1)
	addr2, err := k.RegisterVault(ctx, 2)
	require.NoError(t, err)
	require.NotEqual(t, addr1, addr2)
	_, err = k.RegisterVault(ctx, 1)
	require.ErrorIs(t, err, types.ErrInvalidAddress, "a vault registers once per epoch")

	res, err := q.DepositAddress(ctx, &types.QueryDepositAddressRequest{VaultId: 1})
	require.NoError(t, err)
	require.Equal(t, addr1, res.Address)
	require.Equal(t, uint64(1), res.Epoch)
	id, err := k.ScriptVault.Get(ctx, res.PkScript)
	require.NoError(t, err)
	require.Equal(t, uint64(1), id)

	// The script is the §9.6 derivation for (chain id, epoch, vault id).
	e, _, _ := k.GetActiveEpoch(ctx)
	p, _, err := types.ParseGroupKey(e.GroupKey)
	require.NoError(t, err)
	want, _, err := types.VaultScript(p, localChain, 1, 1)
	require.NoError(t, err)
	require.Equal(t, want, res.PkScript)

	_, err = q.DepositAddress(ctx, &types.QueryDepositAddressRequest{VaultId: 9})
	require.Error(t, err)
	ae, err := q.ActiveEpoch(ctx, &types.QueryActiveEpochRequest{})
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(ae.TreasuryAddress, "bcrt1p"))
	require.NotEqual(t, addr1, ae.TreasuryAddress)
	tr, err := q.Treasury(ctx, &types.QueryTreasuryRequest{})
	require.NoError(t, err)
	require.True(t, tr.CustodiedSat.IsZero())
}

// Without an ACTIVE epoch, vaults open with no address and nothing is registered.
func TestRegisterVaultNoEpoch(t *testing.T) {
	ctx, k := setup(t)
	addr, err := k.RegisterVault(ctx, 1)
	require.NoError(t, err)
	require.Empty(t, addr)
	_, err = keeper.NewQuerier(k).DepositAddress(ctx, &types.QueryDepositAddressRequest{VaultId: 1})
	require.Error(t, err)
	_, err = keeper.NewQuerier(k).ActiveEpoch(ctx, &types.QueryActiveEpochRequest{})
	require.Error(t, err)
}
