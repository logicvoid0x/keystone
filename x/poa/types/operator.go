package types

import (
	"bytes"

	"github.com/cometbft/cometbft/crypto/ed25519"

	errorsmod "cosmossdk.io/errors"

	sdk "github.com/cosmos/cosmos-sdk/types"
)

// ConsAddress derives the CometBFT consensus address of a raw ed25519 pubkey.
func ConsAddress(pubkey []byte) sdk.ConsAddress {
	return sdk.ConsAddress(ed25519.PubKey(pubkey).Address())
}

// ConsAddress returns the operator's consensus address.
func (o Operator) ConsAddress() sdk.ConsAddress { return ConsAddress(o.ConsensusPubkey) }

// Validate checks the record without state.
func (o Operator) Validate() error {
	_, err := validateRecord(o.OperatorAddress, o.ConsensusPubkey, o.Power)
	return err
}

// validateRecord checks an operator record or entry without state.
func validateRecord(addr string, pubkey []byte, power int64) (sdk.AccAddress, error) {
	acc, err := sdk.AccAddressFromBech32(addr)
	if err != nil {
		return nil, errorsmod.Wrapf(ErrInvalidAddress, "operator %q: %v", addr, err)
	}
	if len(pubkey) != PubKeySize {
		return nil, errorsmod.Wrapf(ErrInvalidPubKey, "operator %s: want %d bytes, got %d", addr, PubKeySize, len(pubkey))
	}
	if power != 0 && power != 1 {
		return nil, errorsmod.Wrapf(ErrInvalidPower, "operator %s: power %d", addr, power)
	}
	return acc, nil
}

// checkSorted requires addresses to be strictly ascending by bytes.
func checkSorted(addrs []sdk.AccAddress) error {
	for i := 1; i < len(addrs); i++ {
		if bytes.Compare(addrs[i-1], addrs[i]) >= 0 {
			return errorsmod.Wrapf(ErrUnsorted, "at index %d", i)
		}
	}
	return nil
}

// checkDistinctPubKeys rejects two records sharing a consensus pubkey.
func checkDistinctPubKeys(keys [][]byte) error {
	for i := range keys {
		for j := 0; j < i; j++ {
			if bytes.Equal(keys[i], keys[j]) {
				return errorsmod.Wrapf(ErrPubKeyUsed, "records %d and %d share a pubkey", j, i)
			}
		}
	}
	return nil
}
