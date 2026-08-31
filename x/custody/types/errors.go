package types

import errorsmod "cosmossdk.io/errors"

var (
	ErrInvalidParams  = errorsmod.Register(ModuleName, 2, "invalid params")
	ErrInvalidAddress = errorsmod.Register(ModuleName, 3, "invalid address")
	ErrInvalidAmount  = errorsmod.Register(ModuleName, 4, "invalid amount")
	ErrInsufficient   = errorsmod.Register(ModuleName, 5, "insufficient claimable balance")
	ErrOverCap        = errorsmod.Register(ModuleName, 6, "credit exceeds treasury cap headroom")
	ErrUnderflow      = errorsmod.Register(ModuleName, 7, "credited satoshis would go negative")
	ErrInvalidGenesis = errorsmod.Register(ModuleName, 8, "invalid genesis")
	ErrNoEpoch        = errorsmod.Register(ModuleName, 9, "no active custody epoch")
	ErrNotFound       = errorsmod.Register(ModuleName, 10, "not found")
	ErrDeficit        = errorsmod.Register(ModuleName, 11, "custody deficit outstanding")
	ErrInvalidScript  = errorsmod.Register(ModuleName, 12, "invalid recipient script")
	ErrDKG            = errorsmod.Register(ModuleName, 13, "invalid DKG message")
)
