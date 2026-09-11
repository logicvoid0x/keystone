package keeper

import (
	"context"
	"encoding/hex"
	"errors"
	"slices"
	"strconv"

	"cosmossdk.io/collections"
	errorsmod "cosmossdk.io/errors"
	sdkmath "cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/OWNER/keystone/crypto/frost"
	"github.com/OWNER/keystone/x/custody/types"
)

// ValidateWithdrawal checks a withdrawal before the caller debits a balance: the
// script kind (§9.8 step 1), amount ≥ MinWithdrawSat, and no custody deficit.
func (k *Keeper) ValidateWithdrawal(ctx context.Context, script []byte, sat sdkmath.Int) error {
	if _, err := types.WithdrawScriptKind(script); err != nil {
		return errorsmod.Wrap(types.ErrInvalidScript, err.Error())
	}
	p, err := k.Params.Get(ctx)
	if err != nil {
		return err
	}
	if sat.IsNil() || sat.LT(p.MinWithdrawSat) || !sat.IsUint64() {
		return errorsmod.Wrapf(types.ErrInvalidAmount, "withdrawal of %v sat; minimum is %s", sat, p.MinWithdrawSat)
	}
	return k.CheckNoDeficit(ctx)
}

// QueueWithdrawal appends a request to the FIFO queue (§9.8 step 1). The caller has
// already debited sat from the owner's collateral or claimable balance.
func (k *Keeper) QueueWithdrawal(ctx context.Context, owner sdk.AccAddress, script []byte, sat sdkmath.Int) (uint64, error) {
	if err := k.ValidateWithdrawal(ctx, script, sat); err != nil {
		return 0, err
	}
	n, err := k.NextRequest.Next(ctx)
	if err != nil {
		return 0, err
	}
	id := n + 1
	req := types.WithdrawalRequest{
		Id: id, Owner: owner.String(), RecipientScript: slices.Clone(script), AmountSat: sat.Uint64(),
		Status: types.REQUEST_STATUS_QUEUED, CreatedHeight: sdk.UnwrapSDKContext(ctx).BlockHeight(),
	}
	if err := k.Requests.Set(ctx, id, req); err != nil {
		return 0, err
	}
	if err := k.Queue.Set(ctx, id); err != nil {
		return 0, err
	}
	sdk.UnwrapSDKContext(ctx).EventManager().EmitEvent(sdk.NewEvent(types.EventTypeWithdrawalRequested,
		sdk.NewAttribute(types.AttributeKeyID, strconv.FormatUint(id, 10)),
		sdk.NewAttribute(types.AttributeKeyOwner, req.Owner),
		sdk.NewAttribute(types.AttributeKeySat, sat.String()),
		sdk.NewAttribute(types.AttributeKeyScript, hex.EncodeToString(script))))
	return id, nil
}

// WithdrawClaim is MsgWithdrawClaim: debit the claimable balance and queue a request.
func (k *Keeper) WithdrawClaim(ctx context.Context, msg *types.MsgWithdrawClaim) (uint64, error) {
	if err := msg.ValidateBasic(); err != nil {
		return 0, err
	}
	if err := k.ValidateWithdrawal(ctx, msg.RecipientScript, msg.AmountSat); err != nil {
		return 0, err
	}
	owner := sdk.MustAccAddressFromBech32(msg.Owner)
	if err := k.DebitClaimable(ctx, owner, msg.AmountSat); err != nil {
		return 0, err
	}
	return k.QueueWithdrawal(ctx, owner, msg.RecipientScript, msg.AmountSat)
}

// QueuedSat is the total of QUEUED requests.
func (k *Keeper) QueuedSat(ctx context.Context) (sdkmath.Int, error) {
	total := sdkmath.ZeroInt()
	err := k.Queue.Walk(ctx, nil, func(id uint64) (bool, error) {
		r, err := k.Requests.Get(ctx, id)
		if err != nil {
			return true, err
		}
		total = total.Add(sdkmath.NewIntFromUint64(r.AmountSat))
		return false, nil
	})
	return total, err
}

