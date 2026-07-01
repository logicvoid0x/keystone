package types

import (
	errorsmod "cosmossdk.io/errors"
	sdkmath "cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"
)

var (
	_ sdk.HasValidateBasic = (*MsgUpdateParams)(nil)
	_ sdk.HasValidateBasic = (*MsgOpenVault)(nil)
	_ sdk.HasValidateBasic = (*MsgMint)(nil)
	_ sdk.HasValidateBasic = (*MsgRepay)(nil)
	_ sdk.HasValidateBasic = (*MsgWithdrawCollateral)(nil)
	_ sdk.HasValidateBasic = (*MsgCloseVault)(nil)
	_ sdk.HasValidateBasic = (*MsgDepositClaim)(nil)
)

func checkAddr(field, a string) error {
	if _, err := sdk.AccAddressFromBech32(a); err != nil {
		return errorsmod.Wrapf(ErrInvalidAddress, "%s %q: %v", field, a, err)
	}
	return nil
}

func checkPositive(field string, v sdkmath.Int) error {
	if v.IsNil() || !v.IsPositive() {
		return errorsmod.Wrapf(ErrInvalidAmount, "%s must be positive", field)
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

func (m *MsgOpenVault) ValidateBasic() error { return checkAddr("owner", m.Owner) }

func (m *MsgMint) ValidateBasic() error {
	if err := checkAddr("owner", m.Owner); err != nil {
		return err
	}
	return checkPositive("amount", m.Amount)
}

func (m *MsgRepay) ValidateBasic() error {
	if err := checkAddr("sender", m.Sender); err != nil {
		return err
	}
	return checkPositive("amount", m.Amount)
}

func (m *MsgWithdrawCollateral) ValidateBasic() error {
	if err := checkAddr("owner", m.Owner); err != nil {
		return err
	}
	return checkPositive("amount_sat", m.AmountSat)
}

func (m *MsgCloseVault) ValidateBasic() error { return checkAddr("owner", m.Owner) }

func (m *MsgDepositClaim) ValidateBasic() error {
	if err := checkAddr("owner", m.Owner); err != nil {
		return err
	}
	return checkPositive("amount_sat", m.AmountSat)
}
