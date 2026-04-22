package types

import (
	errorsmod "cosmossdk.io/errors"
	sdkmath "cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"
)

var (
	_ sdk.HasValidateBasic = (*MsgUpdateParams)(nil)
	_ sdk.HasValidateBasic = (*MsgDepositPool)(nil)
	_ sdk.HasValidateBasic = (*MsgWithdrawPool)(nil)
	_ sdk.HasValidateBasic = (*MsgTakeCollateral)(nil)
)

func checkAddr(field, a string) error {
	if _, err := sdk.AccAddressFromBech32(a); err != nil {
		return errorsmod.Wrapf(ErrInvalidAddress, "%s %q: %v", field, a, err)
	}
	return nil
}

func checkAmount(field string, v sdkmath.Int, allowZero bool) error {
	if v.IsNil() || v.IsNegative() || (!allowZero && v.IsZero()) {
		return errorsmod.Wrapf(ErrInvalidAmount, "%s", field)
	}
	return nil
}

// ValidateBasic checks the signer address and the params bounds.
func (m *MsgUpdateParams) ValidateBasic() error {
	if err := checkAddr("admin", m.Admin); err != nil {
		return err
	}
	return m.Params.Validate()
}

func (m *MsgDepositPool) ValidateBasic() error {
	if err := checkAddr("depositor", m.Depositor); err != nil {
		return err
	}
	return checkAmount("amount", m.Amount, false)
}

// ValidateBasic allows amount 0 (claim gains only).
func (m *MsgWithdrawPool) ValidateBasic() error {
	if err := checkAddr("depositor", m.Depositor); err != nil {
		return err
	}
	return checkAmount("amount", m.Amount, true)
}

func (m *MsgTakeCollateral) ValidateBasic() error {
	if err := checkAddr("taker", m.Taker); err != nil {
		return err
	}
	if err := checkAmount("max_sat", m.MaxSat, false); err != nil {
		return err
	}
	return checkAmount("max_price", m.MaxPrice, false)
}
