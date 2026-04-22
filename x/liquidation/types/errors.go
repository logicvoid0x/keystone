package types

import errorsmod "cosmossdk.io/errors"

var (
	ErrInvalidParams   = errorsmod.Register(ModuleName, 2, "invalid params")
	ErrInvalidAddress  = errorsmod.Register(ModuleName, 3, "invalid address")
	ErrInvalidAmount   = errorsmod.Register(ModuleName, 4, "invalid amount")
	ErrWithdrawBlocked = errorsmod.Register(ModuleName, 5, "pool withdrawals are blocked while a vault is liquidatable or the price is stale")
	ErrNoDeposit       = errorsmod.Register(ModuleName, 6, "no stability pool deposit")
	ErrAuctionNotFound = errorsmod.Register(ModuleName, 7, "auction not found")
	ErrPriceTooHigh    = errorsmod.Register(ModuleName, 8, "auction price above max_price")
)
