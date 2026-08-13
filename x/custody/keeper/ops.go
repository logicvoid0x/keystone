package keeper

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"strconv"

	"cosmossdk.io/collections"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/OWNER/keystone/app/protocol/vetypes"
	"github.com/OWNER/keystone/x/custody/types"
)

// pending is a tally touched this block, kept in memory until it is evaluated.
type pending struct {
	key   []byte
	tally types.Tally
	isNew bool
}

// BuildOps is design §9.7 steps 2–4 for one block: it expires tallies older than
// TallyTTL blocks, adds each verified voter once to the tally of every attestation it
// carries (anchor, deposits, reorgs), and accepts every touched tally whose voters hold
// more than 2/3 of the current power. Accepted tallies are deleted; the batch lists
// their payloads in canonical order. Malformed attestations are ignored.
func (k *Keeper) BuildOps(ctx context.Context, votes []types.Vote) (types.CustodyBatch, error) {
	var batch types.CustodyBatch
	height := sdk.UnwrapSDKContext(ctx).BlockHeight()
	if err := k.expireTallies(ctx, height); err != nil {
		return batch, err
	}

	touched := map[string]*pending{}
	var order []string
	add := func(kind types.AttestationKind, payload, voter []byte) error {
		key := types.TallyKey(kind, payload)
		p, ok := touched[string(key)]
		if !ok {
			t, err := k.Tallies.Get(ctx, key)
			switch {
			case errors.Is(err, collections.ErrNotFound):
				p = &pending{key: key, tally: types.Tally{Kind: kind, Payload: payload, FirstHeight: height}, isNew: true}
			case err != nil:
				return err
			default:
				p = &pending{key: key, tally: t}
			}
			touched[string(key)] = p
			order = append(order, string(key))
		}
		i, found := slices.BinarySearchFunc(p.tally.Voters, voter, bytes.Compare)
		if !found {
			p.tally.Voters = slices.Insert(p.tally.Voters, i, slices.Clone(voter))
		}
		return nil
	}
	for _, v := range votes {
		b := v.Bitcoin
		if b == nil {
			continue
		}
		if types.ValidBlockRef(b.Anchor) {
			if err := add(types.ATTESTATION_KIND_ANCHOR, mustMarshal(b.Anchor), v.ConsAddr); err != nil {
				return batch, err
			}
		}
		for i := range b.Deposits {
			if types.ValidDeposit(&b.Deposits[i]) {
				if err := add(types.ATTESTATION_KIND_DEPOSIT, mustMarshal(&b.Deposits[i]), v.ConsAddr); err != nil {
					return batch, err
				}
			}
		}
		for i := range b.Reorgs {
			if types.ValidReorg(&b.Reorgs[i]) {
				if err := add(types.ATTESTATION_KIND_REORG, mustMarshal(&b.Reorgs[i]), v.ConsAddr); err != nil {
					return batch, err
				}
			}
		}
		for i := range b.Confirms {
			if types.ValidConfirm(&b.Confirms[i]) {
				if err := add(types.ATTESTATION_KIND_CONFIRM, mustMarshal(&b.Confirms[i]), v.ConsAddr); err != nil {
					return batch, err
				}
			}
		}
	}

	power, total, err := k.powerFunc(ctx)
	if err != nil {
		return batch, err
	}
	slices.Sort(order)
	for _, key := range order {
		p := touched[key]
		if power(p.tally.Voters)*3 > total*2 {
			if err := k.acceptTally(ctx, p, &batch); err != nil {
				return batch, err
			}
			continue
		}
		if err := k.Tallies.Set(ctx, p.key, p.tally); err != nil {
			return batch, err
		}
		if p.isNew {
			if err := k.TallyExpiry.Set(ctx, collections.Join(p.tally.FirstHeight, p.key)); err != nil {
				return batch, err
			}
		}
	}
	batch.Sort()
	if err := k.buildFrost(ctx, votes, &batch); err != nil {
		return batch, err
	}
	return batch, nil
}

func mustMarshal(m interface{ Marshal() ([]byte, error) }) []byte {
	bz, err := m.Marshal()
	if err != nil {
		panic(err) // plain messages without maps cannot fail to marshal
	}
	return bz
}

