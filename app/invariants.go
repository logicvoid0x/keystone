package app

import (
	"fmt"

	sdkmath "cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"
)

// invariantState is what the invariant runner remembers between blocks to check the
// monotone parts of invariant 3. It lives only in memory and only matters in builds
// that run invariants; it never reaches consensus state.
type invariantState struct {
	rate     sdkmath.Int
	lastDrip int64
}

// CheckInvariants runs every implemented invariant (design Appendix A: 1, 2, 3, 4, 5,
// 6, 7, 8, 9, 14) and returns the first violation.
func (app *App) CheckInvariants(ctx sdk.Context) error {
	if err := app.VaultKeeper.AllInvariants(ctx); err != nil {
		return err
	}
	if err := app.LiquidationKeeper.PoolInvariant(ctx); err != nil {
		return err
	}
	if err := app.OracleKeeper.StepInvariant(ctx); err != nil {
		return err
	}
	if err := app.CustodyKeeper.TreasuryCapInvariant(ctx); err != nil {
		return err
	}
	held, err := app.LiquidationKeeper.HeldSat(ctx)
	if err != nil {
		return err
	}
	if err := app.CustodyKeeper.CoverageInvariant(ctx, held); err != nil {
		return err
	}
	rs, err := app.VaultKeeper.RateState.Get(ctx)
	if err != nil {
		return err
	}
	if prev := app.invState; !prev.rate.IsNil() {
		if rs.Rate.LT(prev.rate) {
			return fmt.Errorf("invariant 3: rate decreased %s → %s", prev.rate, rs.Rate)
		}
		if rs.LastDripUnix < prev.lastDrip {
			return fmt.Errorf("invariant 3: last_drip decreased %d → %d", prev.lastDrip, rs.LastDripUnix)
		}
	}
	app.invState = invariantState{rate: rs.Rate, lastDrip: rs.LastDripUnix}
	return nil
}
