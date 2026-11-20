package keeper_test

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/cosmos/cosmos-sdk/baseapp"
	cdctypes "github.com/cosmos/cosmos-sdk/codec/types"
	"github.com/cosmos/cosmos-sdk/crypto/keys/ed25519"
	cryptotypes "github.com/cosmos/cosmos-sdk/crypto/types"
	"github.com/cosmos/cosmos-sdk/runtime"
	storetypes "github.com/cosmos/cosmos-sdk/store/v2/types"
	"github.com/cosmos/cosmos-sdk/testutil"
	sdk "github.com/cosmos/cosmos-sdk/types"
	moduletestutil "github.com/cosmos/cosmos-sdk/types/module/testutil"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"

	"github.com/logicvoid0x/keystone/x/admin/keeper"
	"github.com/logicvoid0x/keystone/x/admin/types"
)

func addr(i byte) sdk.AccAddress { return sdk.AccAddress(bytes.Repeat([]byte{i}, 20)) }

var admin = addr(0xAD)

// fakeStaking holds validators by operator bytes; Jail records the consensus address.
type fakeStaking struct {
	vals   map[string]stakingtypes.Validator
	jailed []sdk.ConsAddress
}

func (f *fakeStaking) GetValidator(_ context.Context, a sdk.ValAddress) (stakingtypes.Validator, error) {
	v, ok := f.vals[string(a)]
	if !ok {
		return v, stakingtypes.ErrNoValidatorFound
	}
	return v, nil
}

func (f *fakeStaking) Jail(_ context.Context, c sdk.ConsAddress) error {
	f.jailed = append(f.jailed, c)
	return nil
}

type fixture struct {
	ctx sdk.Context
	k   *keeper.Keeper
	sk  *fakeStaking
}

// newFixture starts a keeper with operators 1..n allowlisted.
func newFixture(t *testing.T, n byte) fixture {
	t.Helper()
	key := storetypes.NewKVStoreKey(types.StoreKey)
	tctx := testutil.DefaultContextWithDB(t, key, storetypes.NewTransientStoreKey("transient_test"))
	enc := moduletestutil.MakeTestEncodingConfig()
	sk := &fakeStaking{vals: map[string]stakingtypes.Validator{}}
	k := keeper.NewKeeper(enc.Codec, runtime.NewKVStoreService(key), sk)
	gs := &types.GenesisState{Params: types.Params{Admin: admin.String()}}
	for i := byte(1); i <= n; i++ {
		gs.Allowlist = append(gs.Allowlist, types.AllowEntry{Operator: addr(i).String(), Moniker: "op"})
	}
	require.NoError(t, k.InitGenesis(tctx.Ctx, gs))
	return fixture{ctx: tctx.Ctx.WithEventManager(sdk.NewEventManager()).WithBlockHeight(7), k: k, sk: sk}
}

// addValidator gives operator i a staking validator with a consensus key derived from i.
func (f fixture) addValidator(t *testing.T, i byte, jailed bool) sdk.ConsAddress {
	t.Helper()
	pk := ed25519PubKey(i)
	any, err := cdctypes.NewAnyWithValue(pk)
	require.NoError(t, err)
	v := stakingtypes.Validator{OperatorAddress: sdk.ValAddress(addr(i)).String(), ConsensusPubkey: any, Jailed: jailed}
	f.sk.vals[string(addr(i))] = v
	return sdk.ConsAddress(pk.Address())
}

type refuseGuard struct{ seen []sdk.AccAddress }

func (g *refuseGuard) AllowRemoval(_ context.Context, removed []sdk.AccAddress) error {
	g.seen = removed
	return errors.New("custody says no")
}

func TestGenesisRoundTrip(t *testing.T) {
	f := newFixture(t, 3)
	gs, err := f.k.ExportGenesis(f.ctx)
	require.NoError(t, err)
	require.Equal(t, admin.String(), gs.Params.Admin)
	require.Len(t, gs.Allowlist, 3)
	for i, e := range gs.Allowlist {
		require.Equal(t, addr(byte(i+1)).String(), e.Operator)
	}
	entries, err := f.k.AllEntries(f.ctx)
	require.NoError(t, err)
	require.Equal(t, gs.Allowlist, entries)
}

