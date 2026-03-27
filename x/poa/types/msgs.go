package types

import (
	errorsmod "cosmossdk.io/errors"

	sdk "github.com/cosmos/cosmos-sdk/types"
)

var (
	_ sdk.Msg              = (*MsgSetValidators)(nil)
	_ sdk.HasValidateBasic = (*MsgSetValidators)(nil)
	_ sdk.Msg              = (*MsgUpdateParams)(nil)
	_ sdk.HasValidateBasic = (*MsgUpdateParams)(nil)
)

// ValidateBasic checks the message without state: admin address, at least one entry,
// each entry well formed, entries sorted by operator address bytes, no shared pubkey.
func (m *MsgSetValidators) ValidateBasic() error {
	if _, err := sdk.AccAddressFromBech32(m.Admin); err != nil {
		return errorsmod.Wrapf(ErrInvalidAddress, "admin %q: %v", m.Admin, err)
	}
	if len(m.Entries) == 0 {
		return ErrEmptyEntries
	}
	addrs := make([]sdk.AccAddress, len(m.Entries))
	keys := make([][]byte, len(m.Entries))
	for i, e := range m.Entries {
		acc, err := validateRecord(e.OperatorAddress, e.ConsensusPubkey, e.Power)
		if err != nil {
			return err
		}
		addrs[i], keys[i] = acc, e.ConsensusPubkey
	}
	if err := checkSorted(addrs); err != nil {
		return err
	}
	return checkDistinctPubKeys(keys)
}

// ValidateBasic checks the admin and the new params.
func (m *MsgUpdateParams) ValidateBasic() error {
	if _, err := sdk.AccAddressFromBech32(m.Admin); err != nil {
		return errorsmod.Wrapf(ErrInvalidAddress, "admin %q: %v", m.Admin, err)
	}
	return m.Params.Validate()
}
