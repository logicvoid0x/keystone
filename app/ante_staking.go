package app

import (
	"context"

	errorsmod "cosmossdk.io/errors"
	sdkmath "cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"
	slashingtypes "github.com/cosmos/cosmos-sdk/x/slashing/types"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"

	admintypes "github.com/logicvoid0x/keystone/x/admin/types"
)

// Errors of the staking message rules (design §4.2, §9.4, M7).
var (
	ErrKeyRotationDisabled = errorsmod.Register("keystone_ante", 3, "consensus key rotation is disabled (vote extensions)")
	ErrNotAllowlisted      = errorsmod.Register("keystone_ante", 4, "validator operator is not allowlisted")
	ErrCustodySelfBond     = errorsmod.Register("keystone_ante", 5, "custody share holder cannot unbond its self-delegation below the floor")
)

// Allowlist is x/admin as seen by the ante handler.
type Allowlist interface {
	IsAllowlisted(ctx context.Context, operator sdk.AccAddress) (bool, error)
}

// CustodyBond is x/custody as seen by the unbonding guard.
type CustodyBond interface {
	SelfBondFloor(ctx context.Context, operator sdk.AccAddress) (sdkmath.Int, bool, error)
}

// Delegations is x/staking as seen by the unbonding guard.
type Delegations interface {
	GetDelegation(ctx context.Context, del sdk.AccAddress, val sdk.ValAddress) (stakingtypes.Delegation, error)
	GetValidator(ctx context.Context, val sdk.ValAddress) (stakingtypes.Validator, error)
}

// PosAnte are the keepers the staking message rules read.
type PosAnte struct {
	Allowlist Allowlist
	Custody   CustodyBond
	Staking   Delegations
}

// StakingMsgDecorator enforces the permissioned proof-of-stake rules on every message
// of a tx, including those nested in x/admin MsgExec:
//   - MsgRotateConsPubKey is rejected: a rotating validator's VEs cannot be verified
//     for two heights (ADR-015);
//   - MsgCreateValidator needs an allowlisted operator;
//   - MsgUnjail needs an allowlisted operator (removal from the allowlist jails);
//   - MsgUndelegate and MsgBeginRedelegate of a validator's own self-delegation are
//     rejected when the validator holds a share of a funded custody epoch and the tx
//     would take its self-bond below MinCustodySelfBond (design §9.4). Amounts are
//     summed per validator over the tx.
type StakingMsgDecorator struct{ k PosAnte }

func NewStakingMsgDecorator(k PosAnte) StakingMsgDecorator { return StakingMsgDecorator{k: k} }

func (d StakingMsgDecorator) AnteHandle(ctx sdk.Context, tx sdk.Tx, simulate bool, next sdk.AnteHandler) (sdk.Context, error) {
	unbond := map[string]sdkmath.Int{}
	var order []sdk.ValAddress
	if err := d.check(ctx, tx.GetMsgs(), unbond, &order); err != nil {
		return ctx, err
	}
	for _, val := range order {
		if err := d.checkSelfBond(ctx, val, unbond[string(val)]); err != nil {
			return ctx, err
		}
	}
	return next(ctx, tx, simulate)
}

func (d StakingMsgDecorator) check(ctx sdk.Context, msgs []sdk.Msg, unbond map[string]sdkmath.Int, order *[]sdk.ValAddress) error {
	addSelf := func(del, valoper string, amt sdk.Coin) error {
		val, err := sdk.ValAddressFromBech32(valoper)
		if err != nil {
			return err
		}
		delAcc, err := sdk.AccAddressFromBech32(del)
		if err != nil {
			return err
		}
		if !delAcc.Equals(sdk.AccAddress(val)) {
			return nil // only the operator's own delegation is guarded
		}
		prev, ok := unbond[string(val)]
		if !ok {
			prev = sdkmath.ZeroInt()
			*order = append(*order, val)
		}
		unbond[string(val)] = prev.Add(amt.Amount)
		return nil
	}
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
		case *stakingtypes.MsgUndelegate:
			if err := addSelf(msg.DelegatorAddress, msg.ValidatorAddress, msg.Amount); err != nil {
				return err
			}
		case *stakingtypes.MsgBeginRedelegate:
			if err := addSelf(msg.DelegatorAddress, msg.ValidatorSrcAddress, msg.Amount); err != nil {
				return err
			}
		case *admintypes.MsgExec:
			inner, err := msg.SdkMsgs()
			if err != nil {
				return err
			}
			if err := d.check(ctx, inner, unbond, order); err != nil {
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
	ok, err := d.k.Allowlist.IsAllowlisted(ctx, sdk.AccAddress(val))
	if err != nil {
		return err
	}
	if !ok {
		return errorsmod.Wrapf(ErrNotAllowlisted, "%s", sdk.AccAddress(val))
	}
	return nil
}

// checkSelfBond rejects unbonding amt of val's self-delegation if val is guarded and
// the rest would fall below the floor. A missing delegation or validator is left to
// x/staking to reject.
func (d StakingMsgDecorator) checkSelfBond(ctx sdk.Context, val sdk.ValAddress, amt sdkmath.Int) error {
	floor, guarded, err := d.k.Custody.SelfBondFloor(ctx, sdk.AccAddress(val))
	if err != nil || !guarded {
		return err
	}
	del, err := d.k.Staking.GetDelegation(ctx, sdk.AccAddress(val), val)
	if err != nil {
		return nil
	}
	v, err := d.k.Staking.GetValidator(ctx, val)
	if err != nil {
		return nil
	}
	left := v.TokensFromShares(del.Shares).TruncateInt().Sub(amt)
	if left.LT(floor) {
		return errorsmod.Wrapf(ErrCustodySelfBond, "%s would keep %s, floor %s", sdk.AccAddress(val), left, floor)
	}
	return nil
}
