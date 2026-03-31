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
