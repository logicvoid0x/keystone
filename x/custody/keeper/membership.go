// FLAG FOR AUDIT: custody membership changes, sweeps and consolidation (design §9.4,
// §9.8 step 8).

package keeper

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"slices"
	"strconv"
	"strings"

	"cosmossdk.io/collections"
	sdkmath "cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/OWNER/keystone/x/custody/types"
)

// Consolidation thresholds (design §9.8 step 8).
const (
	ConsolidateAbove   = 50
	ConsolidateMaxRate = 5
)

// epochOperators returns the operators holding a share of epoch, sorted by address.
func (k *Keeper) epochOperators(ctx context.Context, epoch uint64) ([]sdk.AccAddress, error) {
	var out []sdk.AccAddress
	rng := collections.NewPrefixedPairRange[uint64, sdk.AccAddress](epoch)
	err := k.ShareIndex.Walk(ctx, rng, func(key collections.Pair[uint64, sdk.AccAddress], _ uint32) (bool, error) {
		out = append(out, key.K2())
		return false, nil
	})
	slices.SortFunc(out, func(a, b sdk.AccAddress) int { return bytes.Compare(a, b) })
	return out, err
}

// custodyCandidates are the active operators (power > 0) with registered custody keys,
// sorted: the set the next epoch's DKG would run among.
func (k *Keeper) custodyCandidates(ctx context.Context) ([]sdk.AccAddress, error) {
	ops, err := k.vals.ActiveValidators(ctx)
	if err != nil {
		return nil, err
	}
	var out []sdk.AccAddress
	for _, op := range ops {
		if op.Power <= 0 {
			continue
		}
		a, err := sdk.AccAddressFromBech32(op.OperatorAddress)
		if err != nil {
			return nil, err
		}
		if has, err := k.CustodyKeys.Has(ctx, a); err != nil {
			return nil, err
		} else if has {
			out = append(out, a)
		}
	}
	slices.SortFunc(out, func(a, b sdk.AccAddress) int { return bytes.Compare(a, b) })
	return out, nil
}

func diffSets(from, to []sdk.AccAddress) (added, removed []string) {
	has := func(set []sdk.AccAddress, a sdk.AccAddress) bool {
		return slices.ContainsFunc(set, func(b sdk.AccAddress) bool { return a.Equals(b) })
	}
	for _, a := range to {
		if !has(from, a) {
			added = append(added, a.String())
		}
	}
	for _, a := range from {
		if !has(to, a) {
			removed = append(removed, a.String())
		}
	}
	return added, removed
}

// membership is §9.4: when the active set differs from the active epoch's custody set
// it becomes pending (custody_membership_pending{added, removed, effective_height});
// once it has stayed unchanged for MembershipDelayBlocks (or at once with
// EmergencyRekey) the DKG for the next epoch starts among it.
func (k *Keeper) membership(ctx context.Context, active uint64) error {
	h := sdk.UnwrapSDKContext(ctx).BlockHeight()
	cur, err := k.epochOperators(ctx, active)
	if err != nil {
		return err
	}
	next, err := k.custodyCandidates(ctx)
	if err != nil {
		return err
	}
	pending, err := k.Pending.Get(ctx)
	hasPending := err == nil
	if err != nil && !errors.Is(err, collections.ErrNotFound) {
		return err
	}
	if slices.EqualFunc(cur, next, func(a, b sdk.AccAddress) bool { return a.Equals(b) }) || len(next) == 0 {
		if hasPending {
			return k.Pending.Remove(ctx)
		}
		return nil
	}
	hh := sha256.New()
	for _, a := range next {
		hh.Write(a)
	}
	hash := hh.Sum(nil)
	p, err := k.Params.Get(ctx)
	if err != nil {
		return err
	}
	if !hasPending || !bytes.Equal(pending.SetHash, hash) {
		added, removed := diffSets(cur, next)
		pending = types.PendingMembership{SetHash: hash, SinceHeight: h, Added: added, Removed: removed}
		if err := k.Pending.Set(ctx, pending); err != nil {
			return err
		}
		sdk.UnwrapSDKContext(ctx).EventManager().EmitEvent(sdk.NewEvent(types.EventTypeMembershipPending,
			sdk.NewAttribute(types.AttributeKeyAdded, strings.Join(added, ",")),
			sdk.NewAttribute(types.AttributeKeyRemoved, strings.Join(removed, ",")),
			sdk.NewAttribute(types.AttributeKeyEffectiveHeight, strconv.FormatInt(h+int64(p.MembershipDelayBlocks), 10))))
		if !p.EmergencyRekey {
			return nil
		}
	}
	if !p.EmergencyRekey && h < pending.SinceHeight+int64(p.MembershipDelayBlocks) {
		return nil
	}
	if err := k.Pending.Remove(ctx); err != nil {
		return err
	}
	_, err = k.startDKG(ctx, active+1, 0, h+1)
	return err
}

