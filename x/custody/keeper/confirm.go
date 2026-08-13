package keeper

import (
	"bytes"
	"context"
	"errors"
	"strconv"

	"cosmossdk.io/collections"
	sdkmath "cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/OWNER/keystone/app/protocol/vetypes"
	"github.com/OWNER/keystone/x/custody/types"
)

// confirmTx is the ConfirmTx op (§9.8 step 6): a batch transaction confirmed at depth
// k with more than 2/3 of power. Its inputs are spent (deleted), its change output
// becomes an AVAILABLE UTXO, its requests are COMPLETED and the batch is CONFIRMED.
// If an older version than the current one confirmed, the fee reserve gets back what
// the later RBF bumps took from it. Sessions of the batch still active are closed.
func (k *Keeper) confirmTx(ctx context.Context, c vetypes.TxConfirmed) error {
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	id, err := k.TxBatch.Get(ctx, c.Txid)
	if errors.Is(err, collections.ErrNotFound) {
		return errSkip("not a Keystone transaction")
	} else if err != nil {
		return err
	}
	b, err := k.Batches.Get(ctx, id)
	if err != nil {
		return err
	}
	if b.Status == types.BATCH_STATUS_CONFIRMED {
		return errSkip("batch already confirmed")
	}
	var ver *types.BatchTx
	for i := range b.Txs {
		if bytes.Equal(b.Txs[i].Txid, c.Txid) {
			ver = &b.Txs[i]
		}
	}
	if ver == nil || ver.SignedTx == nil {
		return errSkip("transaction version was never signed")
	}
	for _, op := range b.Inputs {
		txid, vout, _ := SplitOutpoint(op)
		u, err := k.Utxos.Get(ctx, collections.Join(txid, vout))
		if err != nil {
			return err
		}
		if err := k.removeUtxo(ctx, u); err != nil {
			return err
		}
	}
	if ver.ChangeSat > 0 {
		e, err := k.Epochs.Get(ctx, b.Epoch)
		if err != nil {
			return err
		}
		_, tweak, err := treasuryOutput(e)
		if err != nil {
			return err
		}
		u := types.Utxo{Txid: ver.Txid, Vout: uint32(len(b.RequestIds)), AmountSat: ver.ChangeSat, Epoch: b.Epoch, Tweak: tweak, Status: types.UTXO_STATUS_AVAILABLE}
		if err := k.setUtxo(ctx, nil, u); err != nil {
			return err
		}
	}
	cur := b.Txs[len(b.Txs)-1]
	if ver.ChangeSat > cur.ChangeSat {
		if err := k.addFeeReserve(ctx, sdkmath.NewIntFromUint64(ver.ChangeSat-cur.ChangeSat)); err != nil {
			return err
		}
	}
	for _, rid := range b.RequestIds {
		r, err := k.Requests.Get(ctx, rid)
		if err != nil {
			return err
		}
		r.Status = types.REQUEST_STATUS_COMPLETED
		if err := k.Requests.Set(ctx, rid, r); err != nil {
			return err
		}
	}
	for _, v := range b.Txs {
		for _, sid := range v.SessionIds {
			if err := k.closeSession(ctx, sid); err != nil {
				return err
			}
		}
	}
	b.Status, b.ConfirmedTxid = types.BATCH_STATUS_CONFIRMED, ver.Txid
	if err := k.Batches.Set(ctx, id, b); err != nil {
		return err
	}
	if err := k.InFlight.Remove(ctx, b.Epoch); err != nil {
		return err
	}
	sdkCtx.EventManager().EmitEvent(sdk.NewEvent(types.EventTypeTxConfirmed,
		sdk.NewAttribute(types.AttributeKeyBatch, strconv.FormatUint(id, 10)),
		sdk.NewAttribute(types.AttributeKeyTxid, types.TxidString(ver.Txid)),
		sdk.NewAttribute(types.AttributeKeyHeight, strconv.FormatUint(uint64(c.Block.Height), 10))))
	return nil
}

// closeSession ends a still-active session of a confirmed batch without charging
// anyone.
func (k *Keeper) closeSession(ctx context.Context, id []byte) error {
	if has, err := k.ActiveSessions.Has(ctx, id); err != nil || !has {
		return err
	}
	s, err := k.Sessions.Get(ctx, id)
	if err != nil {
		return err
	}
	s.State = types.SESSION_STATE_FAILED
	if err := k.Sessions.Set(ctx, id, s); err != nil {
		return err
	}
	return k.ActiveSessions.Remove(ctx, id)
}

// dueForRBF lists in-flight SIGNED batches whose current version has seen
// RbfAfterBtcBlocks of anchor progress since it was signed (§9.8 step 7).
func (k *Keeper) dueForRBF(ctx context.Context) ([]uint64, error) {
	a, err := k.Anchor.Get(ctx)
	if errors.Is(err, collections.ErrNotFound) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	p, err := k.Params.Get(ctx)
	if err != nil {
		return nil, err
	}
	var due []uint64
	err = k.InFlight.Walk(ctx, nil, func(_ uint64, id uint64) (bool, error) {
		b, err := k.Batches.Get(ctx, id)
		if err != nil {
			return true, err
		}
		if b.Status == types.BATCH_STATUS_SIGNED && b.AnchorHeightAtSign > 0 &&
			a.Height >= b.AnchorHeightAtSign+p.RbfAfterBtcBlocks {
			due = append(due, id)
		}
		return false, nil
	})
	return due, err
}

