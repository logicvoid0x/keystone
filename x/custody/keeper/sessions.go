// FLAG FOR AUDIT: FROST session state machine (design §9.10, §9.11).

package keeper

import (
	"bytes"
	"cmp"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"cosmossdk.io/collections"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/OWNER/keystone/crypto/frost"
	"github.com/OWNER/keystone/x/custody/types"
)

// operators maps the active validator set (power > 0): consensus address → operator.
func (k *Keeper) operators(ctx context.Context) (map[string]sdk.AccAddress, error) {
	ops, err := k.vals.ActiveValidators(ctx)
	if err != nil {
		return nil, err
	}
	m := make(map[string]sdk.AccAddress, len(ops))
	for _, op := range ops {
		if op.Power <= 0 {
			continue
		}
		addr, err := sdk.AccAddressFromBech32(op.OperatorAddress)
		if err != nil {
			return nil, err
		}
		m[string(op.ConsAddress())] = addr
	}
	return m, nil
}

func (k *Keeper) stats(ctx context.Context, epoch uint64, op sdk.AccAddress) (types.SignerStats, error) {
	s, err := k.SignerStats.Get(ctx, collections.Join(epoch, op))
	if errors.Is(err, collections.ErrNotFound) {
		return types.SignerStats{}, nil
	}
	return s, err
}

// assignSigners is §9.10 signer selection for a new session of epoch e: operators
// holding a share of e that are active, voted this block, are not blamed this epoch,
// are not excluded and have an unconsumed commitment, sorted by (missed asc, operator
// address asc); the first t are assigned their lowest unconsumed commitment, which
// leaves the pool. ok is false (nothing consumed) when fewer than t are eligible.
func (k *Keeper) assignSigners(ctx context.Context, e types.KeyEpoch, voters []sdk.AccAddress, excluded []string) ([]types.SignerAssignment, bool, error) {
	type cand struct {
		op     sdk.AccAddress
		index  uint32
		missed uint64
		seq    uint64
		c      types.StoredCommitment
	}
	var cands []cand
	for _, op := range voters { // sorted, active (from this block's verified votes)
		if slices.Contains(excluded, op.String()) {
			continue
		}
		idx, err := k.ShareIndex.Get(ctx, collections.Join(e.Number, op))
		if errors.Is(err, collections.ErrNotFound) {
			continue
		} else if err != nil {
			return nil, false, err
		}
		st, err := k.stats(ctx, e.Number, op)
		if err != nil {
			return nil, false, err
		}
		if st.Invalid > 0 {
			continue
		}
		found := false
		var c cand
		rng := collections.NewSuperPrefixedTripleRange[uint64, uint32, uint64](e.Number, idx)
		err = k.Commitments.Walk(ctx, rng, func(key collections.Triple[uint64, uint32, uint64], sc types.StoredCommitment) (bool, error) {
			c, found = cand{op: op, index: idx, missed: st.Missed, seq: key.K3(), c: sc}, true
			return true, nil
		})
		if err != nil {
			return nil, false, err
		}
		if found {
			cands = append(cands, c)
		}
	}
	if len(cands) < int(e.T) {
		return nil, false, nil
	}
	slices.SortFunc(cands, func(a, b cand) int { return cmp.Or(cmp.Compare(a.missed, b.missed), bytes.Compare(a.op, b.op)) })
	cands = cands[:e.T]
	out := make([]types.SignerAssignment, len(cands))
	for i, c := range cands {
		if err := k.Commitments.Remove(ctx, collections.Join3(e.Number, c.index, c.seq)); err != nil {
			return nil, false, err
		}
		st, err := k.stats(ctx, e.Number, c.op)
		if err != nil {
			return nil, false, err
		}
		st.Assigned++
		if err := k.SignerStats.Set(ctx, collections.Join(e.Number, c.op), st); err != nil {
			return nil, false, err
		}
		out[i] = types.SignerAssignment{ShareIndex: c.index, Validator: c.op.String(), NonceSeq: c.seq, Hiding: c.c.Hiding, Binding: c.c.Binding}
	}
	slices.SortFunc(out, func(a, b types.SignerAssignment) int { return cmp.Compare(a.ShareIndex, b.ShareIndex) })
	return out, true, nil
}