func TestIsAllowlistedAndGetEntry(t *testing.T) {
	f := newFixture(t, 2)
	ok, err := f.k.IsAllowlisted(f.ctx, addr(1))
	require.NoError(t, err)
	require.True(t, ok)
	ok, err = f.k.IsAllowlisted(f.ctx, addr(9))
	require.NoError(t, err)
	require.False(t, ok)
	e, err := f.k.GetEntry(f.ctx, addr(2))
	require.NoError(t, err)
	require.Equal(t, addr(2).String(), e.Operator)
	_, err = f.k.GetEntry(f.ctx, addr(9))
	require.ErrorIs(t, err, types.ErrNotAllowlisted)
}

func TestSetAllowlistAdd(t *testing.T) {
	f := newFixture(t, 2)
	require.NoError(t, f.k.SetAllowlist(f.ctx, &types.MsgSetAllowlist{
		Admin: admin.String(), Add: []types.AllowInput{{Operator: addr(5).String(), Moniker: "five"}},
	}))
	e, err := f.k.GetEntry(f.ctx, addr(5))
	require.NoError(t, err)
	require.Equal(t, "five", e.Moniker)
	require.EqualValues(t, 7, e.AddedHeight)

	err = f.k.SetAllowlist(f.ctx, &types.MsgSetAllowlist{Admin: admin.String(), Add: []types.AllowInput{{Operator: addr(5).String()}}})
	require.ErrorIs(t, err, types.ErrAlreadyListed)
}

func TestSetAllowlistRemoveJailsBondedValidator(t *testing.T) {
	f := newFixture(t, 3)
	cons := f.addValidator(t, 2, false)
	f.addValidator(t, 3, true) // already jailed: not jailed again
	require.NoError(t, f.k.SetAllowlist(f.ctx, &types.MsgSetAllowlist{
		Admin: admin.String(), Remove: []string{addr(2).String(), addr(3).String()},
	}))
	require.Equal(t, []sdk.ConsAddress{cons}, f.sk.jailed)
	ok, err := f.k.IsAllowlisted(f.ctx, addr(2))
	require.NoError(t, err)
	require.False(t, ok)
}

func TestSetAllowlistRemoveWithoutValidator(t *testing.T) {
	f := newFixture(t, 2)
	require.NoError(t, f.k.SetAllowlist(f.ctx, &types.MsgSetAllowlist{Admin: admin.String(), Remove: []string{addr(2).String()}}))
	require.Empty(t, f.sk.jailed)
}

func TestSetAllowlistRules(t *testing.T) {
	cases := []struct {
		name string
		msg  types.MsgSetAllowlist
		err  error
	}{
		{"not admin", types.MsgSetAllowlist{Admin: addr(1).String(), Remove: []string{addr(2).String()}}, types.ErrUnauthorized},
		{"remove unknown", types.MsgSetAllowlist{Admin: admin.String(), Remove: []string{addr(9).String()}}, types.ErrNotAllowlisted},
		{"remove all", types.MsgSetAllowlist{Admin: admin.String(), Remove: []string{addr(1).String(), addr(2).String()}}, types.ErrEmptyAllowlist},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t, 2)
			require.ErrorIs(t, f.k.SetAllowlist(f.ctx, &tc.msg), tc.err)
		})
	}
}

func TestSetAllowlistGuardRefuses(t *testing.T) {
	f := newFixture(t, 3)
	f.addValidator(t, 2, false)
	g := &refuseGuard{}
	f.k.SetRemovalGuard(g)
	err := f.k.SetAllowlist(f.ctx, &types.MsgSetAllowlist{Admin: admin.String(), Remove: []string{addr(2).String()}})
	require.ErrorIs(t, err, types.ErrGuardRefused)
	require.Equal(t, []sdk.AccAddress{addr(2)}, g.seen)
	require.Empty(t, f.sk.jailed)
	ok, err := f.k.IsAllowlisted(f.ctx, addr(2))
	require.NoError(t, err)
	require.True(t, ok)
}

