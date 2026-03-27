package keeper_test

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/cometbft/cometbft/crypto/ed25519"
	"github.com/stretchr/testify/require"

	"github.com/cosmos/cosmos-sdk/runtime"
	storetypes "github.com/cosmos/cosmos-sdk/store/v2/types"
	"github.com/cosmos/cosmos-sdk/testutil"
	sdk "github.com/cosmos/cosmos-sdk/types"
	moduletestutil "github.com/cosmos/cosmos-sdk/types/module/testutil"

	"github.com/OWNER/keystone/x/poa/keeper"
	"github.com/OWNER/keystone/x/poa/types"
)

func addr(i byte) sdk.AccAddress { return sdk.AccAddress(bytes.Repeat([]byte{i}, 20)) }

func pubkey(i byte) []byte {
	return ed25519.GenPrivKeyFromSecret([]byte{'k', i}).PubKey().Bytes()
}

var admin = addr(0xAD)

type fixture struct {
	ctx sdk.Context
	k   *keeper.Keeper
}

// newFixture starts a keeper with operators 1..n at power 1.
func newFixture(t *testing.T, n byte) fixture {
	t.Helper()
	key := storetypes.NewKVStoreKey(types.StoreKey)
	tctx := testutil.DefaultContextWithDB(t, key, storetypes.NewTransientStoreKey("transient_test"))
	enc := moduletestutil.MakeTestEncodingConfig()
	k := keeper.NewKeeper(enc.Codec, runtime.NewKVStoreService(key))
	gs := &types.GenesisState{Params: types.Params{Admin: admin.String()}}
	for i := byte(1); i <= n; i++ {
		gs.Operators = append(gs.Operators, op(i, 1))
	}
	ups, err := k.InitGenesis(tctx.Ctx, gs)
	require.NoError(t, err)
	require.Len(t, ups, int(n))
	return fixture{ctx: tctx.Ctx.WithEventManager(sdk.NewEventManager()), k: k}
}

func op(i byte, power int64) types.Operator {
	return types.Operator{OperatorAddress: addr(i).String(), ConsensusPubkey: pubkey(i), Moniker: "op", Power: power}
}

func entry(i byte, pk []byte, power int64) types.ValidatorEntry {
	return types.ValidatorEntry{OperatorAddress: addr(i).String(), ConsensusPubkey: pk, Moniker: "op", Power: power}
}

func setMsg(entries ...types.ValidatorEntry) *types.MsgSetValidators {
	return &types.MsgSetValidators{Admin: admin.String(), Entries: entries}
}

type refuseGuard struct{}

func (refuseGuard) AllowSetValidators(context.Context, []types.Operator, []types.Operator) error {
	return errors.New("custody says no")
}

// recordGuard captures what it was shown.
type recordGuard struct{ before, after []types.Operator }

func (g *recordGuard) AllowSetValidators(_ context.Context, before, after []types.Operator) error {
	g.before, g.after = before, after
	return nil
}

func TestInitGenesisReturnsSortedValidatorSet(t *testing.T) {
	gs := &types.GenesisState{Params: types.Params{Admin: admin.String()}, Operators: []types.Operator{op(1, 1), op(2, 1), op(3, 0), op(4, 1)}}
	key := storetypes.NewKVStoreKey(types.StoreKey)
	tctx := testutil.DefaultContextWithDB(t, key, storetypes.NewTransientStoreKey("t2"))
	k := keeper.NewKeeper(moduletestutil.MakeTestEncodingConfig().Codec, runtime.NewKVStoreService(key))
	ups, err := k.InitGenesis(tctx.Ctx, gs)
	require.NoError(t, err)
	require.Len(t, ups, 3, "power-0 operators produce no update")
	for i := 1; i < len(ups); i++ {
		a := types.ConsAddress(ups[i-1].PubKey.GetEd25519())
		b := types.ConsAddress(ups[i].PubKey.GetEd25519())
		require.Negative(t, bytes.Compare(a, b), "updates sorted by consensus address")
	}
	for _, u := range ups {
		require.Equal(t, int64(1), u.Power)
	}
}

