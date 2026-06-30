package types

// Events (design §11.3) and the M3 additions (docs/decisions.md).
const (
	EventTypeDepositAccepted = "deposit_accepted"
	EventTypeDepositOverCap  = "deposit_over_cap"
	EventTypeDepositReverted = "deposit_reverted"
	EventTypeAnchorAccepted  = "btc_anchor_accepted"
	EventTypeOpSkipped       = "custody_op_skipped"

	AttributeKeyOutpoint   = "outpoint"
	AttributeKeyVault      = "vault"
	AttributeKeySat        = "sat"
	AttributeKeyCollateral = "collateral_sat"
	AttributeKeyClaimable  = "claimable_sat"
	AttributeKeyFeeReserve = "fee_reserve_sat"
	AttributeKeyDeficit    = "deficit"
	AttributeKeyHeight     = "height"
	AttributeKeyHash       = "hash"
	AttributeKeyOp         = "op"
	AttributeKeyReason     = "reason"

	OpAcceptAnchor  = "accept_anchor"
	OpCreditDeposit = "credit_deposit"
	OpRevertDeposit = "revert_deposit"
)
