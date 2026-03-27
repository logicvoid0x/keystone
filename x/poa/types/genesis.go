package types

import sdk "github.com/cosmos/cosmos-sdk/types"

// DefaultGenesis has no admin and no operators, so it is not a valid chain genesis.
// `keystoned testnet init-files` writes a complete one.
func DefaultGenesis() *GenesisState {
	return &GenesisState{Params: DefaultParams()}
}

// Validate checks params, each operator, ordering, pubkey uniqueness, and that at
// least one operator is active.
func (gs GenesisState) Validate() error {
	if err := gs.Params.Validate(); err != nil {
		return err
	}
	addrs := make([]sdk.AccAddress, len(gs.Operators))
	keys := make([][]byte, len(gs.Operators))
	active := false
	for i, op := range gs.Operators {
		acc, err := validateRecord(op.OperatorAddress, op.ConsensusPubkey, op.Power)
		if err != nil {
			return err
		}
		addrs[i], keys[i] = acc, op.ConsensusPubkey
		active = active || op.Power > 0
	}
	if err := checkSorted(addrs); err != nil {
		return err
	}
	if err := checkDistinctPubKeys(keys); err != nil {
		return err
	}
	if !active {
		return ErrEmptyValidatorSet
	}
	return nil
}
