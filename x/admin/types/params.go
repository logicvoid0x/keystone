package types

import (
	errorsmod "cosmossdk.io/errors"

	sdk "github.com/cosmos/cosmos-sdk/types"
)

// DefaultParams has an empty admin; genesis tooling always sets one.
func DefaultParams() Params { return Params{} }

// Validate checks the admin is a valid account address.
func (p Params) Validate() error {
	if _, err := sdk.AccAddressFromBech32(p.Admin); err != nil {
		return errorsmod.Wrapf(ErrInvalidAddress, "admin %q: %v", p.Admin, err)
	}
	return nil
}