// openSession creates session s (attempt, msg, tweak, batch, input set by the caller)
// with id SessionID(chain, epoch, txid, input, attempt), assigns signers and stores it
// OPEN, or STALLED when too few signers are eligible. It returns the id.
func (k *Keeper) openSession(ctx context.Context, s types.SigningSession, txid []byte, voters []sdk.AccAddress) ([]byte, error) {
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	s.Id = types.SessionID(sdkCtx.ChainID(), s.Epoch, txid, s.InputIndex, s.Attempt)
	if has, err := k.Sessions.Has(ctx, s.Id); err != nil {
		return nil, err
	} else if has {
		return nil, fmt.Errorf("session %x already exists", s.Id)
	}
	s.OpenedHeight = sdkCtx.BlockHeight()
	if err := k.tryAssign(ctx, &s, voters); err != nil {
		return nil, err
	}
	if err := k.Sessions.Set(ctx, s.Id, s); err != nil {
		return nil, err
	}
	if err := k.ActiveSessions.Set(ctx, s.Id); err != nil {
		return nil, err
	}
	return s.Id, nil
}

// tryAssign assigns signers to s and makes it OPEN with a fresh deadline, or STALLED.
// It emits session_opened or session_stalled.
func (k *Keeper) tryAssign(ctx context.Context, s *types.SigningSession, voters []sdk.AccAddress) error {
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	e, err := k.Epochs.Get(ctx, s.Epoch)
	if err != nil {
		return err
	}
	p, err := k.Params.Get(ctx)
	if err != nil {
		return err
	}
	signers, ok, err := k.assignSigners(ctx, e, voters, s.Excluded)
	if err != nil {
		return err
	}
	if !ok {
		s.State, s.Signers = types.SESSION_STATE_STALLED, nil
		sdkCtx.EventManager().EmitEvent(sdk.NewEvent(types.EventTypeSessionStalled,
			sdk.NewAttribute(types.AttributeKeySession, hex.EncodeToString(s.Id)),
			sdk.NewAttribute(types.AttributeKeyBatch, strconv.FormatUint(s.BatchId, 10)),
			sdk.NewAttribute(types.AttributeKeyInput, strconv.FormatUint(uint64(s.InputIndex), 10))))
		return nil
	}
	s.State, s.Signers, s.Received = types.SESSION_STATE_OPEN, signers, nil
	s.Deadline = sdkCtx.BlockHeight() + int64(p.SessionDeadline)
	names := make([]string, len(signers))
	for i, a := range signers {
		names[i] = a.Validator
	}
	sdkCtx.EventManager().EmitEvent(sdk.NewEvent(types.EventTypeSessionOpened,
		sdk.NewAttribute(types.AttributeKeySession, hex.EncodeToString(s.Id)),
		sdk.NewAttribute(types.AttributeKeyBatch, strconv.FormatUint(s.BatchId, 10)),
		sdk.NewAttribute(types.AttributeKeyInput, strconv.FormatUint(uint64(s.InputIndex), 10)),
		sdk.NewAttribute(types.AttributeKeyAttempt, strconv.FormatUint(uint64(s.Attempt), 10)),
		sdk.NewAttribute(types.AttributeKeySigners, strings.Join(names, ","))))
	return nil
}

// signing builds the public FROST signing state of an OPEN session.
func (k *Keeper) signing(ctx context.Context, s types.SigningSession) (*frost.Signing, error) {
	e, err := k.Epochs.Get(ctx, s.Epoch)
	if err != nil {
		return nil, err
	}
	key, err := frost.NewKey(frost.Taproot, e.GroupKey, s.Tweak)
	if err != nil {
		return nil, err
	}
	cs := make([]frost.Commitment, len(s.Signers))
	for i, a := range s.Signers {
		if cs[i], err = frost.ParseCommitment(a.ShareIndex, a.Hiding, a.Binding); err != nil {
			return nil, err
		}
	}
	return frost.NewSigning(key, s.Msg, cs)
}

// sessionValidators returns the distinct validators of s sorted by address bytes, with
// the share indices each holds.
func sessionValidators(s types.SigningSession) ([]sdk.AccAddress, map[string][]uint32) {
	shares := map[string][]uint32{}
	var vs []sdk.AccAddress
	for _, a := range s.Signers {
		if _, ok := shares[a.Validator]; !ok {
			vs = append(vs, sdk.MustAccAddressFromBech32(a.Validator))
		}
		shares[a.Validator] = append(shares[a.Validator], a.ShareIndex)
	}
	slices.SortFunc(vs, func(a, b sdk.AccAddress) int { return bytes.Compare(a, b) })
	return vs, shares
}