// InFlightSat is what unconfirmed batches will spend out of the treasury: inputs
// minus change of each batch's current version (recipient outputs plus the fee).
func (k *Keeper) InFlightSat(ctx context.Context) (sdkmath.Int, error) {
	total := sdkmath.ZeroInt()
	err := k.InFlight.Walk(ctx, nil, func(_ uint64, id uint64) (bool, error) {
		b, err := k.Batches.Get(ctx, id)
		if err != nil {
			return true, err
		}
		var in uint64
		for _, a := range b.InputAmounts {
			in += a
		}
		total = total.Add(sdkmath.NewIntFromUint64(in - b.Txs[len(b.Txs)-1].ChangeSat))
		return false, nil
	})
	return total, err
}

// shouldOpenBatch is §9.8 step 2: an ACTIVE epoch with no batch in flight, and a
// queue of ≥ BatchMinRequests requests or one whose oldest request is BatchMaxWait
// blocks old.
func (k *Keeper) shouldOpenBatch(ctx context.Context, p types.Params) (bool, error) {
	e, ok, err := k.GetActiveEpoch(ctx)
	if err != nil || !ok {
		return false, err
	}
	if has, err := k.InFlight.Has(ctx, e.Number); err != nil || has {
		return false, err
	}
	var count uint32
	var oldest int64 = -1
	err = k.Queue.Walk(ctx, nil, func(id uint64) (bool, error) {
		if oldest < 0 {
			r, err := k.Requests.Get(ctx, id)
			if err != nil {
				return true, err
			}
			oldest = r.CreatedHeight
		}
		count++
		return count >= p.BatchMinRequests, nil
	})
	if err != nil || count == 0 {
		return false, err
	}
	h := sdk.UnwrapSDKContext(ctx).BlockHeight()
	return count >= p.BatchMinRequests || h-oldest >= int64(p.BatchMaxWait), nil
}

// batchPlan is a constructed batch before it is stored.
type batchPlan struct {
	epoch    types.KeyEpoch
	inputs   []types.Utxo
	requests []types.WithdrawalRequest
	feerate  uint64
	share    uint64 // fee share of each request
	fee      uint64 // the transaction fee
	change   uint64
}

// planBatch is §9.8 step 3: AVAILABLE UTXOs of the active epoch by (amount desc, txid
// asc, vout asc), taken until they cover the outputs, the fee and a change output of at
// least ChangeDustSat, at most MaxInputs; the longest FIFO prefix of at most
// MaxBatchRequests requests that fits, each request paying FeeShare and keeping at
// least RecipientDustSat. It returns nil when nothing fits.
func (k *Keeper) planBatch(ctx context.Context, p types.Params, feerate uint64) (*batchPlan, error) {
	e, ok, err := k.GetActiveEpoch(ctx)
	if err != nil || !ok {
		return nil, err
	}
	var reqs []types.WithdrawalRequest
	err = k.Queue.Walk(ctx, nil, func(id uint64) (bool, error) {
		r, err := k.Requests.Get(ctx, id)
		if err != nil {
			return true, err
		}
		reqs = append(reqs, r)
		return len(reqs) >= types.MaxBatchRequests, nil
	})
	if err != nil {
		return nil, err
	}
	var utxos []types.Utxo
	rng := collections.NewPrefixedTripleRange[int32, uint64, []byte](int32(types.UTXO_STATUS_AVAILABLE))
	err = k.UtxoIndex.Walk(ctx, rng, func(key collections.Triple[int32, uint64, []byte]) (bool, error) {
		txid, vout, ok := SplitOutpoint(key.K3())
		if !ok {
			return true, errors.New("malformed utxo index key")
		}
		u, err := k.Utxos.Get(ctx, collections.Join(txid, vout))
		if err != nil {
			return true, err
		}
		if u.Epoch == e.Number {
			utxos = append(utxos, u)
		}
		return len(utxos) >= int(p.MaxInputs), nil
	})
	if err != nil {
		return nil, err
	}

	for n := len(reqs); n >= 1; n-- {
		var want, minAmount uint64
		minAmount = ^uint64(0)
		for _, r := range reqs[:n] {
			want += r.AmountSat
			minAmount = min(minAmount, r.AmountSat)
		}
		var have uint64
		for in := 1; in <= len(utxos); in++ {
			have += utxos[in-1].AmountSat
			share := types.FeeShare(feerate, in, n)
			if minAmount < share+types.RecipientDustSat {
				break // more inputs only raise the share
			}
			fee := types.FeeFor(feerate, in, n+1)
			net := want - uint64(n)*share
			if have < net+fee+types.ChangeDustSat {
				continue
			}
			return &batchPlan{
				epoch: e, inputs: slices.Clone(utxos[:in]), requests: slices.Clone(reqs[:n]),
				feerate: feerate, share: share, fee: fee, change: have - net - fee,
			}, nil
		}
	}
	return nil, nil
}

