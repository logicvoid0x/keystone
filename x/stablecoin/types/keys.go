package types

import "cosmossdk.io/collections"

const (
	ModuleName = "stablecoin"
	StoreKey   = ModuleName
)

// Store prefixes (design §10.1). Later milestones add the rest; never reuse a value.
var ParamsKey = collections.NewPrefix(0x01)
