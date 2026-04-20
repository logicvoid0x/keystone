package types

import "cosmossdk.io/collections"

const (
	ModuleName = "custody"
	StoreKey   = ModuleName
)

// Store prefixes (design §10.1). M1 uses only these; M3+ add the rest. Never reuse a value.
var (
	ParamsKey    = collections.NewPrefix(0x01)
	ClaimableKey = collections.NewPrefix(0x15) // address -> claimable sat
	CreditedKey  = collections.NewPrefix(0x1A) // credited sat (for the cap)
)
