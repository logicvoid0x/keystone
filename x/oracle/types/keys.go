package types

import "cosmossdk.io/collections"

const (
	ModuleName = "oracle"
	StoreKey   = ModuleName
)

// Store prefixes (design §10.1). Never reuse a value.
var (
	ParamsKey     = collections.NewPrefix(0x01)
	PriceStateKey = collections.NewPrefix(0x02)
	TwapRingKey   = collections.NewPrefix(0x03) // ring index -> spot
	MissBitsKey   = collections.NewPrefix(0x04) // (consAddr, window index) -> missed (only true bits stored)
	MissCountKey  = collections.NewPrefix(0x05) // consAddr -> miss count
	TwapMetaKey   = collections.NewPrefix(0x06) // TwapMeta (M1 addition)
)

// Events (design §11.3).
const (
	EventTypePriceUpdated        = "price_updated"
	EventTypePriceStatus         = "price_status"
	EventTypeOracleMissThreshold = "oracle_miss_threshold"

	AttributeKeySpot             = "spot"
	AttributeKeyMedian           = "median"
	AttributeKeyParticipationBps = "participation_bps"
	AttributeKeyStatus           = "status"
	AttributeKeyValidator        = "validator"
	AttributeKeyMisses           = "misses"
)
