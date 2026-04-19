package types

import errorsmod "cosmossdk.io/errors"

var (
	ErrInvalidParams  = errorsmod.Register(ModuleName, 2, "invalid params")
	ErrInvalidAddress = errorsmod.Register(ModuleName, 3, "invalid address")
	ErrInvalidAmount  = errorsmod.Register(ModuleName, 4, "invalid amount")
	ErrMintsPaused    = errorsmod.Register(ModuleName, 5, "mints are paused: bad debt above threshold")
)
