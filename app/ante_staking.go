package app

import (
	"context"

	errorsmod "cosmossdk.io/errors"

	sdk "github.com/cosmos/cosmos-sdk/types"
	slashingtypes "github.com/cosmos/cosmos-sdk/x/slashing/types"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"

	admintypes "github.com/OWNER/keystone/x/admin/types"
)

// Errors of the staking message rules (design §4.2, M7).
var (
	ErrKeyRotationDisabled = errorsmod.Register("keystone_ante", 3, "consensus key rotation is disabled (vote extensions)")
	ErrNotAllowlisted      = errorsmod.Register("keystone_ante", 4, "validator operator is not allowlisted")
)

// Allowlist is x/admin as seen by the ante handler.
type Allowlist interface {
	IsAllowlisted(ctx context.Context, operator sdk.AccAddress) (bool, error)
}

// StakingMsgDecorator enforces the permissioned proof-of-stake rules on every message
// of a tx, including those nested in x/admin MsgExec:
//   - MsgRotateConsPubKey is rejected: a rotating validator's VEs cannot be verified
//     for two heights (ADR-015);
//   - MsgCreateValidator needs an allowlisted operator;
//   - MsgUnjail needs an allowlisted operator (removal from the allowlist jails).
type StakingMsgDecorator struct{ allow Allowlist }

func NewStakingMsgDecorator(allow Allowlist) StakingMsgDecorator {
	return StakingMsgDecorator{allow: allow}
}

func (d StakingMsgDecorator) AnteHandle(ctx sdk.Context, tx sdk.Tx, simulate bool, next sdk.AnteHandler) (sdk.Context, error) {
	if err := d.check(ctx, tx.GetMsgs()); err != nil {
		return ctx, err
	}
	return next(ctx, tx, simulate)
}

func (d StakingMsgDecorator) check(ctx sdk.Context, msgs []sdk.Msg) error {
	for _, m := range msgs {
		switch msg := m.(type) {
		case *stakingtypes.MsgRotateConsPubKey:
			return ErrKeyRotationDisabled
		case *stakingtypes.MsgCreateValidator:
			if err := d.requireListed(ctx, msg.ValidatorAddress); err != nil {
				return err
			}
		case *slashingtypes.MsgUnjail:
			if err := d.requireListed(ctx, msg.ValidatorAddr); err != nil {
				return err
			}
		case *admintypes.MsgExec:
			inner, err := msg.SdkMsgs()
			if err != nil {
				return err
			}
			if err := d.check(ctx, inner); err != nil {
				return err
			}
		}
	}
	return nil
}

func (d StakingMsgDecorator) requireListed(ctx sdk.Context, valoper string) error {
	val, err := sdk.ValAddressFromBech32(valoper)
	if err != nil {
		return errorsmod.Wrapf(ErrNotAllowlisted, "validator %q: %v", valoper, err)
	}
	ok, err := d.allow.IsAllowlisted(ctx, sdk.AccAddress(val))
	if err != nil {
		return err
	}
	if !ok {
		return errorsmod.Wrapf(ErrNotAllowlisted, "%s", sdk.AccAddress(val))
	}
	return nil
}