// retire marks RETIRING epochs RETIRED once all their UTXOs are spent, no batch of
// theirs is in flight, and RetireWatchBlocks have passed since they began retiring
// (their deposit addresses stay watched until then; §9.4 step 4).
func (k *Keeper) retire(ctx context.Context) error {
	h := sdk.UnwrapSDKContext(ctx).BlockHeight()
	p, err := k.Params.Get(ctx)
	if err != nil {
		return err
	}
	var due []types.KeyEpoch
	if err := k.Epochs.Walk(ctx, nil, func(_ uint64, e types.KeyEpoch) (bool, error) {
		if e.Status == types.EPOCH_STATUS_RETIRING && h >= e.RetiringHeight+int64(p.RetireWatchBlocks) {
			due = append(due, e)
		}
		return false, nil
	}); err != nil {
		return err
	}
	for _, e := range due {
		funded, err := k.epochFunded(ctx, e.Number)
		if err != nil {
			return err
		}
		inflight, err := k.InFlight.Has(ctx, e.Number)
		if err != nil {
			return err
		}
		if funded || inflight {
			continue
		}
		e.Status = types.EPOCH_STATUS_RETIRED
		if err := k.Epochs.Set(ctx, e.Number, e); err != nil {
			return err
		}
		emitDKG(ctx, types.EventTypeEpochRetired, e.Number)
	}
	return nil
}

// dueMoves lists the RETIRING epochs to sweep and whether the active epoch should
// consolidate this block (no batch of the epoch in flight; consolidation also needs
// more than ConsolidateAbove AVAILABLE UTXOs, the feerate at most ConsolidateMaxRate
// and no withdrawal batch due).
func (k *Keeper) dueMoves(ctx context.Context, feerate uint64, withdrawalDue bool) (sweeps []uint64, consolidate bool, err error) {
	active, err := k.ActiveEpoch.Get(ctx)
	if err != nil || active == 0 {
		return nil, false, err
	}
	counts := map[uint64]int{}
	rng := collections.NewPrefixedTripleRange[int32, uint64, []byte](int32(types.UTXO_STATUS_AVAILABLE))
	if err := k.UtxoIndex.Walk(ctx, rng, func(key collections.Triple[int32, uint64, []byte]) (bool, error) {
		txid, vout, _ := SplitOutpoint(key.K3())
		u, err := k.Utxos.Get(ctx, collections.Join(txid, vout))
		if err != nil {
			return true, err
		}
		counts[u.Epoch]++
		return false, nil
	}); err != nil {
		return nil, false, err
	}
	if err := k.Epochs.Walk(ctx, nil, func(n uint64, e types.KeyEpoch) (bool, error) {
		if e.Status != types.EPOCH_STATUS_RETIRING || counts[n] == 0 {
			return false, nil
		}
		if inflight, err := k.InFlight.Has(ctx, n); err != nil || inflight {
			return err != nil, err
		}
		sweeps = append(sweeps, n)
		return false, nil
	}); err != nil {
		return nil, false, err
	}
	if !withdrawalDue && counts[active] > ConsolidateAbove && feerate <= ConsolidateMaxRate {
		inflight, err := k.InFlight.Has(ctx, active)
		if err != nil {
			return nil, false, err
		}
		consolidate = !inflight
	}
	return sweeps, consolidate, nil
}

