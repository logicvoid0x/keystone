package valset_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	cdctypes "github.com/cosmos/cosmos-sdk/codec/types"
	"github.com/cosmos/cosmos-sdk/crypto/keys/ed25519"
	sdk "github.com/cosmos/cosmos-sdk/types"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"

	"github.com/logicvoid0x/keystone/app/valset"
)

// fakeStaking iterates last powers in insertion order (deliberately not sorted).
type fakeStaking struct {
	order  []sdk.ValAddress
	powers map[string]int64
	vals   map[string]stakingtypes.Validator
}

func (f *fakeStaking) IterateLastValidatorPowers(_ context.Context, fn func(sdk.ValAddress, int64) bool) error {
	for _, op := range f.order {
		if fn(op, f.powers[string(op)]) {
			break
		}
	}
	return nil
}

func (f *fakeStaking) GetValidator(_ context.Context, a sdk.ValAddress) (stakingtypes.Validator, error) {
	v, ok := f.vals[string(a)]
	if !ok {
		return v, stakingtypes.ErrNoValidatorFound
	}
	return v, nil
}

func (f *fakeStaking) GetLastValidatorPower(_ context.Context, a sdk.ValAddress) (int64, error) {
	return f.powers[string(a)], nil
}

func op(i byte) sdk.ValAddress { return sdk.ValAddress(bytes.Repeat([]byte{i}, 20)) }

func (f *fakeStaking) add(t *testing.T, i byte, power int64, jailed bool) []byte {
	t.Helper()
	pk := ed25519.GenPrivKeyFromSecret([]byte{i}).PubKey()
	any, err := cdctypes.NewAnyWithValue(pk)
	require.NoError(t, err)
	f.order = append(f.order, op(i))
	f.powers[string(op(i))] = power
	f.vals[string(op(i))] = stakingtypes.Validator{OperatorAddress: op(i).String(), ConsensusPubkey: any, Jailed: jailed}
	return pk.Bytes()
}

func newFake() *fakeStaking {
	return &fakeStaking{powers: map[string]int64{}, vals: map[string]stakingtypes.Validator{}}
}

func TestActiveValidatorsSortedFilteredAndTotal(t *testing.T) {
	f := newFake()
	pk3 := f.add(t, 3, 30, false)
	f.add(t, 1, 10, true) // jailed: excluded
	pk2 := f.add(t, 2, 20, false)
	f.add(t, 4, 0, false) // not in the last set: excluded
	a := valset.New(f)

	vals, err := a.ActiveValidators(t.Context())
	require.NoError(t, err)
	require.Len(t, vals, 2)
	require.Equal(t, sdk.AccAddress(op(2)).String(), vals[0].OperatorAddress)
	require.Equal(t, pk2, vals[0].ConsensusPubkey)
	require.EqualValues(t, 20, vals[0].Power)
	require.Equal(t, sdk.AccAddress(op(3)).String(), vals[1].OperatorAddress)
	require.Equal(t, pk3, vals[1].ConsensusPubkey)
	require.Equal(t, sdk.ConsAddress(ed25519.GenPrivKeyFromSecret([]byte{3}).PubKey().Address()), vals[1].ConsAddress())

	total, err := a.TotalPower(t.Context())
	require.NoError(t, err)
	require.EqualValues(t, 50, total)
}

func TestGetOperator(t *testing.T) {
	f := newFake()
	f.add(t, 1, 10, true)
	f.add(t, 2, 20, false)
	a := valset.New(f)

	v, err := a.GetOperator(t.Context(), sdk.AccAddress(op(2)))
	require.NoError(t, err)
	require.EqualValues(t, 20, v.Power)
	v, err = a.GetOperator(t.Context(), sdk.AccAddress(op(1)))
	require.NoError(t, err)
	require.Zero(t, v.Power, "jailed validators have power 0")
	_, err = a.GetOperator(t.Context(), sdk.AccAddress(op(9)))
	require.Error(t, err)
}
