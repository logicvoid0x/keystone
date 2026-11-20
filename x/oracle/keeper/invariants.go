package keeper

import (
	"context"
	"fmt"
)

// StepInvariant is invariant 9: for consecutive updates, |spot_new − spot_old| ≤
// ceil(spot_old × MaxStepBps / 10,000) (spot_old = 0 is the unclamped bootstrap). The
// ceil allows the §6.3 clamp's floored lower bound, which can move the spot down by at
// most 1 micro-USD more than MaxStepBps.
func (k *Keeper) StepInvariant(ctx context.Context) error {
	ps, err := k.PriceState.Get(ctx)
	if err != nil {
		return err
	}
	p, err := k.Params.Get(ctx)
	if err != nil {
		return err
	}
	old := ps.PrevSpotMicroUsd
	if old.IsNil() || !old.IsPositive() {
		return nil
	}
	diff := ps.SpotMicroUsd.Sub(old).Abs()
	if diff.MulRaw(10_000).GT(old.MulRaw(int64(p.MaxStepBps)).AddRaw(9_999)) {
		return fmt.Errorf("invariant 9: spot moved %s → %s, more than %d bps", old, ps.SpotMicroUsd, p.MaxStepBps)
	}
	return nil
}