// utxoScript is the P2TR script of a UTXO of epoch e: OP_1 x(P + t·G).
func utxoScript(e types.KeyEpoch, tweak []byte) ([]byte, error) {
	key, err := frost.NewKey(frost.Taproot, e.GroupKey, tweak)
	if err != nil {
		return nil, err
	}
	x := key.XOnly()
	return append([]byte{0x51, 0x20}, x[:]...), nil
}

// treasuryOutput is Q0's script and tweak t0 for epoch e.
func treasuryOutput(e types.KeyEpoch) (script, tweak []byte, err error) {
	p, _, err := types.ParseGroupKey(e.GroupKey)
	if err != nil {
		return nil, nil, err
	}
	return types.TreasuryScript(p)
}

// openBatch stores a planned batch (§9.8 step 3): the unsigned transaction, requests
// BATCHED with their fee share, the fee-reserve difference, inputs LOCKED and one
// signing session per input.
func (k *Keeper) openBatch(ctx context.Context, plan *batchPlan, voters []sdk.AccAddress) error {
	changeScript, _, err := treasuryOutput(plan.epoch)
	if err != nil {
		return err
	}
	return k.openBatchTx(ctx, types.BATCH_KIND_WITHDRAW, plan, changeScript, plan.epoch.Number, voters)
}

