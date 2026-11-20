package keeper

import (
	"cmp"
	"context"
	"errors"
	"slices"

	"cosmossdk.io/collections"
	errorsmod "cosmossdk.io/errors"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/logicvoid0x/keystone/x/custody/types"
)

// GetActiveEpoch returns the ACTIVE key epoch; ok is false when there is none (before
// the first DKG finishes, design §9.3).
func (k *Keeper) GetActiveEpoch(ctx context.Context) (types.KeyEpoch, bool, error) {
	n, err := k.ActiveEpoch.Get(ctx)
	if err != nil || n == 0 {
		return types.KeyEpoch{}, false, err
	}
	e, err := k.Epochs.Get(ctx, n)
	if err != nil {
		return types.KeyEpoch{}, false, err
	}
	return e, true, nil
}

// RegisterVault derives vault id's deposit script for the active epoch (design §9.6),
// stores it in the registry (pk_script → vault id, (vault id, epoch) → pk_script) and
// returns its bech32m address. With no ACTIVE epoch it registers nothing and returns
// "" (deposits are disabled until the first epoch is ACTIVE).
func (k *Keeper) RegisterVault(ctx context.Context, vaultID uint64) (string, error) {
	e, ok, err := k.GetActiveEpoch(ctx)
	if err != nil || !ok {
		return "", err
	}
	p, _, err := types.ParseGroupKey(e.GroupKey)
	if err != nil {
		return "", err
	}
	script, _, err := types.VaultScript(p, sdk.UnwrapSDKContext(ctx).ChainID(), e.Number, vaultID)
	if err != nil {
		return "", err
	}
	if has, err := k.ScriptVault.Has(ctx, script); err != nil {
		return "", err
	} else if has {
		return "", errorsmod.Wrapf(types.ErrInvalidAddress, "deposit script of vault %d already registered", vaultID)
	}
	if err := k.ScriptVault.Set(ctx, script, vaultID); err != nil {
		return "", err
	}
	if err := k.VaultScript.Set(ctx, collections.Join(vaultID, e.Number), script); err != nil {
		return "", err
	}
	return k.scriptAddress(ctx, script)
}

func (k *Keeper) scriptAddress(ctx context.Context, script []byte) (string, error) {
	p, err := k.Params.Get(ctx)
	if err != nil {
		return "", err
	}
	return types.ScriptAddress(script, p.BitcoinNetwork)
}

// DepositAddress returns vault id's deposit address and script for the active epoch.
func (k *Keeper) DepositAddress(ctx context.Context, vaultID uint64) (string, []byte, uint64, error) {
	n, err := k.ActiveEpoch.Get(ctx)
	if err != nil {
		return "", nil, 0, err
	}
	if n == 0 {
		return "", nil, 0, types.ErrNoEpoch
	}
	script, err := k.VaultScript.Get(ctx, collections.Join(vaultID, n))
	if errors.Is(err, collections.ErrNotFound) {
		return "", nil, 0, errorsmod.Wrapf(types.ErrNotFound, "no deposit address for vault %d in epoch %d", vaultID, n)
	}
	if err != nil {
		return "", nil, 0, err
	}
	addr, err := k.scriptAddress(ctx, script)
	return addr, script, n, err
}

// TreasuryAddress returns the epoch's treasury / change address (Q0).
func (k *Keeper) TreasuryAddress(ctx context.Context, e types.KeyEpoch) (string, error) {
	p, _, err := types.ParseGroupKey(e.GroupKey)
	if err != nil {
		return "", err
	}
	script, _, err := types.TreasuryScript(p)
	if err != nil {
		return "", err
	}
	return k.scriptAddress(ctx, script)
}

func sortShareIndices(s []types.ShareIndex) {
	slices.SortFunc(s, func(a, b types.ShareIndex) int {
		return cmp.Or(cmp.Compare(a.Epoch, b.Epoch), cmp.Compare(a.Index, b.Index))
	})
}
