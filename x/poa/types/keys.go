package types

import "cosmossdk.io/collections"

const (
	ModuleName = "poa"
	StoreKey   = ModuleName
)

// Store prefixes (design §10.1). Never reuse a value.
var (
	ParamsKey      = collections.NewPrefix(0x01)
	OperatorsKey   = collections.NewPrefix(0x02)
	ConsIndexKey   = collections.NewPrefix(0x03)
	UsedPubKeysKey = collections.NewPrefix(0x04)
	LastPowerKey   = collections.NewPrefix(0x05)
)

// PubKeySize is the size of a raw ed25519 consensus public key.
const PubKeySize = 32
