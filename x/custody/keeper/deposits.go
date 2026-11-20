package keeper

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strconv"

	"cosmossdk.io/collections"
	sdkmath "cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/logicvoid0x/keystone/app/protocol/vetypes"
	"github.com/logicvoid0x/keystone/x/custody/types"
)

// scriptEpoch finds which epoch registered script for vaultID.
func (k *Keeper) scriptEpoch(ctx context.Context, vaultID uint64, script []byte) (uint64, bool, error) {
	var epoch uint64
	found := false
	rng := collections.NewPrefixedPairRange[uint64, uint64](vaultID)
	err := k.VaultScript.Walk(ctx, rng, func(key collections.Pair[uint64, uint64], s []byte) (bool, error) {
		if bytes.Equal(s, script) {
			epoch, found = key.K2(), true
			return true, nil
		}
		return false, nil
	})
	return epoch, found, err
}

// creditDeposit is design §9.7 step 5 for an accepted DepositSeen: below MinDepositSat
// the satoshis go to the fee reserve; a deposit within the treasury-cap headroom is
// credited to the vault (vault.CreditCollateral); one that would exceed it, or one to a
// closed vault, goes entirely to the owner's claimable balance (§3.2). The output becomes an AVAILABLE UTXO with its tweak t_v,
// and a DepositRecord keeps the split for the reorg path.
func (k *Keeper) creditDeposit(ctx context.Context, d vetypes.DepositSeen) error {
	op := collections.Join(d.Txid, d.Vout)
	if has, err := k.Utxos.Has(ctx, op); err != nil || has {
		return orSkip(err, "deposit already accepted")
	}
	vaultID, err := k.ScriptVault.Get(ctx, d.PkScript)
	if errors.Is(err, collections.ErrNotFound) {
		return errSkip("unknown deposit script")
	} else if err != nil {
		return err
	}
	epochNum, ok, err := k.scriptEpoch(ctx, vaultID, d.PkScript)
	if err != nil || !ok {
		return orSkip(err, "script not registered for an epoch")
	}
	epoch, err := k.Epochs.Get(ctx, epochNum)
	if err != nil {
		return err
	}
	if epoch.Status != types.EPOCH_STATUS_ACTIVE && epoch.Status != types.EPOCH_STATUS_RETIRING {
		return errSkip("deposit to an epoch that is not ACTIVE or RETIRING")
	}
	p, _, err := types.ParseGroupKey(epoch.GroupKey)
	if err != nil {
		return err
	}
	_, tweak, err := types.VaultScript(p, sdk.UnwrapSDKContext(ctx).ChainID(), epochNum, vaultID)
	if err != nil {
		return err
	}
	owner, open, err := k.vault.VaultOwner(ctx, vaultID)
	if err != nil {
		return err
	}
	minDeposit, err := k.vault.MinDepositSat(ctx)
	if err != nil {
		return err
	}

	amount := sdkmath.NewIntFromUint64(d.AmountSat)
	rec := types.DepositRecord{VaultId: vaultID, Owner: owner.String(), Block: *d.Block}
	overCap := false
	switch {
	case amount.LT(minDeposit):
		if err := k.addFeeReserve(ctx, amount); err != nil {
			return err
		}
		rec.FeeReserveSat = d.AmountSat
	case !open:
		if err := k.CreditClaimable(ctx, owner, amount); err != nil {
			return err
		}
		rec.ClaimableSat = d.AmountSat
	default:
		headroom, err := k.CapHeadroom(ctx)
		if err != nil {
			return err
		}
		paused, err := k.IsTheftPaused(ctx)
		if err != nil {
			return err
		}
		if paused || amount.GT(headroom) {
			// §3.2: a deposit that would push credited satoshis over the cap, or any
			// deposit while deposits are paused after a theft (§9.12), is not
			// credited at all; all of it is claimable (withdraw-only).
			if err := k.CreditClaimable(ctx, owner, amount); err != nil {
				return err
			}
			rec.ClaimableSat = d.AmountSat
			overCap = true
			break
		}
		credited, err := k.vault.CreditCollateral(ctx, vaultID, amount)
		if err != nil {
			return err
		}
		if !credited.Equal(amount) {
			return fmt.Errorf("vault %d credited %s of %s sat within headroom", vaultID, credited, amount)
		}
		rec.CollateralSat = d.AmountSat
	}

	u := types.Utxo{Txid: d.Txid, Vout: d.Vout, AmountSat: d.AmountSat, Epoch: epochNum, Tweak: tweak, Status: types.UTXO_STATUS_AVAILABLE}
	if err := k.setUtxo(ctx, nil, u); err != nil {
		return err
	}
	if err := k.Deposits.Set(ctx, op, rec); err != nil {
		return err
	}
	outpoint := types.OutpointString(d.Txid, d.Vout)
	em := sdk.UnwrapSDKContext(ctx).EventManager()
	em.EmitEvent(sdk.NewEvent(types.EventTypeDepositAccepted,
		sdk.NewAttribute(types.AttributeKeyOutpoint, outpoint),
		sdk.NewAttribute(types.AttributeKeyVault, strconv.FormatUint(vaultID, 10)),
		sdk.NewAttribute(types.AttributeKeySat, strconv.FormatUint(d.AmountSat, 10)),
		sdk.NewAttribute(types.AttributeKeyCollateral, strconv.FormatUint(rec.CollateralSat, 10)),
		sdk.NewAttribute(types.AttributeKeyClaimable, strconv.FormatUint(rec.ClaimableSat, 10)),
		sdk.NewAttribute(types.AttributeKeyFeeReserve, strconv.FormatUint(rec.FeeReserveSat, 10))))
	if overCap {
		em.EmitEvent(sdk.NewEvent(types.EventTypeDepositOverCap,
			sdk.NewAttribute(types.AttributeKeyOutpoint, outpoint),
			sdk.NewAttribute(types.AttributeKeyVault, strconv.FormatUint(vaultID, 10)),
			sdk.NewAttribute(types.AttributeKeySat, strconv.FormatUint(rec.ClaimableSat, 10))))
	}
	return nil
}

