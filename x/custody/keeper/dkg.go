// FLAG FOR AUDIT: Pedersen DKG with proofs of knowledge and verifiable complaints
// (design §9.3, §13 A9–A10).

package keeper

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"slices"
	"strconv"
	"strings"

	"cosmossdk.io/collections"
	errorsmod "cosmossdk.io/errors"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/OWNER/keystone/crypto/frost"
	"github.com/OWNER/keystone/x/custody/types"
)

// DKGWindow is the length of each DKG phase in blocks (design §9.3).
const DKGWindow = 20

// DKG message kinds in the 0x14 store (key: epoch, kind, then the dealer or the
// complainer ‖ dealer).
const (
	dkgMsgCommit    = 1
	dkgMsgDeal      = 2
	dkgMsgComplaint = 3
)

// RegisterCustodyKey is MsgRegisterCustodyKey: an x/poa operator sets its DKG
// encryption key and hot key. A hot key serves one operator only.
func (k *Keeper) RegisterCustodyKey(ctx context.Context, msg *types.MsgRegisterCustodyKey) error {
	if err := msg.ValidateBasic(); err != nil {
		return err
	}
	op := sdk.MustAccAddressFromBech32(msg.Operator)
	if _, err := k.vals.GetOperator(ctx, op); err != nil {
		return err
	}
	return k.setCustodyKey(ctx, types.CustodyKey{Operator: msg.Operator, EncPubkey: msg.EncPubkey, HotKey: msg.HotKey})
}

func (k *Keeper) setCustodyKey(ctx context.Context, ck types.CustodyKey) error {
	op := sdk.MustAccAddressFromBech32(ck.Operator)
	hot := sdk.MustAccAddressFromBech32(ck.HotKey)
	if owner, err := k.HotKeys.Get(ctx, hot); err == nil && !bytes.Equal(owner, op) {
		return errorsmod.Wrapf(types.ErrInvalidAddress, "hot key %s belongs to another operator", ck.HotKey)
	} else if err != nil && !errors.Is(err, collections.ErrNotFound) {
		return err
	}
	if prev, err := k.CustodyKeys.Get(ctx, op); err == nil && prev.HotKey != ck.HotKey {
		if err := k.HotKeys.Remove(ctx, sdk.MustAccAddressFromBech32(prev.HotKey)); err != nil {
			return err
		}
	} else if err != nil && !errors.Is(err, collections.ErrNotFound) {
		return err
	}
	if err := k.CustodyKeys.Set(ctx, op, ck); err != nil {
		return err
	}
	return k.HotKeys.Set(ctx, hot, op)
}

// operatorOfHotKey maps a DKG message signer to its operator.
func (k *Keeper) operatorOfHotKey(ctx context.Context, hot string) (sdk.AccAddress, error) {
	addr, err := sdk.AccAddressFromBech32(hot)
	if err != nil {
		return nil, errorsmod.Wrapf(types.ErrInvalidAddress, "hot key %q: %v", hot, err)
	}
	op, err := k.HotKeys.Get(ctx, addr)
	if errors.Is(err, collections.ErrNotFound) {
		return nil, errorsmod.Wrapf(types.ErrNotFound, "no operator has hot key %s", hot)
	}
	return op, err
}

// dkgWindow reports which phase window height falls in for a run started at start.
func dkgWindow(start, height int64) types.DKGPhase {
	switch d := height - start; {
	case d < 0:
		return types.DKG_PHASE_UNSPECIFIED
	case d < DKGWindow:
		return types.DKG_PHASE_COMMIT
	case d < 2*DKGWindow:
		return types.DKG_PHASE_DEAL
	case d < 3*DKGWindow:
		return types.DKG_PHASE_COMPLAIN
	default:
		return types.DKG_PHASE_DONE
	}
}

// participant finds operator op in st.
func participant(st types.DKGState, op sdk.AccAddress) (types.DKGParticipant, bool) {
	for _, p := range st.Participants {
		if p.Operator == op.String() {
			return p, true
		}
	}
	return types.DKGParticipant{}, false
}

