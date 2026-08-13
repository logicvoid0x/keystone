package types

import (
	errorsmod "cosmossdk.io/errors"

	sdk "github.com/cosmos/cosmos-sdk/types"
)

var (
	_ sdk.Msg              = (*MsgUpdateParams)(nil)
	_ sdk.HasValidateBasic = (*MsgUpdateParams)(nil)
)

// ValidateBasic checks the signer address and the params bounds.
func (m *MsgUpdateParams) ValidateBasic() error {
	if _, err := sdk.AccAddressFromBech32(m.Admin); err != nil {
		return errorsmod.Wrapf(ErrInvalidAddress, "admin %q: %v", m.Admin, err)
	}
	return m.Params.Validate()
}

var (
	_ sdk.Msg              = (*MsgWithdrawClaim)(nil)
	_ sdk.HasValidateBasic = (*MsgWithdrawClaim)(nil)
)

// ValidateBasic checks the owner, the recipient script kind and a positive amount.
func (m *MsgWithdrawClaim) ValidateBasic() error {
	if _, err := sdk.AccAddressFromBech32(m.Owner); err != nil {
		return errorsmod.Wrapf(ErrInvalidAddress, "owner %q: %v", m.Owner, err)
	}
	if _, err := WithdrawScriptKind(m.RecipientScript); err != nil {
		return errorsmod.Wrap(ErrInvalidScript, err.Error())
	}
	if m.AmountSat.IsNil() || !m.AmountSat.IsPositive() {
		return errorsmod.Wrapf(ErrInvalidAmount, "amount %v", m.AmountSat)
	}
	return nil
}