// startRBF is the StartRBF op (§9.8 step 7): the same inputs and outputs at feerate
// max(ceil(1.5·F), F + 1) (so the fee rises by at least 1 sat/vB of the BIP-125
// minimum), capped at FeerateMax. The extra fee comes out of the change output and is
// charged to the fee reserve; the bump is skipped if the change would fall below
// ChangeDustSat or the reserve cannot cover it. New sessions open for every input.
func (k *Keeper) startRBF(ctx context.Context, id uint64, voters []sdk.AccAddress) error {
	p, err := k.Params.Get(ctx)
	if err != nil {
		return err
	}
	b, err := k.Batches.Get(ctx, id)
	if err != nil {
		return err
	}
	if b.Status != types.BATCH_STATUS_SIGNED {
		return errSkip("batch is not SIGNED")
	}
	cur := b.Txs[len(b.Txs)-1]
	rate := min(max((cur.FeerateSatVb*3+1)/2, cur.FeerateSatVb+1), p.FeerateMax)
	if rate <= cur.FeerateSatVb {
		return errSkip("feerate already at FeerateMax")
	}
	fee := types.FeeFor(rate, len(b.Inputs), len(b.RequestIds)+1)
	extra := fee - cur.FeeSat
	if cur.ChangeSat < extra+types.ChangeDustSat {
		return errSkip("change cannot pay the fee bump")
	}
	reserve, err := k.FeeReserve.Get(ctx)
	if err != nil {
		return err
	}
	if reserve.LT(sdkmath.NewIntFromUint64(extra)) {
		return errSkip("fee reserve cannot pay the fee bump")
	}
	e, err := k.Epochs.Get(ctx, b.Epoch)
	if err != nil {
		return err
	}
	old, err := types.DeserializeTx(cur.UnsignedTx)
	if err != nil {
		return err
	}
	ins := make([]types.TxInput, len(b.Inputs))
	tweaks := make([][]byte, len(b.Inputs))
	for i, op := range b.Inputs {
		txid, vout, _ := SplitOutpoint(op)
		u, err := k.Utxos.Get(ctx, collections.Join(txid, vout))
		if err != nil {
			return err
		}
		script, err := utxoScript(e, u.Tweak)
		if err != nil {
			return err
		}
		ins[i], tweaks[i] = types.TxInput{Txid: u.Txid, Vout: u.Vout, Amount: u.AmountSat, PkScript: script}, u.Tweak
	}
	outs := make([]types.TxOutput, len(old.TxOut))
	for i, o := range old.TxOut {
		outs[i] = types.TxOutput{Script: o.PkScript, Amount: uint64(o.Value)}
	}
	outs[len(outs)-1].Amount = cur.ChangeSat - extra
	tx, err := types.BuildTx(ins, outs)
	if err != nil {
		return err
	}
	msgs, err := types.Sighashes(tx, ins)
	if err != nil {
		return err
	}
	txid := types.Txid(tx)
	ver := types.BatchTx{Txid: txid, UnsignedTx: types.SerializeTx(tx), FeerateSatVb: rate, FeeSat: fee, ChangeSat: cur.ChangeSat - extra}
	idx := uint32(len(b.Txs))
	for i := range ins {
		sid, err := k.openSession(ctx, types.SigningSession{
			Epoch: b.Epoch, Msg: msgs[i], Tweak: tweaks[i], BatchId: id, InputIndex: uint32(i), TxVersion: idx,
		}, txid, voters)
		if err != nil {
			return err
		}
		ver.SessionIds = append(ver.SessionIds, sid)
	}
	b.Txs = append(b.Txs, ver)
	b.Status = types.BATCH_STATUS_SIGNING
	b.RbfCount++
	if err := k.FeeReserve.Set(ctx, reserve.Sub(sdkmath.NewIntFromUint64(extra))); err != nil {
		return err
	}
	if err := k.Batches.Set(ctx, id, b); err != nil {
		return err
	}
	if err := k.TxBatch.Set(ctx, txid, id); err != nil {
		return err
	}
	sdk.UnwrapSDKContext(ctx).EventManager().EmitEvent(sdk.NewEvent(types.EventTypeRBFStarted,
		sdk.NewAttribute(types.AttributeKeyBatch, strconv.FormatUint(id, 10)),
		sdk.NewAttribute(types.AttributeKeyTxid, types.TxidString(txid)),
		sdk.NewAttribute(types.AttributeKeyFeerate, strconv.FormatUint(rate, 10)),
		sdk.NewAttribute(types.AttributeKeyFee, strconv.FormatUint(fee, 10))))
	return nil
}