// openBatchTx stores a batch: requests' outputs (WITHDRAW), then the change output to
// changeScript (Q0 of changeEpoch); for WITHDRAW the fee shares beyond the fee go to
// the fee reserve.
func (k *Keeper) openBatchTx(ctx context.Context, kind types.BatchKind, plan *batchPlan, changeScript []byte, changeEpoch uint64, voters []sdk.AccAddress) error {
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	n, err := k.NextBatch.Next(ctx)
	if err != nil {
		return err
	}
	id := n + 1
	ins := make([]types.TxInput, len(plan.inputs))
	b := types.WithdrawalBatch{Id: id, Epoch: plan.epoch.Number, Kind: kind, ChangeEpoch: changeEpoch,
		Status: types.BATCH_STATUS_SIGNING, OpenedHeight: sdkCtx.BlockHeight()}
	for i, u := range plan.inputs {
		script, err := utxoScript(plan.epoch, u.Tweak)
		if err != nil {
			return err
		}
		ins[i] = types.TxInput{Txid: u.Txid, Vout: u.Vout, Amount: u.AmountSat, PkScript: script}
		b.Inputs = append(b.Inputs, OutpointBytes(u.Txid, u.Vout))
		b.InputAmounts = append(b.InputAmounts, u.AmountSat)
	}
	outs := make([]types.TxOutput, 0, len(plan.requests)+1)
	for _, r := range plan.requests {
		outs = append(outs, types.TxOutput{Script: r.RecipientScript, Amount: r.AmountSat - plan.share})
		b.RequestIds = append(b.RequestIds, r.Id)
	}
	outs = append(outs, types.TxOutput{Script: changeScript, Amount: plan.change})
	tx, err := types.BuildTx(ins, outs)
	if err != nil {
		return err
	}
	msgs, err := types.Sighashes(tx, ins)
	if err != nil {
		return err
	}
	txid := types.Txid(tx)
	bt := types.BatchTx{Txid: txid, UnsignedTx: types.SerializeTx(tx), FeerateSatVb: plan.feerate, FeeSat: plan.fee, ChangeSat: plan.change}

	for i, u := range plan.inputs {
		prev := u
		u.Status, u.LockedByBatch = types.UTXO_STATUS_LOCKED, id
		if err := k.setUtxo(ctx, &prev, u); err != nil {
			return err
		}
		sid, err := k.openSession(ctx, types.SigningSession{
			Epoch: plan.epoch.Number, Msg: msgs[i], Tweak: u.Tweak, BatchId: id, InputIndex: uint32(i),
		}, txid, voters)
		if err != nil {
			return err
		}
		bt.SessionIds = append(bt.SessionIds, sid)
	}
	b.Txs = []types.BatchTx{bt}
	for _, r := range plan.requests {
		r.Status, r.BatchId, r.FeeSat = types.REQUEST_STATUS_BATCHED, id, plan.share
		if err := k.Requests.Set(ctx, r.Id, r); err != nil {
			return err
		}
		if err := k.Queue.Remove(ctx, r.Id); err != nil {
			return err
		}
	}
	// Σ fee shares ≥ the fee (FeeShare uses 2× the estimate), so this never goes down.
	if kind == types.BATCH_KIND_WITHDRAW {
		if err := k.addFeeReserve(ctx, sdkmath.NewIntFromUint64(uint64(len(plan.requests))*plan.share-plan.fee)); err != nil {
			return err
		}
	}
	if err := k.Batches.Set(ctx, id, b); err != nil {
		return err
	}
	if err := k.TxBatch.Set(ctx, txid, id); err != nil {
		return err
	}
	if err := k.InFlight.Set(ctx, plan.epoch.Number, id); err != nil {
		return err
	}
	sdkCtx.EventManager().EmitEvent(sdk.NewEvent(types.EventTypeBatchOpened,
		sdk.NewAttribute(types.AttributeKeyBatch, strconv.FormatUint(id, 10)),
		sdk.NewAttribute(types.AttributeKeyKind, kind.String()),
		sdk.NewAttribute(types.AttributeKeyTxid, types.TxidString(txid)),
		sdk.NewAttribute(types.AttributeKeyInputs, strconv.Itoa(len(ins))),
		sdk.NewAttribute(types.AttributeKeyRequests, strconv.Itoa(len(plan.requests))),
		sdk.NewAttribute(types.AttributeKeyFeerate, strconv.FormatUint(plan.feerate, 10)),
		sdk.NewAttribute(types.AttributeKeyFee, strconv.FormatUint(plan.fee, 10)),
		sdk.NewAttribute(types.AttributeKeyChange, strconv.FormatUint(plan.change, 10))))
	return nil
}

// applyOpenBatch is the OpenBatch op.
func (k *Keeper) applyOpenBatch(ctx context.Context, ob types.OpenBatch, voters []sdk.AccAddress) error {
	p, err := k.Params.Get(ctx)
	if err != nil {
		return err
	}
	if ok, err := k.shouldOpenBatch(ctx, p); err != nil || !ok {
		return orSkip(err, "no batch due")
	}
	plan, err := k.planBatch(ctx, p, ob.Feerate)
	if err != nil {
		return err
	}
	if plan == nil {
		return errSkip("treasury UTXOs do not cover the oldest request")
	}
	return k.openBatch(ctx, plan, voters)
}

// medianFeerate is the median (upper middle) of the positive VE feerates, clamped to
// [FeerateMin, FeerateMax]; ok is false when no vote carries one.
func medianFeerate(votes []types.Vote, p types.Params) (uint64, bool) {
	var rates []uint64
	for _, v := range votes {
		if v.Bitcoin != nil && v.Bitcoin.FeerateSatVb > 0 {
			rates = append(rates, v.Bitcoin.FeerateSatVb)
		}
	}
	if len(rates) == 0 {
		return 0, false
	}
	slices.Sort(rates)
	return min(max(rates[len(rates)/2], p.FeerateMin), p.FeerateMax), true
}

// GetBatch returns a batch.
func (k *Keeper) GetBatch(ctx context.Context, id uint64) (types.WithdrawalBatch, error) {
	b, err := k.Batches.Get(ctx, id)
	if errors.Is(err, collections.ErrNotFound) {
		return b, errorsmod.Wrapf(types.ErrNotFound, "batch %d", id)
	}
	return b, err
}
