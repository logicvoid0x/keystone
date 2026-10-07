package types

import (
	errorsmod "cosmossdk.io/errors"

	sdk "github.com/cosmos/cosmos-sdk/types"
)

// DefaultGenesis has no admin and an empty allowlist, so it is not a valid chain
// genesis. `keystoned testnet init-files` writes a complete one.
func DefaultGenesis() *GenesisState {
	return &GenesisState{Params: DefaultParams()}
}

// Validate checks params, each entry, ordering, and that the allowlist is not empty.
// That every genesis validator is allowlisted is checked by the app (it needs the
// gentxs, design §10.4).
func (gs GenesisState) Validate() error {
	if err := gs.Params.Validate(); err != nil {
		return err
	}
	if len(gs.Allowlist) == 0 {
		return ErrEmptyAllowlist
	}
	addrs := make([]string, len(gs.Allowlist))
	for i, e := range gs.Allowlist {
		addrs[i] = e.Operator
	}
	_, err := ParseSorted(addrs)
	return err
}

// Allowed reports whether operator (account bytes) is in the genesis allowlist.
func (gs GenesisState) Allowed(operator sdk.AccAddress) bool {
	for _, e := range gs.Allowlist {
		if acc, err := sdk.AccAddressFromBech32(e.Operator); err == nil && acc.Equals(operator) {
			return true
		}
	}
	return false
}

// ValidateAllowlistCovers returns an error naming the first operator not allowlisted.
func (gs GenesisState) ValidateAllowlistCovers(operators []sdk.AccAddress) error {
	for _, op := range operators {
		if !gs.Allowed(op) {
			return errorsmod.Wrapf(ErrNotAllowlisted, "genesis validator %s", op)
		}
	}
	return nil
}