// revertDeposit is design §9.7 step 6 for an accepted ReorgSeen: the UTXO is removed
// and what the deposit credited is taken back from the vault's collateral, the owner's
// claimable balance and the fee reserve. Whatever is no longer there (spent, minted
// against and liquidated, withdrawn) is recorded in the custody deficit.
func (k *Keeper) revertDeposit(ctx context.Context, r vetypes.ReorgSeen) error {
	op := collections.Join(r.Txid, r.Vout)
	rec, err := k.Deposits.Get(ctx, op)
	if errors.Is(err, collections.ErrNotFound) {
		return errSkip("no accepted deposit at this outpoint")
	} else if err != nil {
		return err
	}
	if !bytes.Equal(rec.Block.Hash, r.OldBlockHash) {
		return errSkip("deposit was accepted in a different block")
	}
	u, err := k.Utxos.Get(ctx, op)
	if err != nil {
		return err
	}
	if u.Status != types.UTXO_STATUS_AVAILABLE {
		return errSkip("deposit UTXO is locked by a withdrawal batch")
	}
	if err := k.removeUtxo(ctx, u); err != nil {
		return err
	}
	if err := k.Deposits.Remove(ctx, op); err != nil {
		return err
	}

	short := sdkmath.ZeroInt()
	if rec.CollateralSat > 0 {
		s, err := k.vault.DebitCollateral(ctx, rec.VaultId, sdkmath.NewIntFromUint64(rec.CollateralSat))
		if err != nil {
			return err
		}
		short = short.Add(s)
	}
	if rec.ClaimableSat > 0 {
		owner := sdk.MustAccAddressFromBech32(rec.Owner)
		have, err := k.GetClaimable(ctx, owner)
		if err != nil {
			return err
		}
		take := sdkmath.MinInt(have, sdkmath.NewIntFromUint64(rec.ClaimableSat))
		if err := k.DebitClaimable(ctx, owner, take); err != nil {
			return err
		}
		short = short.Add(sdkmath.NewIntFromUint64(rec.ClaimableSat).Sub(take))
	}
	if rec.FeeReserveSat > 0 {
		have, err := k.FeeReserve.Get(ctx)
		if err != nil {
			return err
		}
		take := sdkmath.MinInt(have, sdkmath.NewIntFromUint64(rec.FeeReserveSat))
		if err := k.FeeReserve.Set(ctx, have.Sub(take)); err != nil {
			return err
		}
		short = short.Add(sdkmath.NewIntFromUint64(rec.FeeReserveSat).Sub(take))
	}
	if short.IsPositive() {
		def, err := k.Deficit.Get(ctx)
		if err != nil {
			return err
		}
		if err := k.Deficit.Set(ctx, def.Add(short)); err != nil {
			return err
		}
	}
	sdk.UnwrapSDKContext(ctx).EventManager().EmitEvent(sdk.NewEvent(types.EventTypeDepositReverted,
		sdk.NewAttribute(types.AttributeKeyOutpoint, types.OutpointString(r.Txid, r.Vout)),
		sdk.NewAttribute(types.AttributeKeyVault, strconv.FormatUint(rec.VaultId, 10)),
		sdk.NewAttribute(types.AttributeKeySat, strconv.FormatUint(u.AmountSat, 10)),
		sdk.NewAttribute(types.AttributeKeyDeficit, short.String())))
	return nil
}

func (k *Keeper) addFeeReserve(ctx context.Context, sat sdkmath.Int) error {
	cur, err := k.FeeReserve.Get(ctx)
	if err != nil {
		return err
	}
	return k.FeeReserve.Set(ctx, cur.Add(sat))
}

// orSkip returns err if set, else a skip with reason.
func orSkip(err error, reason string) error {
	if err != nil {
		return err
	}
	return errSkip(reason)
}

// DepositRecords returns every accepted deposit still held, in outpoint order (for the
// watcher's reorg checks).
func (k *Keeper) DepositRecords(ctx context.Context, fn func(txid []byte, vout uint32, rec types.DepositRecord) bool) error {
	return k.Deposits.Walk(ctx, nil, func(key collections.Pair[[]byte, uint32], rec types.DepositRecord) (bool, error) {
		return fn(key.K1(), key.K2(), rec), nil
	})
}
