package types

import errorsmod "cosmossdk.io/errors"

var (
	ErrInvalidParams  = errorsmod.Register(ModuleName, 2, "invalid params")
	ErrInvalidAddress = errorsmod.Register(ModuleName, 3, "invalid address")
	ErrInvalidAmount  = errorsmod.Register(ModuleName, 4, "invalid amount")
	ErrInsufficient   = errorsmod.Register(ModuleName, 5, "insufficient claimable balance")
	ErrOverCap        = errorsmod.Register(ModuleName, 6, "credit exceeds treasury cap headroom")
	ErrUnderflow      = errorsmod.Register(ModuleName, 7, "credited satoshis would go negative")
)
