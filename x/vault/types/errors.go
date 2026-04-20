package types

import errorsmod "cosmossdk.io/errors"

var (
	ErrInvalidParams  = errorsmod.Register(ModuleName, 2, "invalid params")
	ErrInvalidAddress = errorsmod.Register(ModuleName, 3, "invalid address")
	ErrInvalidAmount  = errorsmod.Register(ModuleName, 4, "invalid amount")
	ErrVaultNotFound  = errorsmod.Register(ModuleName, 5, "vault not found")
	ErrNotOwner       = errorsmod.Register(ModuleName, 6, "signer is not the vault owner")
	ErrVaultStatus    = errorsmod.Register(ModuleName, 7, "vault status does not allow this")
	ErrPriceStatus    = errorsmod.Register(ModuleName, 8, "price status does not allow this")
	ErrBelowMCR       = errorsmod.Register(ModuleName, 9, "collateral ratio would be below MCR")
	ErrDust           = errorsmod.Register(ModuleName, 10, "debt would be below dust")
	ErrCeiling        = errorsmod.Register(ModuleName, 11, "global debt ceiling exceeded")
	ErrHasDebt        = errorsmod.Register(ModuleName, 12, "vault has debt")
	ErrInsufficient   = errorsmod.Register(ModuleName, 13, "insufficient collateral")
	ErrMintsPaused    = errorsmod.Register(ModuleName, 14, "mints are paused")
	ErrUnauthorized   = errorsmod.Register(ModuleName, 15, "unauthorized")
)