// powerFunc returns a function giving the current power of a voter list (1 per active
// operator) and the current total power.
func (k *Keeper) powerFunc(ctx context.Context) (func([][]byte) int64, int64, error) {
	ops, err := k.vals.ActiveValidators(ctx)
	if err != nil {
		return nil, 0, err
	}
	active := make(map[string]int64, len(ops))
	var total int64
	for _, op := range ops {
		active[string(op.ConsAddress())] = op.Power
		total += op.Power
	}
	return func(voters [][]byte) int64 {
		var p int64
		for _, v := range voters {
			p += active[string(v)]
		}
		return p
	}, total, nil
}

// acceptTally removes an accepted tally and adds its payload to the batch.
func (k *Keeper) acceptTally(ctx context.Context, p *pending, batch *types.CustodyBatch) error {
	if !p.isNew {
		if err := k.Tallies.Remove(ctx, p.key); err != nil {
			return err
		}
		if err := k.TallyExpiry.Remove(ctx, collections.Join(p.tally.FirstHeight, p.key)); err != nil {
			return err
		}
	}
	switch p.tally.Kind {
	case types.ATTESTATION_KIND_ANCHOR:
		var a vetypes.BtcBlockRef
		if err := a.Unmarshal(p.tally.Payload); err != nil {
			return err
		}
		batch.Anchors = append(batch.Anchors, a)
	case types.ATTESTATION_KIND_DEPOSIT:
		var d vetypes.DepositSeen
		if err := d.Unmarshal(p.tally.Payload); err != nil {
			return err
		}
		batch.Credits = append(batch.Credits, d)
	case types.ATTESTATION_KIND_REORG:
		var r vetypes.ReorgSeen
		if err := r.Unmarshal(p.tally.Payload); err != nil {
			return err
		}
		batch.Reverts = append(batch.Reverts, r)
	case types.ATTESTATION_KIND_CONFIRM:
		var c vetypes.TxConfirmed
		if err := c.Unmarshal(p.tally.Payload); err != nil {
			return err
		}
		batch.Confirms = append(batch.Confirms, c)
	}
	return nil
}

// expireTallies deletes tallies created TallyTTL or more blocks ago.
func (k *Keeper) expireTallies(ctx context.Context, height int64) error {
	var expired []collections.Pair[int64, []byte]
	err := k.TallyExpiry.Walk(ctx, nil, func(key collections.Pair[int64, []byte]) (bool, error) {
		if height-key.K1() < types.TallyTTL {
			return true, nil
		}
		expired = append(expired, key)
		return false, nil
	})
	if err != nil {
		return err
	}
	for _, key := range expired {
		if err := k.TallyExpiry.Remove(ctx, key); err != nil {
			return err
		}
		if err := k.Tallies.Remove(ctx, key.K2()); err != nil {
			return err
		}
	}
	return nil
}

// ApplyOps applies a batch in the fixed order of §9.11: AcceptAnchor, CreditDeposit,
// RevertDeposit, RecordCommitments, ShareAccepted, SessionSigned, SessionFailed (each
// opening its retry, or stalling), the stalled-session re-check, ConfirmTx, StartRBF,
// OpenBatch. Totality: an operation that cannot apply is skipped with its writes
// discarded and a custody_op_skipped{op, reason} event; ApplyOps itself only fails on
// a store error.
func (k *Keeper) ApplyOps(ctx context.Context, batch types.CustodyBatch) error {
	for _, a := range batch.Anchors {
		k.runOp(ctx, types.OpAcceptAnchor, func(c context.Context) error { return k.acceptAnchor(c, a) })
	}
	for _, d := range batch.Credits {
		k.runOp(ctx, types.OpCreditDeposit, func(c context.Context) error { return k.creditDeposit(c, d) })
	}
	for _, r := range batch.Reverts {
		k.runOp(ctx, types.OpRevertDeposit, func(c context.Context) error { return k.revertDeposit(c, r) })
	}
	for _, f := range batch.Frost {
		k.runOp(ctx, types.OpRecordCommitments, func(c context.Context) error { return k.recordCommitments(c, f) })
	}
	for _, a := range batch.Accepted {
		k.runOp(ctx, types.OpShareAccepted, func(c context.Context) error { return k.applyShareAccepted(c, a) })
	}
	for _, s := range batch.Signed {
		k.runOp(ctx, types.OpSessionSigned, func(c context.Context) error { return k.applySessionSigned(c, s) })
	}
	for _, f := range batch.Failed {
		k.runOp(ctx, types.OpSessionFailed, func(c context.Context) error { return k.applySessionFailed(c, f, batch.Voters) })
	}
	k.runOp(ctx, types.OpSessionStalled, func(c context.Context) error { return k.recheckStalled(c, batch.Voters) })
	for _, cf := range batch.Confirms {
		k.runOp(ctx, types.OpConfirmTx, func(c context.Context) error { return k.confirmTx(c, cf) })
	}
	for _, id := range batch.RBF {
		k.runOp(ctx, types.OpStartRBF, func(c context.Context) error { return k.startRBF(c, id, batch.Voters) })
	}
	if ob := batch.OpenBatch; ob != nil {
		k.runOp(ctx, types.OpOpenBatch, func(c context.Context) error { return k.applyOpenBatch(c, *ob, batch.Voters) })
	}
	return nil
}