func TestUpdateParamsAndCheckAdmin(t *testing.T) {
	f := newFixture(t, 1)
	require.ErrorIs(t, f.k.CheckAdmin(f.ctx, addr(1).String()), types.ErrUnauthorized)
	require.NoError(t, f.k.CheckAdmin(f.ctx, admin.String()))
	require.ErrorIs(t, f.k.UpdateParams(f.ctx, &types.MsgUpdateParams{Admin: addr(1).String(), Params: types.Params{Admin: addr(1).String()}}), types.ErrUnauthorized)
	require.NoError(t, f.k.UpdateParams(f.ctx, &types.MsgUpdateParams{Admin: admin.String(), Params: types.Params{Admin: addr(1).String()}}))
	a, err := f.k.Admin(f.ctx)
	require.NoError(t, err)
	require.Equal(t, addr(1), a)
	require.NoError(t, f.k.CheckAdmin(f.ctx, addr(1).String()))
}

// fakeRouter records the messages it handles.
type fakeRouter struct{ got []sdk.Msg }

func (r *fakeRouter) Handler(sdk.Msg) baseapp.MsgServiceHandler {
	return func(ctx sdk.Context, m sdk.Msg) (*sdk.Result, error) {
		r.got = append(r.got, m)
		return &sdk.Result{}, nil
	}
}

func (r *fakeRouter) HandlerByTypeURL(string) baseapp.MsgServiceHandler { return r.Handler(nil) }

func execMsg(t *testing.T, signer sdk.AccAddress, msgs ...sdk.Msg) *types.MsgExec {
	t.Helper()
	anys := make([]*cdctypes.Any, len(msgs))
	for i, m := range msgs {
		a, err := cdctypes.NewAnyWithValue(m)
		require.NoError(t, err)
		anys[i] = a
	}
	return &types.MsgExec{Admin: signer.String(), Msgs: anys}
}

func TestExec(t *testing.T) {
	stakingURL := sdk.MsgTypeURL(&stakingtypes.MsgUpdateParams{})
	authority := keeper.ModuleAddress().String()
	good := &stakingtypes.MsgUpdateParams{Authority: authority, Params: stakingtypes.DefaultParams()}

	t.Run("dispatches an accepted message", func(t *testing.T) {
		f := newFixture(t, 1)
		r := &fakeRouter{}
		f.k.SetExecRouter(r, []string{stakingURL})
		require.NoError(t, f.k.Exec(f.ctx, execMsg(t, admin, good)))
		require.Len(t, r.got, 1)
	})
	t.Run("not admin", func(t *testing.T) {
		f := newFixture(t, 1)
		f.k.SetExecRouter(&fakeRouter{}, []string{stakingURL})
		require.ErrorIs(t, f.k.Exec(f.ctx, execMsg(t, addr(1), good)), types.ErrUnauthorized)
	})
	t.Run("type not accepted", func(t *testing.T) {
		f := newFixture(t, 1)
		r := &fakeRouter{}
		f.k.SetExecRouter(r, []string{stakingURL})
		send := &banktypes.MsgSend{FromAddress: authority, ToAddress: admin.String()}
		require.ErrorIs(t, f.k.Exec(f.ctx, execMsg(t, admin, good, send)), types.ErrExecMsg)
		require.Empty(t, r.got, "nothing runs when any message is refused")
	})
	t.Run("wrong authority", func(t *testing.T) {
		f := newFixture(t, 1)
		f.k.SetExecRouter(&fakeRouter{}, []string{stakingURL})
		bad := &stakingtypes.MsgUpdateParams{Authority: admin.String(), Params: stakingtypes.DefaultParams()}
		require.ErrorIs(t, f.k.Exec(f.ctx, execMsg(t, admin, bad)), types.ErrExecMsg)
	})
	t.Run("no router", func(t *testing.T) {
		f := newFixture(t, 1)
		require.ErrorIs(t, f.k.Exec(f.ctx, execMsg(t, admin, good)), types.ErrExecMsg)
	})
}

func ed25519PubKey(i byte) cryptotypes.PubKey {
	return ed25519.GenPrivKeyFromSecret([]byte{'v', i}).PubKey()
}
