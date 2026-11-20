package types

import (
	errorsmod "cosmossdk.io/errors"

	"github.com/logicvoid0x/keystone/x/internal/bounds"
)

// DefaultParams are the design §10.3 defaults.
func DefaultParams() Params {
	return Params{
		PenaltyBps:       1000,
		MaxPerBlock:      50,
		AuctionStartBps:  12000,
		AuctionStepBps:   100,
		AuctionResetBps:  6000,
		AuctionMaxBlocks: 300,
	}
}

// Validate enforces sanity bounds (design §17 names none for this module).
func (p Params) Validate() error {
	err := bounds.First(
		bounds.Uint("penalty_bps", uint64(p.PenaltyBps), 0, 5000),
		bounds.Uint("max_per_block", uint64(p.MaxPerBlock), 1, 1000),
		bounds.Uint("auction_start_bps", uint64(p.AuctionStartBps), bounds.MaxBps, 30_000),
		bounds.Uint("auction_step_bps", uint64(p.AuctionStepBps), 1, bounds.MaxBps),
		bounds.Uint("auction_reset_bps", uint64(p.AuctionResetBps), 1, bounds.MaxBps-1),
		bounds.Uint("auction_max_blocks", uint64(p.AuctionMaxBlocks), 1, 100_000),
	)
	if err != nil {
		return errorsmod.Wrap(ErrInvalidParams, err.Error())
	}
	return nil
}
