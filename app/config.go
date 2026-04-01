package app

import (
	sdk "github.com/cosmos/cosmos-sdk/types"
)

const (
	// AppName is the binary and app name.
	AppName = "keystoned"

	// Bech32 prefixes (docs/decisions.md: the design names none).
	AccountAddressPrefix   = "kstn"
	ValidatorAddressPrefix = AccountAddressPrefix + "valoper"
	ConsensusAddressPrefix = AccountAddressPrefix + "valcons"
	AccountPubKeyPrefix    = AccountAddressPrefix + "pub"
	ValidatorPubKeyPrefix  = ValidatorAddressPrefix + "pub"
	ConsensusPubKeyPrefix  = ConsensusAddressPrefix + "pub"

	// Denom is the KUSD base denom (1e-6 KUSD).
	Denom = "ukusd"

	// MaxTxPerAccountPerBlock bounds user transactions per signer per block (design §5.6).
	MaxTxPerAccountPerBlock = 5
)

// SetAddressConfig sets the global bech32 prefixes. Safe to call more than once
// before the config is sealed.
func SetAddressConfig() {
	cfg := sdk.GetConfig()
	if cfg.GetBech32AccountAddrPrefix() == AccountAddressPrefix {
		return
	}
	cfg.SetBech32PrefixForAccount(AccountAddressPrefix, AccountPubKeyPrefix)
	cfg.SetBech32PrefixForValidator(ValidatorAddressPrefix, ValidatorPubKeyPrefix)
	cfg.SetBech32PrefixForConsensusNode(ConsensusAddressPrefix, ConsensusPubKeyPrefix)
	cfg.Seal()
}
