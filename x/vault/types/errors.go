package types

import errorsmod "cosmossdk.io/errors"

var (
	ErrInvalidParams  = errorsmod.Register(ModuleName, 2, "invalid params")
	ErrInvalidAddress = errorsmod.Register(ModuleName, 3, "invalid address")
)
