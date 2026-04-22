package types

import (
	"cosmossdk.io/collections"
	sdkmath "cosmossdk.io/math"
)

const (
	ModuleName = "liquidation"
	StoreKey   = ModuleName
)

// Store prefixes (design §10.1). Never reuse a value.
var (
	ParamsKey        = collections.NewPrefix(0x01)
	PoolScalarsKey   = collections.NewPrefix(0x02) // PoolScalars{P, epoch, scale}
	PoolDepositsKey  = collections.NewPrefix(0x03) // depositor -> PoolDeposit
	AuctionsKey      = collections.NewPrefix(0x04) // auction id -> Auction
	NextAuctionIDKey = collections.NewPrefix(0x05) // sequence
	EpochScaleSumKey = collections.NewPrefix(0x06) // (epoch, scale) -> S (M1)
	TotalDepositsKey = collections.NewPrefix(0x07) // total compounded deposits, ukusd (M1)
	PendingSatKey    = collections.NewPrefix(0x08) // seized sat not yet paid to depositors (M1)
	PoolErrorsKey    = collections.NewPrefix(0x09) // PoolErrors error feedback (M1)
)

// Liquity precision constants (StabilityPool.sol).
var (
	// DecimalPrecision is 1e18, the precision of P and S.
	DecimalPrecision = sdkmath.NewIntWithDecimal(1, 18)
	// ScaleFactor is 1e9: P is rescaled when it would fall below it.
	ScaleFactor = sdkmath.NewIntWithDecimal(1, 9)
)

// Events (design §11.3).
const (
	EventTypeVaultLiquidated    = "vault_liquidated"
	EventTypeAuctionStarted     = "auction_started"
	EventTypeAuctionTaken       = "auction_taken"
	EventTypeAuctionReset       = "auction_reset"
	EventTypeLiquidationSkipped = "liquidation_skipped"
	EventTypeAuctionFinished    = "auction_finished"

	AttributeKeyID        = "id"
	AttributeKeyMode      = "mode"
	AttributeKeyDebt      = "debt"
	AttributeKeySeizedSat = "seized_sat"
	AttributeKeyPrice     = "price"
	AttributeKeyTab       = "tab"
	AttributeKeySat       = "sat"
	AttributeKeyReason    = "reason"
)
