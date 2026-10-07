// Package validators holds the validator record x/oracle and x/custody read from the
// app's validator set (app/valset over x/staking, design §4.2). Keeping it here means
// neither module imports x/staking.
package validators

import (
	"github.com/cometbft/cometbft/crypto/ed25519"

	sdk "github.com/cosmos/cosmos-sdk/types"
)

// Validator is one validator as the Keystone modules see it.
type Validator struct {
	// OperatorAddress is the operator's account address (bech32); its bytes are the
	// valoper address bytes.
	OperatorAddress string
	// ConsensusPubkey is the raw 32-byte ed25519 consensus key.
	ConsensusPubkey []byte
	// Power is the CometBFT voting power (bonded tokens / PowerReduction); 0 when the
	// validator is not in the active set.
	Power int64
}

// ConsAddress returns the CometBFT consensus address of the validator.
func (v Validator) ConsAddress() sdk.ConsAddress {
	return sdk.ConsAddress(ed25519.PubKey(v.ConsensusPubkey).Address())
}