// openMove builds a SWEEP (all of epoch from's AVAILABLE UTXOs, largest first, up to
// MaxInputs, to the active epoch's Q0) or a CONSOLIDATE (the MaxInputs smallest of the
// active epoch into one Q0 output) batch. The fee comes out of the moved satoshis and
// is charged to the fee reserve; any part the reserve cannot cover is recorded in the
// custody deficit, so the books stay balanced (default assumption — review).
func (k *Keeper) openMove(ctx context.Context, kind types.BatchKind, from uint64, feerate uint64, voters []sdk.AccAddress) error {
	p, err := k.Params.Get(ctx)
	if err != nil {
		return err
	}
	if has, err := k.InFlight.Has(ctx, from); err != nil || has {
		return orSkip(err, "a batch of the epoch is in flight")
	}
	active, ok, err := k.GetActiveEpoch(ctx)
	if err != nil || !ok {
		return orSkip(err, "no active epoch")
	}
	src, err := k.Epochs.Get(ctx, from)
	if err != nil {
		return err
	}
	var utxos []types.Utxo
	rng := collections.NewPrefixedTripleRange[int32, uint64, []byte](int32(types.UTXO_STATUS_AVAILABLE))
	if err := k.UtxoIndex.Walk(ctx, rng, func(key collections.Triple[int32, uint64, []byte]) (bool, error) {
		txid, vout, _ := SplitOutpoint(key.K3())
		u, err := k.Utxos.Get(ctx, collections.Join(txid, vout))
		if err != nil {
			return true, err
		}
		if u.Epoch == from {
			utxos = append(utxos, u)
		}
		return false, nil
	}); err != nil {
		return err
	}
	if kind == types.BATCH_KIND_CONSOLIDATE {
		slices.Reverse(utxos) // smallest first
	}
	utxos = utxos[:min(len(utxos), int(p.MaxInputs))]
	if len(utxos) == 0 || (kind == types.BATCH_KIND_CONSOLIDATE && len(utxos) < 2) {
		return errSkip("nothing to move")
	}
	var total uint64
	for _, u := range utxos {
		total += u.AmountSat
	}
	fee := types.FeeFor(feerate, len(utxos), 1)
	if total < fee+types.ChangeDustSat {
		return errSkip("UTXOs do not cover the fee")
	}
	toScript, _, err := treasuryOutput(active)
	if err != nil {
		return err
	}
	plan := &batchPlan{epoch: src, inputs: utxos, feerate: feerate, fee: fee, change: total - fee}
	if err := k.openBatchTx(ctx, kind, plan, toScript, active.Number, voters); err != nil {
		return err
	}
	return k.chargeReserve(ctx, fee)
}

// chargeReserve takes sat out of the fee reserve; what it cannot cover goes to the
// deficit.
func (k *Keeper) chargeReserve(ctx context.Context, sat uint64) error {
	reserve, err := k.FeeReserve.Get(ctx)
	if err != nil {
		return err
	}
	need := sdkmath.NewIntFromUint64(sat)
	take := sdkmath.MinInt(reserve, need)
	if err := k.FeeReserve.Set(ctx, reserve.Sub(take)); err != nil {
		return err
	}
	if short := need.Sub(take); short.IsPositive() {
		def, err := k.Deficit.Get(ctx)
		if err != nil {
			return err
		}
		return k.Deficit.Set(ctx, def.Add(short))
	}
	return nil
}
