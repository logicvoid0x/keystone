package keeper

import (
	"context"

	"cosmossdk.io/collections"
	errorsmod "cosmossdk.io/errors"

	sdk "github.com/cosmos/cosmos-sdk/types"

	admintypes "github.com/OWNER/keystone/x/admin/types"
	"github.com/OWNER/keystone/x/custody/types"
)

// RemovalGuard is the x/admin removal guard (design §9.4): it rejects an allowlist
// removal (which jails the removed validators) that would leave fewer than t share
// holders of a funded epoch (ACTIVE or RETIRING with any UTXO) active, since only
// active validators can sign the epoch's sweep.
type RemovalGuard struct{ k *Keeper }

// RemovalGuard returns the guard x/admin calls on every allowlist removal.
func (k *Keeper) RemovalGuard() RemovalGuard { return RemovalGuard{k} }

var _ admintypes.RemovalGuard = RemovalGuard{}

// AllowRemoval implements admintypes.RemovalGuard.
func (g RemovalGuard) AllowRemoval(ctx context.Context, removed []sdk.AccAddress) error {
	vals, err := g.k.vals.ActiveValidators(ctx)
	if err != nil {
		return err
	}
	active := map[string]bool{}
	for _, v := range vals {
		if v.Power > 0 {
			active[v.OperatorAddress] = true
		}
	}
	for _, r := range removed {
		delete(active, r.String())
	}
	var epochs []types.KeyEpoch
	if err := g.k.Epochs.Walk(ctx, nil, func(_ uint64, e types.KeyEpoch) (bool, error) {
		if e.Status == types.EPOCH_STATUS_ACTIVE || e.Status == types.EPOCH_STATUS_RETIRING {
			epochs = append(epochs, e)
		}
		return false, nil
	}); err != nil {
		return err
	}
	for _, e := range epochs {
		funded, err := g.k.epochFunded(ctx, e.Number)
		if err != nil {
			return err
		}
		if !funded {
			continue
		}
		var left uint32
		rng := collections.NewPrefixedPairRange[uint64, sdk.AccAddress](e.Number)
		if err := g.k.ShareIndex.Walk(ctx, rng, func(key collections.Pair[uint64, sdk.AccAddress], _ uint32) (bool, error) {
			if active[key.K2().String()] {
				left++
			}
			return false, nil
		}); err != nil {
			return err
		}
		if left < e.T {
			return errorsmod.Wrapf(types.ErrRemovalGuard,
				"epoch %d holds funds and would keep %d active share holders, fewer than t = %d; remove validators in stages", e.Number, left, e.T)
		}
	}
	return nil
}

// epochFunded reports whether any UTXO of epoch is held.
func (k *Keeper) epochFunded(ctx context.Context, epoch uint64) (bool, error) {
	funded := false
	err := k.Utxos.Walk(ctx, nil, func(_ collections.Pair[[]byte, uint32], u types.Utxo) (bool, error) {
		if u.Epoch == epoch {
			funded = true
			return true, nil
		}
		return false, nil
	})
	return funded, err
}