// dkgFor loads epoch's run and checks that the signer's operator is a participant and
// that the current height is in phase.
func (k *Keeper) dkgFor(ctx context.Context, epoch uint64, hot string, phase types.DKGPhase) (types.DKGState, types.DKGParticipant, error) {
	op, err := k.operatorOfHotKey(ctx, hot)
	if err != nil {
		return types.DKGState{}, types.DKGParticipant{}, err
	}
	st, err := k.DKGStates.Get(ctx, epoch)
	if errors.Is(err, collections.ErrNotFound) {
		return st, types.DKGParticipant{}, errorsmod.Wrapf(types.ErrNotFound, "no DKG for epoch %d", epoch)
	} else if err != nil {
		return st, types.DKGParticipant{}, err
	}
	if st.Phase == types.DKG_PHASE_DONE || st.Phase == types.DKG_PHASE_FAILED {
		return st, types.DKGParticipant{}, errorsmod.Wrapf(types.ErrDKG, "DKG for epoch %d is over", epoch)
	}
	if w := dkgWindow(st.StartHeight, sdk.UnwrapSDKContext(ctx).BlockHeight()); w != phase {
		return st, types.DKGParticipant{}, errorsmod.Wrapf(types.ErrDKG, "DKG epoch %d is in %s, not %s", epoch, w, phase)
	}
	p, ok := participant(st, op)
	if !ok {
		return st, p, errorsmod.Wrapf(types.ErrDKG, "%s is not a participant", op)
	}
	return st, p, nil
}

func dkgKey(epoch uint64, kind uint32, key []byte) collections.Triple[uint64, uint32, []byte] {
	return collections.Join3(epoch, kind, key)
}

// DKGCommit is MsgDKGCommit (§9.3 step 1): t coefficient commitments and a proof of
// knowledge of a_0 bound to (chain id, epoch, dealer index). A bad proof is rejected.
func (k *Keeper) DKGCommit(ctx context.Context, msg *types.MsgDKGCommit) error {
	st, p, err := k.dkgFor(ctx, msg.Epoch, msg.HotKey, types.DKG_PHASE_COMMIT)
	if err != nil {
		return err
	}
	op := sdk.MustAccAddressFromBech32(p.Operator)
	key := dkgKey(msg.Epoch, dkgMsgCommit, op)
	if has, err := k.DKGMessages.Has(ctx, key); err != nil || has {
		return orErr(err, errorsmod.Wrapf(types.ErrDKG, "%s already committed", op))
	}
	if len(msg.Commitments) != int(st.T) {
		return errorsmod.Wrapf(types.ErrDKG, "%d commitments, want t = %d", len(msg.Commitments), st.T)
	}
	cs, err := parsePoints(msg.Commitments)
	if err != nil {
		return errorsmod.Wrap(types.ErrDKG, err.Error())
	}
	pokCtx := frost.PoKContext(sdk.UnwrapSDKContext(ctx).ChainID(), msg.Epoch)
	if !frost.VerifyProofOfKnowledge(frost.Taproot, pokCtx, p.Index, cs[0], msg.Proof) {
		return errorsmod.Wrap(types.ErrDKG, "invalid proof of knowledge")
	}
	bz := mustMarshal(&types.DKGCommitment{Commitments: msg.Commitments, Proof: msg.Proof})
	if err := k.DKGMessages.Set(ctx, key, bz); err != nil {
		return err
	}
	emitDKG(ctx, types.EventTypeDKGCommit, msg.Epoch, sdk.NewAttribute(types.AttributeKeyOperator, p.Operator))
	return nil
}

func parsePoints(bzs [][]byte) ([]frost.Point, error) {
	out := make([]frost.Point, len(bzs))
	for i, b := range bzs {
		p, err := frost.ParsePoint(b)
		if err != nil {
			return nil, err
		}
		out[i] = p
	}
	return out, nil
}

func orErr(err, other error) error {
	if err != nil {
		return err
	}
	return other
}

// shareCiphertextLen is nonce ‖ 32-byte share ‖ 16-byte GCM tag.
const shareCiphertextLen = frost.ShareNonceLen + 32 + 16

