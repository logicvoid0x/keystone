package types

import "cosmossdk.io/collections"

const (
	ModuleName = "stablecoin"
	StoreKey   = ModuleName

	// Denom is KUSD's base denom (1e-6 KUSD).
	Denom = "ukusd"
	// SurplusModuleAccount holds stability-fee revenue (design §7.4). It has no
	// permissions; Heal moves KUSD to this module's account to burn it.
	SurplusModuleAccount = "surplus"
)

// Store prefixes (design §10.1). Never reuse a value.
var (
	ParamsKey       = collections.NewPrefix(0x01)
	SinKey          = collections.NewPrefix(0x02)
	SinOverSinceKey = collections.NewPrefix(0x03) // height since sin > SinPauseBps of supply, 0 = not over (M1)
	MintsPausedKey  = collections.NewPrefix(0x04) // bool (M1)
)

// Events (design §11.3).
const (
	EventTypeBadDebt     = "bad_debt"
	EventTypeHealed      = "healed"
	EventTypeMintsPaused = "mints_paused"

	AttributeKeyAmount = "amount"
	AttributeKeySource = "source"
	AttributeKeySin    = "sin"
)
