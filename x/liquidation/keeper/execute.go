package keeper

import (
	"context"
	"fmt"

	sdkmath "cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/logicvoid0x/keystone/x/liquidation/types"
	vaulttypes "github.com/logicvoid0x/keystone/x/vault/types"
)

// Execute runs the batch in order at price, then advances the running auctions.
// Totality (design §5.9): each entry runs in a cached context; an entry that no
// longer applies (vault gone, not liquidatable, any error) is discarded and emits
// liquidation_skipped. Execute itself only fails on a store error outside an entry.
func (k *Keeper) Execute(ctx context.Context, batch types.LiquidationBatch, price sdkmath.Int) error {
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	for _, e := range batch.Entries {
		cc, write := sdkCtx.CacheContext()
		if err := k.executeEntry(cc, e, price); err != nil {
			emit(ctx, types.EventTypeLiquidationSkipped,
				sdk.NewAttribute(types.AttributeKeyID, fmt.Sprint(e.VaultId)),
				sdk.NewAttribute(types.AttributeKeyReason, err.Error()))
			continue
		}
		write()
	}
	return k.tickAuctions(ctx)
}

func (k *Keeper) executeEntry(ctx context.Context, e types.Entry, price sdkmath.Int) error {
	v, err := k.vault.GetVault(ctx, e.VaultId)
	if err != nil {
		return err
	}
	ok, err := k.vault.IsLiquidatable(ctx, v, price)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("vault %d is no longer liquidatable", v.Id)
	}
	switch e.Mode {
	case types.MODE_POOL:
		return k.liquidateToPool(ctx, v, price)
	case types.MODE_AUCTION:
		return k.liquidateToAuction(ctx, v, price)
	default:
		return fmt.Errorf("unknown mode %v", e.Mode)
	}
}

// liquidateToPool is design §8.4 pool mode, steps 1–4.
func (k *Keeper) liquidateToPool(ctx context.Context, v vaulttypes.Vault, price sdkmath.Int) error {
	p, err := k.Params.Get(ctx)
	if err != nil {
		return err
	}
	debt, err := k.vault.Debt(ctx, v)
	if err != nil {
		return err
	}
	// 1. Seize S = min(collateral, ceil(D × (10,000 + penalty) × 1e8 ÷ (10,000 × P))).
	seize := vaulttypes.SeizeSat(debt, price, v.CollateralSat, p.PenaltyBps)
	// 3. If S × P < D, move up to D − S × P of KUSD from surplus to the pool. The
	// seized value is rounded up, so surplus pays no more than the shortfall.
	value := seize.Mul(price).Add(vaulttypes.SatPerBTC.SubRaw(1)).Quo(vaulttypes.SatPerBTC)
	comp := sdkmath.ZeroInt()
	if value.LT(debt) {
		if comp, err = k.stablecoin.CoverShortfall(ctx, types.ModuleName, debt.Sub(value)); err != nil {
			return err
		}
	}
	// 4. Vault: art = 0; remaining collateral stays with the owner.
	closed, err := k.vault.CloseForLiquidation(ctx, v.Id, seize)
	if err != nil {
		return err
	}
	if !closed.Equal(debt) {
		return fmt.Errorf("vault %d debt changed: %s != %s", v.Id, closed, debt)
	}
	// 2. Depositors absorb D − comp and gain S; burn D from the pool.
	if err := k.offset(ctx, debt.Sub(comp), seize); err != nil {
		return err
	}
	if err := k.stablecoin.BurnFromModule(ctx, types.ModuleName, debt); err != nil {
		return err
	}
	emit(ctx, types.EventTypeVaultLiquidated,
		sdk.NewAttribute(types.AttributeKeyID, fmt.Sprint(v.Id)),
		sdk.NewAttribute(types.AttributeKeyMode, "POOL"),
		sdk.NewAttribute(types.AttributeKeyDebt, debt.String()),
		sdk.NewAttribute(types.AttributeKeySeizedSat, seize.String()),
		sdk.NewAttribute(types.AttributeKeyPrice, price.String()))
	return nil
}

// liquidateToAuction is design §8.4 auction mode, step 1: D moves to sin and all
// collateral goes to an auction with tab = D × (1 + penalty).
func (k *Keeper) liquidateToAuction(ctx context.Context, v vaulttypes.Vault, price sdkmath.Int) error {
	p, err := k.Params.Get(ctx)
	if err != nil {
		return err
	}
	coll := v.CollateralSat
	debt, err := k.vault.CloseForLiquidation(ctx, v.Id, coll)
	if err != nil {
		return err
	}
	if err := k.stablecoin.RecordBadDebt(ctx, debt, "auction"); err != nil {
		return err
	}
	emit(ctx, types.EventTypeVaultLiquidated,
		sdk.NewAttribute(types.AttributeKeyID, fmt.Sprint(v.Id)),
		sdk.NewAttribute(types.AttributeKeyMode, "AUCTION"),
		sdk.NewAttribute(types.AttributeKeyDebt, debt.String()),
		sdk.NewAttribute(types.AttributeKeySeizedSat, coll.String()),
		sdk.NewAttribute(types.AttributeKeyPrice, price.String()))
	if !coll.IsPositive() {
		return nil // nothing to sell: the debt stays in sin for surplus to heal
	}
	id, err := k.NextAuctionID.Next(ctx)
	if err != nil {
		return err
	}
	id++ // auction ids start at 1
	tab := ceilDiv(debt.MulRaw(int64(10_000+p.PenaltyBps)), sdkmath.NewInt(10_000))
	a := types.Auction{
		Id: id, VaultId: v.Id, Tab: tab, Raised: sdkmath.ZeroInt(), CollateralSat: coll,
		StartPrice:  price.MulRaw(int64(p.AuctionStartBps)).QuoRaw(10_000),
		StartHeight: sdk.UnwrapSDKContext(ctx).BlockHeight(),
		SinOwed:     debt,
	}
	if err := k.Auctions.Set(ctx, id, a); err != nil {
		return err
	}
	emit(ctx, types.EventTypeAuctionStarted,
		sdk.NewAttribute(types.AttributeKeyID, fmt.Sprint(id)),
		sdk.NewAttribute(types.AttributeKeyTab, tab.String()))
	return nil
}

func ceilDiv(a, b sdkmath.Int) sdkmath.Int {
	q := a.Quo(b)
	if !a.Mod(b).IsZero() {
		q = q.AddRaw(1)
	}
	return q
}