// DKGDeal is MsgDKGDeal (§9.3 step 2): after committing, one ciphertext for every
// other participant, by recipient index.
func (k *Keeper) DKGDeal(ctx context.Context, msg *types.MsgDKGDeal) error {
	st, p, err := k.dkgFor(ctx, msg.Epoch, msg.HotKey, types.DKG_PHASE_DEAL)
	if err != nil {
		return err
	}
	op := sdk.MustAccAddressFromBech32(p.Operator)
	if has, err := k.DKGMessages.Has(ctx, dkgKey(msg.Epoch, dkgMsgCommit, op)); err != nil || !has {
		return orErr(err, errorsmod.Wrapf(types.ErrDKG, "%s did not commit", op))
	}
	key := dkgKey(msg.Epoch, dkgMsgDeal, op)
	if has, err := k.DKGMessages.Has(ctx, key); err != nil || has {
		return orErr(err, errorsmod.Wrapf(types.ErrDKG, "%s already dealt", op))
	}
	var want []uint32
	for _, q := range st.Participants {
		if q.Index != p.Index {
			want = append(want, q.Index)
		}
	}
	if len(msg.Ciphertexts) != len(want) {
		return errorsmod.Wrapf(types.ErrDKG, "%d ciphertexts, want %d", len(msg.Ciphertexts), len(want))
	}
	for i, c := range msg.Ciphertexts {
		if c.Recipient != want[i] || len(c.Ciphertext) != shareCiphertextLen {
			return errorsmod.Wrapf(types.ErrDKG, "ciphertext %d: recipient %d, %d bytes", i, c.Recipient, len(c.Ciphertext))
		}
	}
	if err := k.DKGMessages.Set(ctx, key, mustMarshal(&types.DKGDealing{Ciphertexts: msg.Ciphertexts})); err != nil {
		return err
	}
	emitDKG(ctx, types.EventTypeDKGDeal, msg.Epoch, sdk.NewAttribute(types.AttributeKeyOperator, p.Operator))
	return nil
}

func (k *Keeper) dkgCommitment(ctx context.Context, epoch uint64, op sdk.AccAddress) (types.DKGCommitment, bool, error) {
	var c types.DKGCommitment
	bz, err := k.DKGMessages.Get(ctx, dkgKey(epoch, dkgMsgCommit, op))
	if errors.Is(err, collections.ErrNotFound) {
		return c, false, nil
	} else if err != nil {
		return c, false, err
	}
	return c, true, c.Unmarshal(bz)
}

func (k *Keeper) dkgDealing(ctx context.Context, epoch uint64, op sdk.AccAddress) (types.DKGDealing, bool, error) {
	var d types.DKGDealing
	bz, err := k.DKGMessages.Get(ctx, dkgKey(epoch, dkgMsgDeal, op))
	if errors.Is(err, collections.ErrNotFound) {
		return d, false, nil
	} else if err != nil {
		return d, false, err
	}
	return d, true, d.Unmarshal(bz)
}