func hasReceived(s types.SigningSession, v string) bool {
	_, ok := slices.BinarySearchFunc(s.Received, v, func(r types.ValidatorShare, v string) int { return strings.Compare(r.Validator, v) })
	return ok
}

// buildSigning is the §9.11 on-chain processing, run by BuildOps for every OPEN
// session in id order: each assigned operator's combined share from this block's VEs is
// verified; an invalid one fails the session with blame; when every operator has a
// valid share the signature is aggregated and checked (a failure there is a bug and
// panics, identically on every node); at the deadline the session fails with the
// missing operators. shares maps operator → session id → z.
func (k *Keeper) buildSigning(ctx context.Context, shares map[string]map[string][]byte, batch *types.CustodyBatch) error {
	height := sdk.UnwrapSDKContext(ctx).BlockHeight()
	var ids [][]byte
	if err := k.ActiveSessions.Walk(ctx, nil, func(id []byte) (bool, error) {
		ids = append(ids, id)
		return false, nil
	}); err != nil {
		return err
	}
sessions:
	for _, id := range ids {
		s, err := k.Sessions.Get(ctx, id)
		if err != nil {
			return err
		}
		if s.State != types.SESSION_STATE_OPEN {
			continue
		}
		sg, err := k.signing(ctx, s)
		if err != nil {
			// Unusable commitments (cannot happen for recorded points): fail without
			// blame so a retry gets fresh ones.
			batch.Failed = append(batch.Failed, types.SessionFailed{SessionID: id})
			continue
		}
		vs, held := sessionValidators(s)
		valid := map[string][]byte{}
		for _, r := range s.Received {
			valid[r.Validator] = r.Z
		}
		var accepted []types.ShareAccepted
		for _, v := range vs {
			name := v.String()
			if hasReceived(s, name) {
				continue
			}
			z, ok := shares[name][string(id)]
			if !ok {
				continue
			}
			ys := make([]frost.Point, len(held[name]))
			for i, idx := range held[name] {
				yb, err := k.VerifyingShares.Get(ctx, collections.Join(s.Epoch, idx))
				if err != nil {
					return err
				}
				if ys[i], err = frost.ParsePoint(yb); err != nil {
					return err
				}
			}
			zs, err := frost.ParseScalar(z)
			if err != nil || !sg.VerifyShares(held[name], ys, zs) {
				batch.Failed = append(batch.Failed, types.SessionFailed{SessionID: id, Blame: name})
				continue sessions
			}
			accepted = append(accepted, types.ShareAccepted{SessionID: id, Validator: name, Z: z})
			valid[name] = z
		}
		batch.Accepted = append(batch.Accepted, accepted...)
		if len(valid) == len(vs) {
			zs := make([]frost.Scalar, 0, len(vs))
			for _, v := range vs {
				z, err := frost.ParseScalar(valid[v.String()])
				if err != nil {
					return err
				}
				zs = append(zs, z)
			}
			sig, err := sg.Aggregate(zs)
			if err != nil || !frost.Verify(sg.Key(), s.Msg, sig) {
				panic(fmt.Sprintf("frost aggregation bug: session %x", id)) // identical on every node
			}
			batch.Signed = append(batch.Signed, types.SessionSigned{SessionID: id, Signature: sig})
		} else if height >= s.Deadline {
			var missing []string
			for _, v := range vs {
				if _, ok := valid[v.String()]; !ok {
					missing = append(missing, v.String())
				}
			}
			batch.Failed = append(batch.Failed, types.SessionFailed{SessionID: id, Missing: missing})
		}
	}
	return nil
}

// applyShareAccepted records a verified combined share on its session.
func (k *Keeper) applyShareAccepted(ctx context.Context, a types.ShareAccepted) error {
	s, err := k.Sessions.Get(ctx, a.SessionID)
	if err != nil {
		return err
	}
	if s.State != types.SESSION_STATE_OPEN || hasReceived(s, a.Validator) {
		return errSkip("session not open or share already recorded")
	}
	i, _ := slices.BinarySearchFunc(s.Received, a.Validator, func(r types.ValidatorShare, v string) int { return strings.Compare(r.Validator, v) })
	s.Received = slices.Insert(s.Received, i, types.ValidatorShare{Validator: a.Validator, Z: a.Z})
	return k.Sessions.Set(ctx, a.SessionID, s)
}

