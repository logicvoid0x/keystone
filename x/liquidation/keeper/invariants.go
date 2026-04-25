package keeper

import (
	"context"
	"fmt"

	sdkmath "cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/OWNER/keystone/x/liquidation/types"
)

// PoolInvariant is invariant 8: the pool module's KUSD balance ≥ Σ compounded
// deposits, and the difference is at most one ukusd per depositor plus the tracked pool
// slack (Appendix A as corrected in M1). It also checks that pending satoshis cover
// every depositor's gain.
func (k *Keeper) PoolInvariant(ctx context.Context) error {
	sumC, sumG := sdkmath.ZeroInt(), sdkmath.ZeroInt()
	var depositors int64
	err := k.Deposits.Walk(ctx, nil, func(addr sdk.AccAddress, _ types.PoolDeposit) (bool, error) {
		c, g, err := k.Position(ctx, addr)
		if err != nil {
			return true, err
		}
		sumC, sumG = sumC.Add(c), sumG.Add(g)
		depositors++
		return false, nil
	})
	if err != nil {
		return err
	}
	bal := k.ModuleBalance(ctx)
	if bal.LT(sumC) {
		return fmt.Errorf("invariant 8: pool balance %s < Σ compounded %s", bal, sumC)
	}
	slack, err := k.PoolSlack.Get(ctx)
	if err != nil {
		return err
	}
	if diff, bound := bal.Sub(sumC), slack.AddRaw(depositors); diff.GT(bound) {
		return fmt.Errorf("invariant 8: pool balance − Σ compounded = %s > depositors + slack = %s", diff, bound)
	}
	pending, err := k.PendingSat.Get(ctx)
	if err != nil {
		return err
	}
	if pending.LT(sumG) {
		return fmt.Errorf("invariant 8: pending sat %s < Σ gains %s", pending, sumG)
	}
	return nil
}
