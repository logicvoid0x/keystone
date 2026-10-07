// Package valset adapts x/staking to the validator set x/oracle and x/custody read
// (design §4.2, M7). It reads committed staking state only, so it is deterministic.
package valset

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"slices"

	sdk "github.com/cosmos/cosmos-sdk/types"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"

	"github.com/OWNER/keystone/x/validators"
)

// StakingKeeper is the part of x/staking the adapter reads.
type StakingKeeper interface {
	IterateLastValidatorPowers(ctx context.Context, fn func(operator sdk.ValAddress, power int64) (stop bool)) error
	GetValidator(ctx context.Context, addr sdk.ValAddress) (stakingtypes.Validator, error)
	GetLastValidatorPower(ctx context.Context, operator sdk.ValAddress) (int64, error)
}

// Adapter implements the oracle's and custody's ValidatorSet.
type Adapter struct{ sk StakingKeeper }

// New returns the adapter.
func New(sk StakingKeeper) Adapter { return Adapter{sk: sk} }

// ActiveValidators returns the validators in the set last sent to CometBFT (power > 0)
// that are not jailed, sorted by operator address bytes: the contract x/poa had.
func (a Adapter) ActiveValidators(ctx context.Context) ([]validators.Validator, error) {
	type entry struct {
		op    sdk.ValAddress
		power int64
	}
	var entries []entry
	if err := a.sk.IterateLastValidatorPowers(ctx, func(op sdk.ValAddress, power int64) bool {
		if power > 0 {
			entries = append(entries, entry{slices.Clone(op), power})
		}
		return false
	}); err != nil {
		return nil, err
	}
	out := make([]validators.Validator, 0, len(entries))
	for _, e := range entries {
		v, err := a.sk.GetValidator(ctx, e.op)
		if err != nil {
			return nil, err
		}
		if v.IsJailed() {
			continue
		}
		rec, err := record(v, e.op, e.power)
		if err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	slices.SortFunc(out, func(x, y validators.Validator) int {
		return bytes.Compare(sdk.MustAccAddressFromBech32(x.OperatorAddress), sdk.MustAccAddressFromBech32(y.OperatorAddress))
	})
	return out, nil
}

// TotalPower is the sum of the active validators' power.
func (a Adapter) TotalPower(ctx context.Context) (int64, error) {
	vals, err := a.ActiveValidators(ctx)
	if err != nil {
		return 0, err
	}
	var total int64
	for _, v := range vals {
		total += v.Power
	}
	return total, nil
}

// GetOperator returns the validator run by operator, of any status; its power is 0
// unless it is in the last CometBFT set and not jailed.
func (a Adapter) GetOperator(ctx context.Context, operator sdk.AccAddress) (validators.Validator, error) {
	val := sdk.ValAddress(operator)
	v, err := a.sk.GetValidator(ctx, val)
	if errors.Is(err, stakingtypes.ErrNoValidatorFound) {
		return validators.Validator{}, fmt.Errorf("unknown validator operator %s", operator)
	}
	if err != nil {
		return validators.Validator{}, err
	}
	power, err := a.sk.GetLastValidatorPower(ctx, val)
	if err != nil {
		return validators.Validator{}, err
	}
	if v.IsJailed() {
		power = 0
	}
	return record(v, val, power)
}

func record(v stakingtypes.Validator, op sdk.ValAddress, power int64) (validators.Validator, error) {
	pk, err := v.ConsPubKey()
	if err != nil {
		return validators.Validator{}, err
	}
	raw := pk.Bytes()
	if len(raw) != 32 {
		return validators.Validator{}, fmt.Errorf("validator %s: consensus key is not ed25519 (%d bytes)", op, len(raw))
	}
	return validators.Validator{OperatorAddress: sdk.AccAddress(op).String(), ConsensusPubkey: raw, Power: power}, nil
}