// applySessionSigned stores the signature; when every input of the batch version is
// signed it assembles the witnesses, stores the signed tx and emits withdrawal_signed.
func (k *Keeper) applySessionSigned(ctx context.Context, ss types.SessionSigned) error {
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	s, err := k.Sessions.Get(ctx, ss.SessionID)
	if err != nil {
		return err
	}
	if s.State != types.SESSION_STATE_OPEN {
		return errSkip("session not open")
	}
	s.State, s.Signature = types.SESSION_STATE_SIGNED, ss.Signature
	if err := k.Sessions.Set(ctx, s.Id, s); err != nil {
		return err
	}
	if err := k.ActiveSessions.Remove(ctx, s.Id); err != nil {
		return err
	}
	sdkCtx.EventManager().EmitEvent(sdk.NewEvent(types.EventTypeSessionSigned,
		sdk.NewAttribute(types.AttributeKeySession, hex.EncodeToString(s.Id)),
		sdk.NewAttribute(types.AttributeKeyBatch, strconv.FormatUint(s.BatchId, 10)),
		sdk.NewAttribute(types.AttributeKeyInput, strconv.FormatUint(uint64(s.InputIndex), 10))))

	b, err := k.Batches.Get(ctx, s.BatchId)
	if err != nil {
		return err
	}
	ver := &b.Txs[s.TxVersion]
	if int(s.TxVersion) != len(b.Txs)-1 || ver.SignedTx != nil {
		return nil // an older version, superseded by RBF
	}
	sigs := make([][]byte, len(ver.SessionIds))
	for i, id := range ver.SessionIds {
		other, err := k.Sessions.Get(ctx, id)
		if err != nil {
			return err
		}
		if other.State != types.SESSION_STATE_SIGNED {
			return nil // still waiting for other inputs
		}
		sigs[i] = other.Signature
	}
	signed, err := types.AttachWitnesses(ver.UnsignedTx, sigs)
	if err != nil {
		return err
	}
	ver.SignedTx = signed
	b.Status = types.BATCH_STATUS_SIGNED
	if a, err := k.Anchor.Get(ctx); err == nil {
		b.AnchorHeightAtSign = a.Height
	} else if !errors.Is(err, collections.ErrNotFound) {
		return err
	}
	if err := k.Batches.Set(ctx, b.Id, b); err != nil {
		return err
	}
	sdkCtx.EventManager().EmitEvent(sdk.NewEvent(types.EventTypeWithdrawalSigned,
		sdk.NewAttribute(types.AttributeKeyBatch, strconv.FormatUint(b.Id, 10)),
		sdk.NewAttribute(types.AttributeKeyTxid, types.TxidString(ver.Txid))))
	return nil
}

