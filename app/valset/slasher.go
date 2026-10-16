package valset

import (
	"context"

	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"
)

// SlashStaking is the part of x/staking theft slashing uses.
type SlashStaking interface {
	GetValidator(ctx context.Context, addr sdk.ValAddress) (stakingtypes.Validator, error)
	GetUnbondingDelegationsFromValidator(ctx context.Context, val sdk.ValAddress) ([]stakingtypes.UnbondingDelegation, error)
	GetRedelegationsFromSrcValidator(ctx context.Context, val sdk.ValAddress) ([]stakingtypes.Redelegation, error)
	TokensToConsensusPower(ctx context.Context, tokens math.Int) int64
	Slash(ctx context.Context, cons sdk.ConsAddress, infractionHeight, power int64, factor math.LegacyDec) (math.Int, error)
}

// SlashSlashing is the part of x/slashing theft slashing uses.
type SlashSlashing interface {
	Jail(ctx context.Context, cons sdk.ConsAddress) error
	Tombstone(ctx context.Context, cons sdk.ConsAddress) error
	IsTombstoned(ctx context.Context, cons sdk.ConsAddress) bool
}

// Slasher implements x/custody's StakingSlasher (design §9.12).
type Slasher struct {
	sk SlashStaking
	sl SlashSlashing
}

// NewSlasher returns the theft slasher.
func NewSlasher(sk SlashStaking, sl SlashSlashing) Slasher { return Slasher{sk: sk, sl: sl} }

// SlashForTheft slashes operator's validator by fraction for an infraction at
// infractionHeight, then jails and tombstones it. x/staking burns
// power × fraction tokens, taking first from unbonding and redelegation entries
// created since the infraction and the rest from the validator, so power covers the
// validator's tokens plus those entries: with fraction 1 everything still at stake is
// burned.
func (s Slasher) SlashForTheft(ctx context.Context, operator sdk.AccAddress, infractionHeight int64, fraction math.LegacyDec) (math.Int, error) {
	val := sdk.ValAddress(operator)
	v, err := s.sk.GetValidator(ctx, val)
	if err != nil {
		return math.ZeroInt(), err
	}
	cons, err := v.GetConsAddr()
	if err != nil {
		return math.ZeroInt(), err
	}
	exposure := v.Tokens
	ubds, err := s.sk.GetUnbondingDelegationsFromValidator(ctx, val)
	if err != nil {
		return math.ZeroInt(), err
	}
	for _, u := range ubds {
		for _, e := range u.Entries {
			if e.CreationHeight >= infractionHeight {
				exposure = exposure.Add(e.InitialBalance)
			}
		}
	}
	reds, err := s.sk.GetRedelegationsFromSrcValidator(ctx, val)
	if err != nil {
		return math.ZeroInt(), err
	}
	for _, r := range reds {
		for _, e := range r.Entries {
			if e.CreationHeight >= infractionHeight {
				exposure = exposure.Add(e.InitialBalance)
			}
		}
	}
	power := s.sk.TokensToConsensusPower(ctx, exposure) + 1 // round up
	burned, err := s.sk.Slash(ctx, cons, infractionHeight, power, fraction)
	if err != nil {
		return math.ZeroInt(), err
	}
	if !v.IsJailed() {
		if err := s.sl.Jail(ctx, cons); err != nil {
			return math.ZeroInt(), err
		}
	}
	if !s.sl.IsTombstoned(ctx, cons) {
		if err := s.sl.Tombstone(ctx, cons); err != nil {
			return math.ZeroInt(), err
		}
	}
	return burned, nil
}
