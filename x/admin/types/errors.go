package types

import errorsmod "cosmossdk.io/errors"

var (
	ErrUnauthorized   = errorsmod.Register(ModuleName, 2, "signer is not the admin")
	ErrUnsorted       = errorsmod.Register(ModuleName, 3, "entries must be sorted by operator address without duplicates")
	ErrGuardRefused   = errorsmod.Register(ModuleName, 4, "allowlist removal refused by the custody removal guard")
	ErrNotAllowlisted = errorsmod.Register(ModuleName, 5, "operator is not allowlisted")
	ErrInvalidAddress = errorsmod.Register(ModuleName, 6, "invalid address")
	ErrEmptyChange    = errorsmod.Register(ModuleName, 7, "nothing to add or remove")
	ErrAlreadyListed  = errorsmod.Register(ModuleName, 8, "operator is already allowlisted")
	ErrOverlap        = errorsmod.Register(ModuleName, 9, "an operator is both added and removed")
	ErrExecMsg        = errorsmod.Register(ModuleName, 10, "message not allowed in MsgExec")
	ErrEmptyAllowlist = errorsmod.Register(ModuleName, 11, "allowlist would be empty")
)
