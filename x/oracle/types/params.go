package types

import (
	errorsmod "cosmossdk.io/errors"

	"github.com/logicvoid0x/keystone/x/internal/bounds"
)

// DefaultParams are the design §10.3 defaults.
func DefaultParams() Params {
	return Params{
		MinSources:  3,
		MaxStepBps:  500,
		StaleBlocks: 20,
		TwapBlocks:  100,
		VolatileBps: 1500,
		MissWindow:  1000,
		MissAlert:   200,
		OutlierBps:  200,
	}
}

// Validate enforces the bounds. MaxStepBps 100..2000 is from design §17; the others
// are sanity bounds.
func (p Params) Validate() error {
	err := bounds.First(
		bounds.Uint("min_sources", uint64(p.MinSources), 1, 32),
		bounds.Uint("max_step_bps", uint64(p.MaxStepBps), 100, 2000),
		bounds.Uint("stale_blocks", uint64(p.StaleBlocks), 1, 10_000),
		bounds.Uint("twap_blocks", uint64(p.TwapBlocks), 1, 10_000),
		bounds.Uint("volatile_bps", uint64(p.VolatileBps), 1, bounds.MaxBps),
		bounds.Uint("miss_window", uint64(p.MissWindow), 1, 100_000),
		bounds.Uint("miss_alert", uint64(p.MissAlert), 1, uint64(p.MissWindow)),
		bounds.Uint("outlier_bps", uint64(p.OutlierBps), 1, bounds.MaxBps),
	)
	if err != nil {
		return errorsmod.Wrap(ErrInvalidParams, err.Error())
	}
	return nil
}
