package types

import errorsmod "cosmossdk.io/errors"

var (
	ErrUnauthorized      = errorsmod.Register(ModuleName, 2, "signer is not the admin")
	ErrInvalidPower      = errorsmod.Register(ModuleName, 3, "power must be 0 or 1")
	ErrInvalidPubKey     = errorsmod.Register(ModuleName, 4, "invalid consensus pubkey")
	ErrPubKeyUsed        = errorsmod.Register(ModuleName, 5, "consensus pubkey already used")
	ErrPubKeyChange      = errorsmod.Register(ModuleName, 6, "consensus pubkey cannot change")
	ErrUnsorted          = errorsmod.Register(ModuleName, 7, "entries must be sorted by operator address without duplicates")
	ErrGuardRefused      = errorsmod.Register(ModuleName, 8, "validator set change refused by removal guard")
	ErrEmptyValidatorSet = errorsmod.Register(ModuleName, 9, "validator set would have no active operator")
	ErrUnknownOperator   = errorsmod.Register(ModuleName, 10, "unknown operator")
	ErrInvalidAddress    = errorsmod.Register(ModuleName, 11, "invalid address")
	ErrEmptyEntries      = errorsmod.Register(ModuleName, 12, "no entries")
)
