package keeper

import (
	"context"
	"fmt"
)

// TreasuryCapInvariant is invariant 14: credited collateral ≤ TreasuryCapSat. Credits
// are refused beyond the headroom (AddCredited), so this holds unless the admin lowers
// the cap below current custody, which design §3.2 allows (deposits then pause). Tests
// do not lower the cap.
func (k *Keeper) TreasuryCapInvariant(ctx context.Context) error {
	p, err := k.Params.Get(ctx)
	if err != nil {
		return err
	}
	c, err := k.Credited.Get(ctx)
	if err != nil {
		return err
	}
	if c.GT(p.TreasuryCapSat) {
		return fmt.Errorf("invariant 14: credited %s > cap %s", c, p.TreasuryCapSat)
	}
	return nil
}
