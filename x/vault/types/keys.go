package types

import "cosmossdk.io/collections"

const (
	ModuleName = "vault"
	StoreKey   = ModuleName
)

// Store prefixes (design §10.1). Never reuse a value.
var (
	ParamsKey        = collections.NewPrefix(0x01)
	VaultsKey        = collections.NewPrefix(0x02) // vault id -> Vault
	NextVaultIDKey   = collections.NewPrefix(0x03) // sequence
	OwnerIndexKey    = collections.NewPrefix(0x04) // (owner, vault id) -> empty
	LiqIndexKey      = collections.NewPrefix(0x05) // (inverted ratio 32 B, vault id) -> true (§8.1)
	RateStateKey     = collections.NewPrefix(0x06) // RateState
	TotalArtKey      = collections.NewPrefix(0x07) // total art
	CeilingMarkKey   = collections.NewPrefix(0x08) // CeilingMark at the last mint (M1)
	RoundingSlackKey = collections.NewPrefix(0x09) // rounding-slack bound for invariant 1 (M1)
)

// Events (design §11.3).
const (
	EventTypeVaultOpened         = "vault_opened"
	EventTypeMint                = "mint"
	EventTypeRepay               = "repay"
	EventTypeCollateralCredited  = "collateral_credited"
	EventTypeCollateralWithdrawn = "collateral_withdrawn"

	AttributeKeyID        = "id"
	AttributeKeyOwner     = "owner"
	AttributeKeyAddress   = "address"
	AttributeKeyAmount    = "amount"
	AttributeKeySat       = "sat"
	AttributeKeyRequestID = "request_id"
)
