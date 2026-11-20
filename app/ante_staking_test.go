package app_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	protov2 "google.golang.org/protobuf/proto"

	sdkmath "cosmossdk.io/math"

	cdctypes "github.com/cosmos/cosmos-sdk/codec/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	slashingtypes "github.com/cosmos/cosmos-sdk/x/slashing/types"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"

	"github.com/logicvoid0x/keystone/app"
	admintypes "github.com/logicvoid0x/keystone/x/admin/types"
)

type fakeTx struct{ msgs []sdk.Msg }

func (t fakeTx) GetMsgs() []sdk.Msg                    { return t.msgs }
func (t fakeTx) GetMsgsV2() ([]protov2.Message, error) { return nil, nil }

type fakePos struct {
	listed  map[string]bool
	guarded map[string]bool
	tokens  map[string]int64 // self-delegation tokens per operator
}

func (f fakePos) IsAllowlisted(_ context.Context, op sdk.AccAddress) (bool, error) {
	return f.listed[string(op)], nil
}

func (f fakePos) SelfBondFloor(_ context.Context, op sdk.AccAddress) (sdkmath.Int, bool, error) {
	return sdkmath.NewInt(100), f.guarded[string(op)], nil
}

func (f fakePos) GetDelegation(_ context.Context, del sdk.AccAddress, _ sdk.ValAddress) (stakingtypes.Delegation, error) {
	return stakingtypes.Delegation{Shares: sdkmath.LegacyNewDec(f.tokens[string(del)])}, nil
}

func (f fakePos) GetValidator(_ context.Context, val sdk.ValAddress) (stakingtypes.Validator, error) {
	n := sdkmath.NewInt(f.tokens[string(val)])
	return stakingtypes.Validator{Tokens: n, DelegatorShares: sdkmath.LegacyNewDecFromInt(n)}, nil
}

func acc(i byte) sdk.AccAddress { return sdk.AccAddress(bytes.Repeat([]byte{i}, 20)) }

func coin(n int64) sdk.Coin { return sdk.NewInt64Coin(app.BondDenom, n) }

func TestStakingMsgDecorator(t *testing.T) {
	op, other := acc(1), acc(2)
	pos := fakePos{
		listed:  map[string]bool{string(op): true},
		guarded: map[string]bool{string(op): true},
		tokens:  map[string]int64{string(op): 150},
	}
	d := app.NewStakingMsgDecorator(app.PosAnte{Allowlist: pos, Custody: pos, Staking: pos})
	val := sdk.ValAddress(op).String()
	undel := func(from sdk.AccAddress, n int64) sdk.Msg {
		return &stakingtypes.MsgUndelegate{DelegatorAddress: from.String(), ValidatorAddress: val, Amount: coin(n)}
	}
	exec := func(m sdk.Msg) sdk.Msg {
		a, err := cdctypes.NewAnyWithValue(m)
		require.NoError(t, err)
		return &admintypes.MsgExec{Admin: other.String(), Msgs: []*cdctypes.Any{a}}
	}
	cases := []struct {
		name string
		msgs []sdk.Msg
		err  error
	}{
		{"rotation", []sdk.Msg{&stakingtypes.MsgRotateConsPubKey{ValidatorAddress: val}}, app.ErrKeyRotationDisabled},
		{"rotation inside MsgExec", []sdk.Msg{exec(&stakingtypes.MsgRotateConsPubKey{ValidatorAddress: val})}, app.ErrKeyRotationDisabled},
		{"create listed", []sdk.Msg{&stakingtypes.MsgCreateValidator{ValidatorAddress: val}}, nil},
		{"create unlisted", []sdk.Msg{&stakingtypes.MsgCreateValidator{ValidatorAddress: sdk.ValAddress(other).String()}}, app.ErrNotAllowlisted},
		{"unjail unlisted", []sdk.Msg{&slashingtypes.MsgUnjail{ValidatorAddr: sdk.ValAddress(other).String()}}, app.ErrNotAllowlisted},
		{"self-undelegate above floor", []sdk.Msg{undel(op, 50)}, nil},
		{"self-undelegate below floor", []sdk.Msg{undel(op, 51)}, app.ErrCustodySelfBond},
		{"split across messages", []sdk.Msg{undel(op, 30), undel(op, 30)}, app.ErrCustodySelfBond},
		{"redelegate below floor", []sdk.Msg{&stakingtypes.MsgBeginRedelegate{DelegatorAddress: op.String(), ValidatorSrcAddress: val, ValidatorDstAddress: sdk.ValAddress(other).String(), Amount: coin(60)}}, app.ErrCustodySelfBond},
		{"other delegators are never blocked", []sdk.Msg{undel(other, 1_000)}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			next := func(ctx sdk.Context, _ sdk.Tx, _ bool) (sdk.Context, error) { return ctx, nil }
			_, err := d.AnteHandle(sdk.Context{}, fakeTx{tc.msgs}, false, next)
			if tc.err == nil {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, tc.err)
			}
		})
	}

	// An unguarded operator can unbond everything.
	pos.guarded[string(op)] = false
	_, err := d.AnteHandle(sdk.Context{}, fakeTx{[]sdk.Msg{undel(op, 150)}}, false,
		func(ctx sdk.Context, _ sdk.Tx, _ bool) (sdk.Context, error) { return ctx, nil })
	require.NoError(t, err)
}
