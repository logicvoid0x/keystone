package types

import "cosmossdk.io/collections"

const (
	ModuleName = "admin"
	StoreKey   = ModuleName
)

// Store prefixes (design §10.1). Never reuse a value.
var (
	ParamsKey    = collections.NewPrefix(0x01)
	AllowlistKey = collections.NewPrefix(0x02)
)
