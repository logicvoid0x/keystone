package keeper

import (
	"bytes"
	"context"
	"fmt"

	"cosmossdk.io/collections"
	sdkmath "cosmossdk.io/math"

	"github.com/logicvoid0x/keystone/x/vault/types"
)

// Invariants from design Appendix A that x/vault can check. Each returns nil or a
// description of the violation.

type vaultTotals struct {
	art, debt, coll sdkmath.Int
	withArt         int64
}

func (k *Keeper) totals(ctx context.Context) (vaultTotals, error) {
	t := vaultTotals{art: sdkmath.ZeroInt(), debt: sdkmath.ZeroInt(), coll: sdkmath.ZeroInt()}
	rate, err := k.Rate(ctx)
	if err != nil {
		return t, err
	}
	err = k.Vaults.Walk(ctx, nil, func(_ uint64, v types.Vault) (bool, error) {
		t.art = t.art.Add(v.Art)
		t.coll = t.coll.Add(v.CollateralSat)
		if v.Art.IsPositive() {
			t.withArt++
			t.debt = t.debt.Add(types.DebtOf(v.Art, rate))
		}
		return false, nil
	})
	return t, err
}

// SupplyInvariant is invariant 1: 0 ≤ Σ debt_v + sin − supply ≤ |V| + S, with S the
// tracked rounding slack (Appendix A as corrected in M1).
func (k *Keeper) SupplyInvariant(ctx context.Context) error {
	t, err := k.totals(ctx)
	if err != nil {
		return err
	}
	sin, err := k.stablecoin.GetSin(ctx)
	if err != nil {
		return err
	}
	slack, err := k.RoundingSlack.Get(ctx)
	if err != nil {
		return err
	}
	gap := t.debt.Add(sin).Sub(k.stablecoin.Supply(ctx))
	if gap.IsNegative() {
		return fmt.Errorf("invariant 1: Σdebt + sin − supply = %s < 0", gap)
	}
	if bound := slack.AddRaw(t.withArt); gap.GT(bound) {
		return fmt.Errorf("invariant 1: Σdebt + sin − supply = %s > |V| + S = %s", gap, bound)
	}
	return nil
}

// TotalArtInvariant is invariant 2: totalArt = Σ art_v.
func (k *Keeper) TotalArtInvariant(ctx context.Context) error {
	t, err := k.totals(ctx)
	if err != nil {
		return err
	}
	stored, err := k.TotalArt.Get(ctx)
	if err != nil {
		return err
	}
	if !stored.Equal(t.art) {
		return fmt.Errorf("invariant 2: totalArt %s != Σ art %s", stored, t.art)
	}
	return nil
}

// RateInvariant is the stateless half of invariant 3: rate ≥ RAY. Monotonicity is
// checked against the previous block by the app's invariant runner.
func (k *Keeper) RateInvariant(ctx context.Context) error {
	r, err := k.Rate(ctx)
	if err != nil {
		return err
	}
	if r.LT(types.RAY) {
		return fmt.Errorf("invariant 3: rate %s < RAY", r)
	}
	return nil
}

// DustInvariant is invariant 4: every debt is 0 or ≥ Dust. (Raising Dust by
// MsgUpdateParams can leave older vaults below it; tests do not change Dust.)
func (k *Keeper) DustInvariant(ctx context.Context) error {
	p, err := k.Params.Get(ctx)
	if err != nil {
		return err
	}
	rate, err := k.Rate(ctx)
	if err != nil {
		return err
	}
	var bad error
	err = k.Vaults.Walk(ctx, nil, func(id uint64, v types.Vault) (bool, error) {
		d := types.DebtOf(v.Art, rate)
		if d.IsPositive() && d.LT(p.Dust) {
			bad = fmt.Errorf("invariant 4: vault %d debt %s < dust %s", id, d, p.Dust)
			return true, nil
		}
		return false, nil
	})
	if err != nil {
		return err
	}
	return bad
}

// CeilingInvariant is invariant 5 in aggregate form: ceil(totalArt × rate_mark / RAY)
// ≤ ceiling_mark, where the mark is the rate and ceiling at the last mint.
func (k *Keeper) CeilingInvariant(ctx context.Context) error {
	m, err := k.CeilingMark.Get(ctx)
	if err != nil {
		return err
	}
	a, err := k.TotalArt.Get(ctx)
	if err != nil {
		return err
	}
	if d := types.DebtOf(a, m.Rate); d.GT(m.Ceiling) {
		return fmt.Errorf("invariant 5: debt at last-mint rate %s > ceiling %s", d, m.Ceiling)
	}
	return nil
}

// LiqIndexInvariant is invariant 7: a vault is in the index iff art > 0, with the key
// exactly as in §8.1.
func (k *Keeper) LiqIndexInvariant(ctx context.Context) error {
	want := map[uint64][]byte{}
	err := k.Vaults.Walk(ctx, nil, func(id uint64, v types.Vault) (bool, error) {
		if v.Art.IsPositive() {
			want[id] = types.LiqKey(v.Art, v.CollateralSat)
		}
		return false, nil
	})
	if err != nil {
		return err
	}
	seen := 0
	var bad error
	err = k.LiqIndex.Walk(ctx, nil, func(key collections.Pair[[]byte, uint64], _ bool) (bool, error) {
		w, ok := want[key.K2()]
		if !ok || !bytes.Equal(w, key.K1()) {
			bad = fmt.Errorf("invariant 7: index entry for vault %d is stale or extra", key.K2())
			return true, nil
		}
		seen++
		return false, nil
	})
	if err != nil {
		return err
	}
	if bad != nil {
		return bad
	}
	if seen != len(want) {
		return fmt.Errorf("invariant 7: %d vaults with art, %d index entries", len(want), seen)
	}
	return nil
}

// CreditedInvariant ties the custody credited counter to vault collateral (the M1
// definition of credited) — the accounting behind invariant 14.
func (k *Keeper) CreditedInvariant(ctx context.Context) error {
	t, err := k.totals(ctx)
	if err != nil {
		return err
	}
	c, err := k.custody.GetCredited(ctx)
	if err != nil {
		return err
	}
	if !c.Equal(t.coll) {
		return fmt.Errorf("invariant 14: credited %s != Σ collateral %s", c, t.coll)
	}
	return nil
}

// AllInvariants runs every x/vault invariant.
func (k *Keeper) AllInvariants(ctx context.Context) error {
	for _, f := range []func(context.Context) error{
		k.SupplyInvariant, k.TotalArtInvariant, k.RateInvariant, k.DustInvariant,
		k.CeilingInvariant, k.LiqIndexInvariant, k.CreditedInvariant,
	} {
		if err := f(ctx); err != nil {
			return err
		}
	}
	return nil
}