// DKGComplaint is MsgDKGComplaint (§9.3 step 3). The DLEQ proof must show that
// ecdh_secret = x·D for the complainer's key X = x·G and the dealer's key D (else the
// message is rejected). The chain then decrypts the complainer's share and checks it
// against the dealer's commitments: if decryption fails or the check fails the dealer
// is disqualified, otherwise the complainer is (a false complaint).
func (k *Keeper) DKGComplaint(ctx context.Context, msg *types.MsgDKGComplaint) (bool, error) {
	st, p, err := k.dkgFor(ctx, msg.Epoch, msg.HotKey, types.DKG_PHASE_COMPLAIN)
	if err != nil {
		return false, err
	}
	complainer := sdk.MustAccAddressFromBech32(p.Operator)
	dealer, err := sdk.AccAddressFromBech32(msg.Dealer)
	if err != nil {
		return false, errorsmod.Wrapf(types.ErrInvalidAddress, "dealer %q", msg.Dealer)
	}
	dp, ok := participant(st, dealer)
	if !ok || dealer.Equals(complainer) {
		return false, errorsmod.Wrapf(types.ErrDKG, "%s is not another participant", dealer)
	}
	key := dkgKey(msg.Epoch, dkgMsgComplaint, append(slices.Clone(complainer), dealer...))
	if has, err := k.DKGMessages.Has(ctx, key); err != nil || has {
		return false, orErr(err, errorsmod.Wrap(types.ErrDKG, "complaint already filed"))
	}
	com, okc, err := k.dkgCommitment(ctx, msg.Epoch, dealer)
	if err != nil {
		return false, err
	}
	deal, okd, err := k.dkgDealing(ctx, msg.Epoch, dealer)
	if err != nil {
		return false, err
	}
	if !okc || !okd {
		return false, errorsmod.Wrapf(types.ErrDKG, "%s did not deal", dealer)
	}
	ck, err := k.CustodyKeys.Get(ctx, complainer)
	if err != nil {
		return false, err
	}
	dk, err := k.CustodyKeys.Get(ctx, dealer)
	if err != nil {
		return false, err
	}
	x, err := frost.ParsePoint(ck.EncPubkey)
	if err != nil {
		return false, err
	}
	d, err := frost.ParsePoint(dk.EncPubkey)
	if err != nil {
		return false, err
	}
	s, err := frost.ParsePoint(msg.EcdhSecret)
	if err != nil {
		return false, errorsmod.Wrap(types.ErrDKG, "ecdh secret is not a point")
	}
	if !frost.VerifyDLEQ(x, d, s, msg.DleqProof) {
		return false, errorsmod.Wrap(types.ErrDKG, "invalid DLEQ proof")
	}

	upheld := true
	i := slices.IndexFunc(deal.Ciphertexts, func(c types.DKGCiphertext) bool { return c.Recipient == p.Index })
	if i >= 0 {
		ad := frost.ShareAD(msg.Epoch, dp.Index, p.Index)
		if sk, err := frost.ShareKey(s, ad); err == nil {
			if share, err := frost.OpenShare(sk, ad, deal.Ciphertexts[i].Ciphertext); err == nil {
				cs, err := parsePoints(com.Commitments)
				if err != nil {
					return false, err
				}
				upheld = !frost.EqualPoints(frost.MulBase(&share), frost.EvalCommitment(cs, p.Index))
			}
		}
	}
	guilty := complainer.String()
	if upheld {
		guilty = dealer.String()
	}
	if j, found := slices.BinarySearch(st.Disqualified, guilty); !found {
		st.Disqualified = slices.Insert(st.Disqualified, j, guilty)
	}
	if err := k.DKGStates.Set(ctx, msg.Epoch, st); err != nil {
		return false, err
	}
	rec := types.DKGComplaint{Complainer: p.Operator, Dealer: dealer.String(), EcdhSecret: msg.EcdhSecret, DleqProof: msg.DleqProof, Upheld: upheld}
	if err := k.DKGMessages.Set(ctx, key, mustMarshal(&rec)); err != nil {
		return false, err
	}
	emitDKG(ctx, types.EventTypeDKGComplaint, msg.Epoch,
		sdk.NewAttribute(types.AttributeKeyOperator, p.Operator),
		sdk.NewAttribute(types.AttributeKeyDealer, dealer.String()),
		sdk.NewAttribute(types.AttributeKeyUpheld, strconv.FormatBool(upheld)))
	return upheld, nil
}

func emitDKG(ctx context.Context, typ string, epoch uint64, attrs ...sdk.Attribute) {
	sdk.UnwrapSDKContext(ctx).EventManager().EmitEvent(sdk.NewEvent(typ,
		append([]sdk.Attribute{sdk.NewAttribute(types.AttributeKeyEpoch, strconv.FormatUint(epoch, 10))}, attrs...)...))
}

// startDKG opens a DKG for epoch among the active operators that registered custody
// keys, starting at start. Share index = rank by operator address bytes, from 1.
func (k *Keeper) startDKG(ctx context.Context, epoch uint64, attempt uint32, start int64) (bool, error) {
	ops, err := k.vals.ActiveValidators(ctx)
	if err != nil {
		return false, err
	}
	var addrs []sdk.AccAddress
	for _, op := range ops {
		a, err := sdk.AccAddressFromBech32(op.OperatorAddress)
		if err != nil {
			return false, err
		}
		if has, err := k.CustodyKeys.Has(ctx, a); err != nil {
			return false, err
		} else if has && op.Power > 0 {
			addrs = append(addrs, a)
		}
	}
	if len(addrs) == 0 {
		return false, nil
	}
	slices.SortFunc(addrs, func(a, b sdk.AccAddress) int { return bytes.Compare(a, b) })
	st := types.DKGState{Epoch: epoch, Attempt: attempt, StartHeight: start, Phase: types.DKG_PHASE_COMMIT, T: uint32(2*len(addrs)/3 + 1)}
	for i, a := range addrs {
		st.Participants = append(st.Participants, types.DKGParticipant{Operator: a.String(), Index: uint32(i + 1)})
	}
	if err := k.DKGStates.Set(ctx, epoch, st); err != nil {
		return false, err
	}
	emitDKG(ctx, types.EventTypeDKGPhase, epoch,
		sdk.NewAttribute(types.AttributeKeyPhase, types.DKG_PHASE_COMMIT.String()),
		sdk.NewAttribute(types.AttributeKeyAttempt, strconv.FormatUint(uint64(attempt), 10)),
		sdk.NewAttribute(types.AttributeKeyHeight, strconv.FormatInt(start, 10)))
	return true, nil
}