// applySessionFailed is SessionFailed + OpenRetry / SessionStalled (§9.10): the failed
// session's operators are charged (invalid share: blamed for the epoch; missing: a
// miss), and a retry with attempt + 1 opens with fresh assignments, excluding the
// missing operators for that attempt, or STALLED if too few are eligible.
func (k *Keeper) applySessionFailed(ctx context.Context, f types.SessionFailed, voters []sdk.AccAddress) error {
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	s, err := k.Sessions.Get(ctx, f.SessionID)
	if err != nil {
		return err
	}
	if s.State != types.SESSION_STATE_OPEN {
		return errSkip("session not open")
	}
	s.State = types.SESSION_STATE_FAILED
	if err := k.Sessions.Set(ctx, s.Id, s); err != nil {
		return err
	}
	if err := k.ActiveSessions.Remove(ctx, s.Id); err != nil {
		return err
	}
	reason := types.SessionFailedMissing
	if f.Blame != "" {
		reason = types.SessionFailedInvalid
		if err := k.charge(ctx, s.Epoch, f.Blame, true); err != nil {
			return err
		}
	}
	for _, m := range f.Missing {
		if err := k.charge(ctx, s.Epoch, m, false); err != nil {
			return err
		}
	}
	sdkCtx.EventManager().EmitEvent(sdk.NewEvent(types.EventTypeSessionFailed,
		sdk.NewAttribute(types.AttributeKeySession, hex.EncodeToString(s.Id)),
		sdk.NewAttribute(types.AttributeKeyBatch, strconv.FormatUint(s.BatchId, 10)),
		sdk.NewAttribute(types.AttributeKeyReason, reason),
		sdk.NewAttribute(types.AttributeKeyBlame, f.Blame),
		sdk.NewAttribute(types.AttributeKeyMissing, strings.Join(f.Missing, ","))))

	b, err := k.Batches.Get(ctx, s.BatchId)
	if err != nil {
		return err
	}
	ver := &b.Txs[s.TxVersion]
	retry := types.SigningSession{
		Epoch: s.Epoch, Attempt: s.Attempt + 1, Msg: s.Msg, Tweak: s.Tweak, BatchId: s.BatchId,
		InputIndex: s.InputIndex, TxVersion: s.TxVersion, Excluded: slices.Clone(f.Missing),
	}
	slices.Sort(retry.Excluded)
	id, err := k.openSession(ctx, retry, ver.Txid, voters)
	if err != nil {
		return err
	}
	ver.SessionIds[s.InputIndex] = id
	return k.Batches.Set(ctx, b.Id, b)
}

// charge records an invalid share or a miss and emits signer_flagged once an operator
// passes 3 invalid shares or misses more than 20% of its assigned sessions (§9.10).
func (k *Keeper) charge(ctx context.Context, epoch uint64, operator string, invalid bool) error {
	op, err := sdk.AccAddressFromBech32(operator)
	if err != nil {
		return err
	}
	st, err := k.stats(ctx, epoch, op)
	if err != nil {
		return err
	}
	if invalid {
		st.Invalid++
	} else {
		st.Missed++
	}
	if !st.Flagged && (st.Invalid > 3 || st.Missed*5 > st.Assigned) {
		st.Flagged = true
		sdk.UnwrapSDKContext(ctx).EventManager().EmitEvent(sdk.NewEvent(types.EventTypeSignerFlagged,
			sdk.NewAttribute(types.AttributeKeyOperator, operator),
			sdk.NewAttribute(types.AttributeKeyEpoch, strconv.FormatUint(epoch, 10)),
			sdk.NewAttribute(types.AttributeKeyInvalid, strconv.FormatUint(st.Invalid, 10)),
			sdk.NewAttribute(types.AttributeKeyMissed, strconv.FormatUint(st.Missed, 10)),
			sdk.NewAttribute(types.AttributeKeyAssigned, strconv.FormatUint(st.Assigned, 10))))
	}
	return k.SignerStats.Set(ctx, collections.Join(epoch, op), st)
}

// recheckStalled tries to open every STALLED session (§9.10: re-checked every block).
func (k *Keeper) recheckStalled(ctx context.Context, voters []sdk.AccAddress) error {
	var ids [][]byte
	if err := k.ActiveSessions.Walk(ctx, nil, func(id []byte) (bool, error) {
		ids = append(ids, id)
		return false, nil
	}); err != nil {
		return err
	}
	for _, id := range ids {
		s, err := k.Sessions.Get(ctx, id)
		if err != nil {
			return err
		}
		if s.State != types.SESSION_STATE_STALLED {
			continue
		}
		// The exclusions applied to the attempt that stalled; a later open may use
		// every eligible operator again.
		s.Excluded = nil
		cc, write := sdk.UnwrapSDKContext(ctx).CacheContext()
		if err := k.tryAssign(cc, &s, voters); err != nil {
			return err
		}
		if s.State != types.SESSION_STATE_OPEN {
			continue // still stalled: drop the repeated event
		}
		if err := k.Sessions.Set(cc, id, s); err != nil {
			return err
		}
		write()
	}
	return nil
}

// GetActiveSessions returns the OPEN and STALLED sessions in id order.
func (k *Keeper) GetActiveSessions(ctx context.Context) ([]types.SigningSession, error) {
	var out []types.SigningSession
	err := k.ActiveSessions.Walk(ctx, nil, func(id []byte) (bool, error) {
		s, err := k.Sessions.Get(ctx, id)
		if err != nil {
			return true, err
		}
		out = append(out, s)
		return false, nil
	})
	return out, err
}
