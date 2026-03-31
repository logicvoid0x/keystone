package types

import (
	errorsmod "cosmossdk.io/errors"

	"github.com/OWNER/keystone/x/internal/bounds"
)

// DefaultParams are the design §10.3 defaults.
func DefaultParams() Params {
	return Params{SinPauseBps: 500, SinPauseBlocks: 1000}
}

// Validate enforces sanity bounds (design §17 names none for this module; recorded in
// docs/decisions.md).
func (p Params) Validate() error {
	err := bounds.First(
		bounds.Uint("sin_pause_bps", uint64(p.SinPauseBps), 1, bounds.MaxBps),
		bounds.Uint("sin_pause_blocks", uint64(p.SinPauseBlocks), 1, 1_000_000),
	)
	if err != nil {
		return errorsmod.Wrap(ErrInvalidParams, err.Error())
	}
	return nil
}