// EndBlock drives the DKG (§9.3): with no epoch at all it opens the epoch-1 run for the
// next height; it records phase changes; in the last block of the complaint window it
// finalizes. Only store errors are returned.
func (k *Keeper) EndBlock(ctx context.Context) error {
	h := sdk.UnwrapSDKContext(ctx).BlockHeight()
	var cur *types.DKGState
	if err := k.DKGStates.Walk(ctx, nil, func(_ uint64, st types.DKGState) (bool, error) {
		if st.Phase != types.DKG_PHASE_DONE && st.Phase != types.DKG_PHASE_FAILED {
			s := st
			cur = &s
			return true, nil
		}
		return false, nil
	}); err != nil {
		return err
	}
	if cur == nil {
		n, err := k.ActiveEpoch.Get(ctx)
		if err != nil {
			return err
		}
		has, err := k.DKGStates.Has(ctx, 1)
		if err != nil || n != 0 || has {
			return err
		}
		_, err = k.startDKG(ctx, 1, 0, h+1)
		return err
	}
	st := *cur
	if w := dkgWindow(st.StartHeight, h+1); w != st.Phase && w != types.DKG_PHASE_DONE {
		st.Phase = w
		if err := k.DKGStates.Set(ctx, st.Epoch, st); err != nil {
			return err
		}
		emitDKG(ctx, types.EventTypeDKGPhase, st.Epoch, sdk.NewAttribute(types.AttributeKeyPhase, w.String()),
			sdk.NewAttribute(types.AttributeKeyHeight, strconv.FormatInt(h+1, 10)))
	}
	if h == st.StartHeight+3*DKGWindow-1 {
		return k.finalizeDKG(ctx, st)
	}
	return nil
}

// qualified are the participants that committed and dealt and were not disqualified.
func (k *Keeper) qualified(ctx context.Context, st types.DKGState) ([]types.DKGParticipant, [][]frost.Point, error) {
	var q []types.DKGParticipant
	var cs [][]frost.Point
	for _, p := range st.Participants {
		if _, bad := slices.BinarySearch(st.Disqualified, p.Operator); bad {
			continue
		}
		op := sdk.MustAccAddressFromBech32(p.Operator)
		com, ok, err := k.dkgCommitment(ctx, st.Epoch, op)
		if err != nil {
			return nil, nil, err
		}
		if _, okd, err := k.dkgDealing(ctx, st.Epoch, op); err != nil {
			return nil, nil, err
		} else if !ok || !okd {
			continue
		}
		pts, err := parsePoints(com.Commitments)
		if err != nil {
			return nil, nil, err
		}
		q = append(q, p)
		cs = append(cs, pts)
	}
	return q, cs, nil
}

