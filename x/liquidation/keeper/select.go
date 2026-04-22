package keeper

import (
	"context"

	sdkmath "cosmossdk.io/math"

	"github.com/OWNER/keystone/x/liquidation/types"
	vaulttypes "github.com/OWNER/keystone/x/vault/types"
)

// Select chooses this block's liquidations at price, exactly design §8.2. It reads
// state only. STALE price → empty batch. Up to MaxPerBlock vaults, worst first; a
// vault goes to the pool if the pool's remaining balance covers its whole debt,
// otherwise to an auction.
func (k *Keeper) Select(ctx context.Context, price sdkmath.Int) (types.LiquidationBatch, error) {
	batch := types.LiquidationBatch{PriceMicroUsd: price}
	active, err := k.oracle.IsActiveForLiquidation(ctx)
	if err != nil || !active {
		return batch, err
	}
	p, err := k.Params.Get(ctx)
	if err != nil {
		return batch, err
	}
	poolLeft, err := k.PoolBalance(ctx)
	if err != nil {
		return batch, err
	}
	var iterErr error
	err = k.vault.IterateLiquidatable(ctx, price, func(v vaulttypes.Vault) (stop bool) {
		if len(batch.Entries) == int(p.MaxPerBlock) {
			return true
		}
		debt, err := k.vault.Debt(ctx, v)
		if err != nil {
			iterErr = err
			return true
		}
		mode := types.MODE_POOL
		if poolLeft.LT(debt) {
			mode = types.MODE_AUCTION
		} else {
			poolLeft = poolLeft.Sub(debt)
		}
		batch.Entries = append(batch.Entries, types.Entry{VaultId: v.Id, Mode: mode})
		return false
	})
	if err != nil {
		return batch, err
	}
	return batch, iterErr
}