func TestSetValidatorsRules(t *testing.T) {
	cases := []struct {
		name  string
		msg   func() *types.MsgSetValidators
		guard types.RemovalGuard
		err   error
	}{
		{"non-admin signer", func() *types.MsgSetValidators {
			m := setMsg(entry(5, pubkey(5), 1))
			m.Admin = addr(9).String()
			return m
		}, nil, types.ErrUnauthorized},
		{"power 2", func() *types.MsgSetValidators { return setMsg(entry(5, pubkey(5), 2)) }, nil, types.ErrInvalidPower},
		{"negative power", func() *types.MsgSetValidators { return setMsg(entry(5, pubkey(5), -1)) }, nil, types.ErrInvalidPower},
		{"short pubkey", func() *types.MsgSetValidators { return setMsg(entry(5, pubkey(5)[:31], 1)) }, nil, types.ErrInvalidPubKey},
		{"new operator reuses a pubkey", func() *types.MsgSetValidators { return setMsg(entry(5, pubkey(1), 1)) }, nil, types.ErrPubKeyUsed},
		{"existing operator changes pubkey", func() *types.MsgSetValidators { return setMsg(entry(1, pubkey(7), 1)) }, nil, types.ErrPubKeyChange},
		{"unsorted entries", func() *types.MsgSetValidators {
			return setMsg(entry(6, pubkey(6), 1), entry(5, pubkey(5), 1))
		}, nil, types.ErrUnsorted},
		{"duplicate entries", func() *types.MsgSetValidators {
			return setMsg(entry(5, pubkey(5), 1), entry(5, pubkey(5), 1))
		}, nil, types.ErrUnsorted},
		{"two entries share a pubkey", func() *types.MsgSetValidators {
			return setMsg(entry(5, pubkey(5), 1), entry(6, pubkey(5), 1))
		}, nil, types.ErrPubKeyUsed},
		{"no entries", func() *types.MsgSetValidators { return setMsg() }, nil, types.ErrEmptyEntries},
		{"removes every operator", func() *types.MsgSetValidators {
			return setMsg(entry(1, pubkey(1), 0), entry(2, pubkey(2), 0))
		}, nil, types.ErrEmptyValidatorSet},
		{"guard refuses", func() *types.MsgSetValidators { return setMsg(entry(2, pubkey(2), 0)) }, refuseGuard{}, types.ErrGuardRefused},
		{"valid add", func() *types.MsgSetValidators { return setMsg(entry(5, pubkey(5), 1)) }, nil, nil},
		{"valid remove", func() *types.MsgSetValidators { return setMsg(entry(2, pubkey(2), 0)) }, nil, nil},
		{"valid moniker change", func() *types.MsgSetValidators {
			e := entry(1, pubkey(1), 1)
			e.Moniker = "renamed"
			return setMsg(e)
		}, nil, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t, 2)
			if tc.guard != nil {
				f.k.SetRemovalGuard(tc.guard)
			}
			before, err := f.k.AllOperators(f.ctx)
			require.NoError(t, err)
			err = f.k.SetValidators(f.ctx, tc.msg())
			if tc.err != nil {
				require.ErrorIs(t, err, tc.err)
				after, err := f.k.AllOperators(f.ctx)
				require.NoError(t, err)
				require.Equal(t, before, after, "state unchanged on error")
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestPubKeyOfRemovedOperatorCannotBeReused(t *testing.T) {
	f := newFixture(t, 2)
	require.NoError(t, f.k.SetValidators(f.ctx, setMsg(entry(2, pubkey(2), 0))))
	err := f.k.SetValidators(f.ctx, setMsg(entry(5, pubkey(2), 1)))
	require.ErrorIs(t, err, types.ErrPubKeyUsed)
	// The same operator can come back with its own key.
	require.NoError(t, f.k.SetValidators(f.ctx, setMsg(entry(2, pubkey(2), 1))))
}

func TestGuardSeesBeforeAndAfter(t *testing.T) {
	f := newFixture(t, 2)
	g := &recordGuard{}
	f.k.SetRemovalGuard(g)
	require.NoError(t, f.k.SetValidators(f.ctx, setMsg(entry(2, pubkey(2), 0), entry(5, pubkey(5), 1))))
	require.Equal(t, []types.Operator{op(1, 1), op(2, 1)}, g.before)
	require.Equal(t, []types.Operator{op(1, 1), op(2, 0), op(5, 1)}, g.after)
}

func TestRemovalKeepsRecordAndPubKey(t *testing.T) {
	f := newFixture(t, 3)
	require.NoError(t, f.k.SetValidators(f.ctx, setMsg(entry(2, pubkey(2), 0))))

	got, err := f.k.GetOperator(f.ctx, addr(2))
	require.NoError(t, err)
	require.Equal(t, int64(0), got.Power)

	// Same block, before EndBlocker: the removed operator's key still resolves.
	pk, err := f.k.GetPubKeyByConsAddr(f.ctx, types.ConsAddress(pubkey(2)))
	require.NoError(t, err)
	require.Equal(t, pubkey(2), pk.GetEd25519())

	ups, err := f.k.EndBlocker(f.ctx)
	require.NoError(t, err)
	require.Len(t, ups, 1)
	require.Equal(t, int64(0), ups[0].Power)
	require.Equal(t, pubkey(2), ups[0].PubKey.GetEd25519())

	// And after EndBlocker too.
	_, err = f.k.GetPubKeyByConsAddr(f.ctx, types.ConsAddress(pubkey(2)))
	require.NoError(t, err)
}

func TestGetPubKeyByConsAddrUnknown(t *testing.T) {
	f := newFixture(t, 1)
	_, err := f.k.GetPubKeyByConsAddr(f.ctx, types.ConsAddress(pubkey(9)))
	require.ErrorIs(t, err, types.ErrUnknownOperator)
}

func TestEndBlockerDiff(t *testing.T) {
	f := newFixture(t, 3)

	ups, err := f.k.EndBlocker(f.ctx)
	require.NoError(t, err)
	require.Empty(t, ups, "no change since genesis")

	require.NoError(t, f.k.SetValidators(f.ctx, setMsg(entry(1, pubkey(1), 0), entry(4, pubkey(4), 1), entry(5, pubkey(5), 1))))
	ups, err = f.k.EndBlocker(f.ctx)
	require.NoError(t, err)
	require.Len(t, ups, 3)
	powers := map[string]int64{}
	for i, u := range ups {
		powers[string(u.PubKey.GetEd25519())] = u.Power
		if i > 0 {
			a := types.ConsAddress(ups[i-1].PubKey.GetEd25519())
			b := types.ConsAddress(u.PubKey.GetEd25519())
			require.Negative(t, bytes.Compare(a, b), "sorted by consensus address")
		}
	}
	require.Equal(t, map[string]int64{string(pubkey(1)): 0, string(pubkey(4)): 1, string(pubkey(5)): 1}, powers)

	ups, err = f.k.EndBlocker(f.ctx)
	require.NoError(t, err)
	require.Empty(t, ups, "updates are sent once")

	// Remove then re-add within one block: no net change, no update.
	require.NoError(t, f.k.SetValidators(f.ctx, setMsg(entry(4, pubkey(4), 0))))
	require.NoError(t, f.k.SetValidators(f.ctx, setMsg(entry(4, pubkey(4), 1))))
	ups, err = f.k.EndBlocker(f.ctx)
	require.NoError(t, err)
	require.Empty(t, ups)
}

func TestValidatorSetUpdatedEvent(t *testing.T) {
	f := newFixture(t, 2)
	require.NoError(t, f.k.SetValidators(f.ctx, setMsg(entry(2, pubkey(2), 0), entry(5, pubkey(5), 1))))
	ev := lastEvent(t, f.ctx, types.EventTypeValidatorSetUpdated)
	require.Equal(t, addr(5).String(), attr(ev, types.AttributeKeyAdded))
	require.Equal(t, addr(2).String(), attr(ev, types.AttributeKeyRemoved))
}

func TestGenesisRoundTrip(t *testing.T) {
	f := newFixture(t, 3)
	require.NoError(t, f.k.SetValidators(f.ctx, setMsg(entry(2, pubkey(2), 0))))
	_, err := f.k.EndBlocker(f.ctx)
	require.NoError(t, err)

	exported, err := f.k.ExportGenesis(f.ctx)
	require.NoError(t, err)
	require.NoError(t, exported.Validate())
	require.Len(t, exported.Operators, 3)

	key := storetypes.NewKVStoreKey(types.StoreKey)
	tctx := testutil.DefaultContextWithDB(t, key, storetypes.NewTransientStoreKey("t2"))
	k2 := keeper.NewKeeper(moduletestutil.MakeTestEncodingConfig().Codec, runtime.NewKVStoreService(key))
	ups, err := k2.InitGenesis(tctx.Ctx, exported)
	require.NoError(t, err)
	require.Len(t, ups, 2, "only active operators")

	again, err := k2.ExportGenesis(tctx.Ctx)
	require.NoError(t, err)
	require.Equal(t, exported, again)

	// The removed operator's pubkey stays used after import.
	err = k2.SetValidators(tctx.Ctx.WithEventManager(sdk.NewEventManager()), setMsg(entry(7, pubkey(2), 1)))
	require.ErrorIs(t, err, types.ErrPubKeyUsed)
}

func TestUpdateParams(t *testing.T) {
	f := newFixture(t, 1)
	newAdmin := addr(0xBE)

	err := f.k.UpdateParams(f.ctx, &types.MsgUpdateParams{Admin: addr(9).String(), Params: types.Params{Admin: newAdmin.String()}})
	require.ErrorIs(t, err, types.ErrUnauthorized)

	err = f.k.UpdateParams(f.ctx, &types.MsgUpdateParams{Admin: admin.String(), Params: types.Params{Admin: "bad"}})
	require.ErrorIs(t, err, types.ErrInvalidAddress)

	require.NoError(t, f.k.UpdateParams(f.ctx, &types.MsgUpdateParams{Admin: admin.String(), Params: types.Params{Admin: newAdmin.String()}}))
	ev := lastEvent(t, f.ctx, types.EventTypeAdminChanged)
	require.Equal(t, admin.String(), attr(ev, types.AttributeKeyOld))
	require.Equal(t, newAdmin.String(), attr(ev, types.AttributeKeyNew))

	got, err := f.k.Admin(f.ctx)
	require.NoError(t, err)
	require.Equal(t, newAdmin, got)
	require.ErrorIs(t, f.k.CheckAdmin(f.ctx, admin.String()), types.ErrUnauthorized)
	require.NoError(t, f.k.CheckAdmin(f.ctx, newAdmin.String()))
}

func TestReadMethods(t *testing.T) {
	f := newFixture(t, 3)
	require.NoError(t, f.k.SetValidators(f.ctx, setMsg(entry(2, pubkey(2), 0))))

	active, err := f.k.ActiveValidators(f.ctx)
	require.NoError(t, err)
	require.Equal(t, []types.Operator{op(1, 1), op(3, 1)}, active)

	all, err := f.k.AllOperators(f.ctx)
	require.NoError(t, err)
	require.Len(t, all, 3)

	total, err := f.k.TotalPower(f.ctx)
	require.NoError(t, err)
	require.Equal(t, int64(2), total)

	_, err = f.k.GetOperator(f.ctx, addr(9))
	require.ErrorIs(t, err, types.ErrUnknownOperator)
}

func TestQueries(t *testing.T) {
	f := newFixture(t, 2)
	q := keeper.NewQuerier(f.k)

	p, err := q.Params(f.ctx, &types.QueryParamsRequest{})
	require.NoError(t, err)
	require.Equal(t, admin.String(), p.Params.Admin)

	vs, err := q.Validators(f.ctx, &types.QueryValidatorsRequest{})
	require.NoError(t, err)
	require.Equal(t, []types.Operator{op(1, 1), op(2, 1)}, vs.Validators)

	v, err := q.Validator(f.ctx, &types.QueryValidatorRequest{Address: addr(2).String()})
	require.NoError(t, err)
	require.Equal(t, op(2, 1), v.Validator)

	_, err = q.Validator(f.ctx, &types.QueryValidatorRequest{Address: addr(9).String()})
	require.Error(t, err)

	tp, err := q.TotalPower(f.ctx, &types.QueryTotalPowerRequest{})
	require.NoError(t, err)
	require.Equal(t, int64(2), tp.TotalPower)
}

func TestMsgServer(t *testing.T) {
	f := newFixture(t, 1)
	ms := keeper.NewMsgServerImpl(f.k)
	_, err := ms.SetValidators(f.ctx, setMsg(entry(2, pubkey(2), 1)))
	require.NoError(t, err)
	_, err = ms.UpdateParams(f.ctx, &types.MsgUpdateParams{Admin: addr(3).String(), Params: types.Params{Admin: addr(3).String()}})
	require.ErrorIs(t, err, types.ErrUnauthorized)
}

func lastEvent(t *testing.T, ctx sdk.Context, typ string) sdk.Event {
	t.Helper()
	evs := ctx.EventManager().Events()
	for i := len(evs) - 1; i >= 0; i-- {
		if evs[i].Type == typ {
			return evs[i]
		}
	}
	t.Fatalf("no %s event", typ)
	return sdk.Event{}
}

func attr(ev sdk.Event, key string) string {
	for _, a := range ev.Attributes {
		if a.Key == key {
			return a.Value
		}
	}
	return ""
}