// finalizeDKG is §9.3 step 4: with qualified dealers Q, Y = Σ C_{v,0}, C_j = Σ C_{v,j},
// and Y_i = Σ_j i^j·C_j for every participant. Y is normalized to even y by negating
// every C_j (operators negate their shares the same way). With fewer than 2/3 of the
// participants qualified the run restarts once, then fails with dkg_failed.
func (k *Keeper) finalizeDKG(ctx context.Context, st types.DKGState) error {
	h := sdk.UnwrapSDKContext(ctx).BlockHeight()
	q, cs, err := k.qualified(ctx, st)
	if err != nil {
		return err
	}
	names := make([]string, len(q))
	for i, p := range q {
		names[i] = p.Operator
	}
	if 3*len(q) < 2*len(st.Participants) {
		if st.Attempt == 0 {
			if err := k.clearDKGMessages(ctx, st.Epoch); err != nil {
				return err
			}
			emitDKG(ctx, types.EventTypeDKGRestarted, st.Epoch, sdk.NewAttribute(types.AttributeKeyQualified, strings.Join(names, ",")))
			_, err := k.startDKG(ctx, st.Epoch, 1, h+1)
			return err
		}
		st.Phase = types.DKG_PHASE_FAILED
		emitDKG(ctx, types.EventTypeDKGFailed, st.Epoch, sdk.NewAttribute(types.AttributeKeyQualified, strings.Join(names, ",")))
		return k.DKGStates.Set(ctx, st.Epoch, st)
	}
	agg := make([]frost.Point, st.T)
	for _, c := range cs {
		for j := range agg {
			agg[j] = frost.AddPoints(agg[j], c[j])
		}
	}
	gk, err := frost.SerializePoint(agg[0])
	if err != nil {
		return err
	}
	if gk[0] == 0x03 {
		for j := range agg {
			agg[j] = frost.NegPoint(agg[j])
		}
		if gk, err = frost.SerializePoint(agg[0]); err != nil {
			return err
		}
	}
	e := types.KeyEpoch{Number: st.Epoch, GroupKey: gk[:], N: uint32(len(st.Participants)), T: st.T, Status: types.EPOCH_STATUS_ACTIVE, StartHeight: h}
	if err := k.Epochs.Set(ctx, e.Number, e); err != nil {
		return err
	}
	for _, p := range st.Participants {
		y, err := frost.SerializePoint(frost.EvalCommitment(agg, p.Index))
		if err != nil {
			return err
		}
		if err := k.VerifyingShares.Set(ctx, collections.Join(e.Number, p.Index), y[:]); err != nil {
			return err
		}
		if err := k.ShareIndex.Set(ctx, collections.Join(e.Number, sdk.MustAccAddressFromBech32(p.Operator)), p.Index); err != nil {
			return err
		}
	}
	if err := k.ActiveEpoch.Set(ctx, e.Number); err != nil {
		return err
	}
	st.Phase = types.DKG_PHASE_DONE
	if err := k.DKGStates.Set(ctx, st.Epoch, st); err != nil {
		return err
	}
	// Vaults opened while no epoch was ACTIVE get their deposit script now.
	if k.vault != nil {
		ids, err := k.vault.OpenVaultIDs(ctx)
		if err != nil {
			return err
		}
		for _, id := range ids {
			if has, err := k.VaultScript.Has(ctx, collections.Join(id, e.Number)); err != nil {
				return err
			} else if !has {
				if _, err := k.RegisterVault(ctx, id); err != nil {
					return err
				}
			}
		}
	}
	emitDKG(ctx, types.EventTypeEpochActivated, e.Number,
		sdk.NewAttribute(types.AttributeKeyGroupKey, hex.EncodeToString(e.GroupKey)),
		sdk.NewAttribute(types.AttributeKeyQualified, strings.Join(names, ",")),
		sdk.NewAttribute(types.AttributeKeyThreshold, strconv.FormatUint(uint64(e.T), 10)))
	return nil
}

func (k *Keeper) clearDKGMessages(ctx context.Context, epoch uint64) error {
	var keys []collections.Triple[uint64, uint32, []byte]
	rng := collections.NewPrefixedTripleRange[uint64, uint32, []byte](epoch)
	if err := k.DKGMessages.Walk(ctx, rng, func(key collections.Triple[uint64, uint32, []byte], _ []byte) (bool, error) {
		keys = append(keys, key)
		return false, nil
	}); err != nil {
		return err
	}
	for _, key := range keys {
		if err := k.DKGMessages.Remove(ctx, key); err != nil {
			return err
		}
	}
	return nil
}

// DKGRun is a DKG run and its messages (queries and the DKG sidecar).
type DKGRun struct {
	State      types.DKGState
	Dealers    []types.DKGDealer
	Complaints []types.DKGComplaint
}

// GetDKG returns epoch's run.
func (k *Keeper) GetDKG(ctx context.Context, epoch uint64) (DKGRun, bool, error) {
	var run DKGRun
	st, err := k.DKGStates.Get(ctx, epoch)
	if errors.Is(err, collections.ErrNotFound) {
		return run, false, nil
	} else if err != nil {
		return run, false, err
	}
	run.State = st
	for _, p := range st.Participants {
		op := sdk.MustAccAddressFromBech32(p.Operator)
		d := types.DKGDealer{Operator: p.Operator}
		if c, ok, err := k.dkgCommitment(ctx, epoch, op); err != nil {
			return run, false, err
		} else if ok {
			d.Commitment = &c
		}
		if dl, ok, err := k.dkgDealing(ctx, epoch, op); err != nil {
			return run, false, err
		} else if ok {
			d.Dealing = &dl
		}
		run.Dealers = append(run.Dealers, d)
	}
	rng := collections.NewSuperPrefixedTripleRange[uint64, uint32, []byte](epoch, dkgMsgComplaint)
	err = k.DKGMessages.Walk(ctx, rng, func(_ collections.Triple[uint64, uint32, []byte], bz []byte) (bool, error) {
		var c types.DKGComplaint
		if err := c.Unmarshal(bz); err != nil {
			return true, err
		}
		run.Complaints = append(run.Complaints, c)
		return false, nil
	})
	return run, true, err
}
