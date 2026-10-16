package keeper

import (
	"context"
	"errors"
	"fmt"

	"cosmossdk.io/collections"

	sdkmath "cosmossdk.io/math"
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

// CoverageInvariant is invariant 6 (custody coverage) with the M3 terms: the satoshis
// in Keystone UTXOs plus the recorded deficit cover credited collateral (= Σ vault
// collateral, checked by x/vault), claimable balances, the fee reserve and held, the
// satoshis owed by other modules (auction collateral and pending pool gains), plus
// (M4a) queued withdrawal requests and what in-flight batches will pay out (recipient
// outputs and fee: inputs minus change of the current version).
func (k *Keeper) CoverageInvariant(ctx context.Context, held sdkmath.Int) error {
	utxos, err := k.UtxoSum(ctx)
	if err != nil {
		return err
	}
	deficit, err := k.Deficit.Get(ctx)
	if err != nil {
		return err
	}
	credited, err := k.Credited.Get(ctx)
	if err != nil {
		return err
	}
	claim, err := k.TotalClaimable(ctx)
	if err != nil {
		return err
	}
	fee, err := k.FeeReserve.Get(ctx)
	if err != nil {
		return err
	}
	queued, err := k.QueuedSat(ctx)
	if err != nil {
		return err
	}
	inflight, err := k.InFlightSat(ctx)
	if err != nil {
		return err
	}
	have, owed := utxos.Add(deficit), credited.Add(claim).Add(fee).Add(held).Add(queued).Add(inflight)
	if have.LT(owed) {
		return fmt.Errorf("invariant 6: UTXOs %s + deficit %s < collateral %s + claimable %s + fee reserve %s + held %s + queued %s + in flight %s",
			utxos, deficit, credited, claim, fee, held, queued, inflight)
	}
	return nil
}

// TheftPauseInvariant is invariant 15 (M7, §9.12): if the ACTIVE epoch has been
// slashed for theft, deposits are paused.
func (k *Keeper) TheftPauseInvariant(ctx context.Context) error {
	active, err := k.ActiveEpoch.Get(ctx)
	if errors.Is(err, collections.ErrNotFound) || active == 0 {
		return nil
	} else if err != nil {
		return err
	}
	slashed, err := k.SlashedEpochs.Has(ctx, active)
	if err != nil || !slashed {
		return err
	}
	paused, err := k.IsTheftPaused(ctx)
	if err != nil {
		return err
	}
	if !paused {
		return fmt.Errorf("invariant 15: active epoch %d was slashed for theft but deposits are not paused", active)
	}
	return nil
}