// errSkip marks an operation that does not apply; it is reported, not failed.
type errSkip string

func (e errSkip) Error() string { return string(e) }

func (k *Keeper) runOp(ctx context.Context, op string, f func(context.Context) error) {
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	cc, write := sdkCtx.CacheContext()
	if err := f(cc); err != nil {
		sdkCtx.EventManager().EmitEvent(sdk.NewEvent(types.EventTypeOpSkipped,
			sdk.NewAttribute(types.AttributeKeyOp, op), sdk.NewAttribute(types.AttributeKeyReason, err.Error())))
		return
	}
	write()
}

// acceptAnchor stores the anchor unless it would lower the accepted height (§9.7
// step 4). An anchor at the same height with a different hash replaces it.
func (k *Keeper) acceptAnchor(ctx context.Context, a vetypes.BtcBlockRef) error {
	cur, err := k.Anchor.Get(ctx)
	switch {
	case errors.Is(err, collections.ErrNotFound):
	case err != nil:
		return err
	case a.Height < cur.Height:
		return errSkip("anchor height would decrease")
	case a.Height == cur.Height && bytes.Equal(a.Hash, cur.Hash):
		return errSkip("anchor unchanged")
	}
	if err := k.Anchor.Set(ctx, a); err != nil {
		return err
	}
	sdk.UnwrapSDKContext(ctx).EventManager().EmitEvent(sdk.NewEvent(types.EventTypeAnchorAccepted,
		sdk.NewAttribute(types.AttributeKeyHeight, strconv.FormatUint(uint64(a.Height), 10)),
		sdk.NewAttribute(types.AttributeKeyHash, types.TxidString(a.Hash))))
	return nil
}

// buildFrost is the FROST part of BuildOps (§9.11): it maps each verified vote to its
// operator (only active operators count), collects the FROST payloads for
// RecordCommitments, verifies the shares of every OPEN session, picks the batches due
// for RBF and decides whether a withdrawal batch opens.
func (k *Keeper) buildFrost(ctx context.Context, votes []types.Vote, batch *types.CustodyBatch) error {
	ops, err := k.operators(ctx)
	if err != nil {
		return err
	}
	shares := map[string]map[string][]byte{}
	for _, v := range votes {
		op, ok := ops[string(v.ConsAddr)]
		if !ok {
			continue
		}
		i, found := slices.BinarySearchFunc(batch.Voters, op, func(a, b sdk.AccAddress) int { return bytes.Compare(a, b) })
		if found {
			continue // one vote per operator
		}
		batch.Voters = slices.Insert(batch.Voters, i, op)
		if v.Frost == nil {
			continue
		}
		batch.Frost = append(batch.Frost, types.FrostInput{Operator: op, Payload: *v.Frost})
		m := map[string][]byte{}
		for _, s := range v.Frost.Shares {
			m[string(s.SessionId)] = s.Z
		}
		shares[op.String()] = m
	}
	slices.SortFunc(batch.Frost, func(a, b types.FrostInput) int { return bytes.Compare(a.Operator, b.Operator) })
	if err := k.buildSigning(ctx, shares, batch); err != nil {
		return err
	}
	if batch.RBF, err = k.dueForRBF(ctx); err != nil {
		return err
	}
	p, err := k.Params.Get(ctx)
	if err != nil {
		return err
	}
	if due, err := k.shouldOpenBatch(ctx, p); err != nil {
		return err
	} else if due {
		if f, ok := medianFeerate(votes, p); ok {
			batch.OpenBatch = &types.OpenBatch{Feerate: f}
		}
	}
	return nil
}
