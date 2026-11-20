// FLAG FOR AUDIT: DKG participant (design §9.3).

// Package dkg is the operator side of the custody DKG (spec M4b-T2): from the
// committed DKG state it produces this operator's MsgDKGCommit, MsgDKGDeal and
// MsgDKGComplaint (signed by its custody hot key), and after finalization derives its
// share, checks it against its verifying share and writes epoch_<n>/shares.json. It is
// not state-machine code: it holds the polynomial and the encryption key and uses
// crypto/rand.
package dkg

import (
	"bytes"
	"errors"
	"fmt"
	"slices"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/logicvoid0x/keystone/crypto/frost"
	"github.com/logicvoid0x/keystone/crypto/frost/secret"
	custodytypes "github.com/logicvoid0x/keystone/x/custody/types"
)

// Window is the DKG phase length in blocks (x/custody DKGWindow).
const Window = 20

// ResendAfter is how many blocks a message may stay off-chain before it is sent again.
const ResendAfter = 4

// View is the committed state the participant acts on.
type View struct {
	ChainID string
	Height  int64
	// State is the latest DKG run (nil if none); Dealers and Complaints its messages.
	State      *custodytypes.DKGState
	Dealers    []custodytypes.DKGDealer
	Complaints []custodytypes.DKGComplaint
	// Keys are the registered custody keys.
	Keys []custodytypes.CustodyKey
	// Epoch is the key epoch the run produced (nil until finalized), and
	// VerifyingShare this operator's Y_i in it.
	Epoch          *custodytypes.KeyEpoch
	VerifyingShare []byte
}

// Store keeps the participant's secrets.
type Store interface {
	// LoadPolynomial / SavePolynomial persist the dealer polynomial of a run, so a
	// restart re-sends the same commitment.
	LoadPolynomial(epoch uint64, attempt uint32) ([]frost.Scalar, bool, error)
	SavePolynomial(epoch uint64, attempt uint32, poly []frost.Scalar) error
	DeletePolynomials(epoch uint64) error
	// HasShares / WriteShares are epoch_<n>/shares.json.
	HasShares(epoch uint64) (bool, error)
	WriteShares(s secret.EpochShares) error
}

// Participant is one operator's DKG role.
type Participant struct {
	Operator sdk.AccAddress
	HotKey   string
	EncKey   frost.Scalar
	Store    Store

	// Test faults: CorruptFor sends recipient index CorruptFor a wrong share;
	// AccuseFalsely files a complaint against that dealer although its share is good.
	CorruptFor    uint32
	AccuseFalsely string

	sent map[string]int64
}

func (p *Participant) due(key string, h int64) bool {
	if p.sent == nil {
		p.sent = map[string]int64{}
	}
	last, ok := p.sent[key]
	if ok && h-last < ResendAfter {
		return false
	}
	p.sent[key] = h
	return true
}

func (v View) key(op string) (custodytypes.CustodyKey, bool) {
	i := slices.IndexFunc(v.Keys, func(k custodytypes.CustodyKey) bool { return k.Operator == op })
	if i < 0 {
		return custodytypes.CustodyKey{}, false
	}
	return v.Keys[i], true
}

func (v View) dealer(op string) custodytypes.DKGDealer {
	i := slices.IndexFunc(v.Dealers, func(d custodytypes.DKGDealer) bool { return d.Operator == op })
	if i < 0 {
		return custodytypes.DKGDealer{Operator: op}
	}
	return v.Dealers[i]
}

func phaseAt(st *custodytypes.DKGState, h int64) custodytypes.DKGPhase {
	switch d := h - st.StartHeight; {
	case d < 0:
		return custodytypes.DKG_PHASE_UNSPECIFIED
	case d < Window:
		return custodytypes.DKG_PHASE_COMMIT
	case d < 2*Window:
		return custodytypes.DKG_PHASE_DEAL
	case d < 3*Window:
		return custodytypes.DKG_PHASE_COMPLAIN
	default:
		return custodytypes.DKG_PHASE_DONE
	}
}

// Step returns the messages to submit for the next block (height v.Height + 1) and,
// once the run is finalized, writes this operator's share.
func (p *Participant) Step(v View) ([]sdk.Msg, error) {
	st := v.State
	if st == nil {
		return nil, nil
	}
	me := slices.IndexFunc(st.Participants, func(q custodytypes.DKGParticipant) bool { return q.Operator == p.Operator.String() })
	if me < 0 {
		return nil, nil
	}
	idx := st.Participants[me].Index
	switch st.Phase {
	case custodytypes.DKG_PHASE_DONE:
		return nil, p.deriveShare(v, idx)
	case custodytypes.DKG_PHASE_FAILED:
		return nil, nil
	}
	next := v.Height + 1
	switch phaseAt(st, next) {
	case custodytypes.DKG_PHASE_COMMIT:
		if v.dealer(p.Operator.String()).Commitment != nil || !p.due(fmt.Sprintf("commit/%d/%d", st.Epoch, st.Attempt), next) {
			return nil, nil
		}
		m, err := p.commit(v, idx)
		if err != nil {
			return nil, err
		}
		return []sdk.Msg{m}, nil
	case custodytypes.DKG_PHASE_DEAL:
		mine := v.dealer(p.Operator.String())
		if mine.Commitment == nil || mine.Dealing != nil || !p.due(fmt.Sprintf("deal/%d/%d", st.Epoch, st.Attempt), next) {
			return nil, nil
		}
		m, err := p.deal(v, idx)
		if err != nil {
			return nil, err
		}
		return []sdk.Msg{m}, nil
	case custodytypes.DKG_PHASE_COMPLAIN:
		return p.complaints(v, idx, next)
	}
	return nil, nil
}

func (p *Participant) polynomial(st *custodytypes.DKGState) ([]frost.Scalar, error) {
	poly, ok, err := p.Store.LoadPolynomial(st.Epoch, st.Attempt)
	if err != nil || ok {
		return poly, err
	}
	if poly, err = secret.NewPolynomial(nil, st.T); err != nil {
		return nil, err
	}
	// Persisted before the commitment leaves this process.
	return poly, p.Store.SavePolynomial(st.Epoch, st.Attempt, poly)
}

func (p *Participant) commit(v View, idx uint32) (sdk.Msg, error) {
	st := v.State
	poly, err := p.polynomial(st)
	if err != nil {
		return nil, err
	}
	if len(poly) != int(st.T) {
		return nil, fmt.Errorf("stored polynomial has %d coefficients, want %d", len(poly), st.T)
	}
	m := &custodytypes.MsgDKGCommit{HotKey: p.HotKey, Epoch: st.Epoch}
	for _, c := range secret.Commitments(poly) {
		b, err := frost.SerializePoint(c)
		if err != nil {
			return nil, err
		}
		m.Commitments = append(m.Commitments, b[:])
	}
	if m.Proof, err = secret.ProofOfKnowledge(nil, frost.Taproot, frost.PoKContext(v.ChainID, st.Epoch), idx, poly[0]); err != nil {
		return nil, err
	}
	return m, nil
}

func (p *Participant) deal(v View, idx uint32) (sdk.Msg, error) {
	st := v.State
	poly, err := p.polynomial(st)
	if err != nil {
		return nil, err
	}
	m := &custodytypes.MsgDKGDeal{HotKey: p.HotKey, Epoch: st.Epoch}
	for _, q := range st.Participants {
		if q.Index == idx {
			continue
		}
		ck, ok := v.key(q.Operator)
		if !ok {
			return nil, fmt.Errorf("no custody key for %s", q.Operator)
		}
		pk, err := frost.ParsePoint(ck.EncPubkey)
		if err != nil {
			return nil, err
		}
		ad := frost.ShareAD(st.Epoch, idx, q.Index)
		key, err := frost.ShareKey(frost.ECDH(&p.EncKey, pk), ad)
		if err != nil {
			return nil, err
		}
		share := secret.EvalPolynomial(poly, q.Index)
		if q.Index == p.CorruptFor {
			var one frost.Scalar
			one.SetInt(1)
			share.Add(&one)
		}
		ct, err := secret.SealShare(nil, key, ad, share)
		if err != nil {
			return nil, err
		}
		m.Ciphertexts = append(m.Ciphertexts, custodytypes.DKGCiphertext{Recipient: q.Index, Ciphertext: ct})
	}
	return m, nil
}

// received decrypts and checks dealer d's share for us; ok is false for a bad share.
func (p *Participant) received(v View, d custodytypes.DKGDealer, dIdx, idx uint32) (frost.Scalar, bool, error) {
	ck, ok := v.key(d.Operator)
	if !ok {
		return frost.Scalar{}, false, fmt.Errorf("no custody key for %s", d.Operator)
	}
	pk, err := frost.ParsePoint(ck.EncPubkey)
	if err != nil {
		return frost.Scalar{}, false, err
	}
	i := slices.IndexFunc(d.Dealing.Ciphertexts, func(c custodytypes.DKGCiphertext) bool { return c.Recipient == idx })
	if i < 0 {
		return frost.Scalar{}, false, nil
	}
	ad := frost.ShareAD(v.State.Epoch, dIdx, idx)
	key, err := frost.ShareKey(frost.ECDH(&p.EncKey, pk), ad)
	if err != nil {
		return frost.Scalar{}, false, err
	}
	share, err := frost.OpenShare(key, ad, d.Dealing.Ciphertexts[i].Ciphertext)
	if err != nil {
		return frost.Scalar{}, false, nil
	}
	cs, err := points(d.Commitment.Commitments)
	if err != nil {
		return frost.Scalar{}, false, err
	}
	return share, frost.EqualPoints(frost.MulBase(&share), frost.EvalCommitment(cs, idx)), nil
}

func points(bzs [][]byte) ([]frost.Point, error) {
	out := make([]frost.Point, len(bzs))
	for i, b := range bzs {
		pt, err := frost.ParsePoint(b)
		if err != nil {
			return nil, err
		}
		out[i] = pt
	}
	return out, nil
}

func (p *Participant) complaints(v View, idx uint32, next int64) ([]sdk.Msg, error) {
	var out []sdk.Msg
	for _, q := range v.State.Participants {
		if q.Index == idx {
			continue
		}
		d := v.dealer(q.Operator)
		if d.Commitment == nil || d.Dealing == nil {
			continue
		}
		filed := slices.ContainsFunc(v.Complaints, func(c custodytypes.DKGComplaint) bool {
			return c.Complainer == p.Operator.String() && c.Dealer == q.Operator
		})
		if filed {
			continue
		}
		_, good, err := p.received(v, d, q.Index, idx)
		if err != nil {
			return nil, err
		}
		if good && p.AccuseFalsely != q.Operator {
			continue
		}
		if !p.due(fmt.Sprintf("complain/%d/%d/%s", v.State.Epoch, v.State.Attempt, q.Operator), next) {
			continue
		}
		ck, _ := v.key(q.Operator)
		pk, err := frost.ParsePoint(ck.EncPubkey)
		if err != nil {
			return nil, err
		}
		s, proof, err := secret.ProveDLEQ(nil, p.EncKey, pk)
		if err != nil {
			return nil, err
		}
		sb, err := frost.SerializePoint(s)
		if err != nil {
			return nil, err
		}
		out = append(out, &custodytypes.MsgDKGComplaint{HotKey: p.HotKey, Epoch: v.State.Epoch, Dealer: q.Operator, EcdhSecret: sb[:], DleqProof: proof})
	}
	return out, nil
}

// deriveShare is §9.3 step 5: s_i = Σ_{v∈Q} f_v(i) over the qualified dealers, negated
// if the group key was normalized, checked against Y_i, then written to shares.json.
func (p *Participant) deriveShare(v View, idx uint32) error {
	st := v.State
	if v.Epoch == nil || v.Epoch.Number != st.Epoch {
		return nil
	}
	if has, err := p.Store.HasShares(st.Epoch); err != nil || has {
		return err
	}
	poly, ok, err := p.Store.LoadPolynomial(st.Epoch, st.Attempt)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("epoch %d: dealer polynomial lost; this operator cannot derive its share", st.Epoch)
	}
	var s frost.Scalar
	var y0 frost.Point
	for _, q := range st.Participants {
		if _, bad := slices.BinarySearch(st.Disqualified, q.Operator); bad {
			continue
		}
		d := v.dealer(q.Operator)
		if d.Commitment == nil || d.Dealing == nil {
			continue
		}
		cs, err := points(d.Commitment.Commitments)
		if err != nil {
			return err
		}
		y0 = frost.AddPoints(y0, cs[0])
		if q.Index == idx {
			own := secret.EvalPolynomial(poly, idx)
			s.Add(&own)
			continue
		}
		share, good, err := p.received(v, d, q.Index, idx)
		if err != nil {
			return err
		}
		if !good {
			return fmt.Errorf("epoch %d: qualified dealer %s sent a share that does not check", st.Epoch, q.Operator)
		}
		s.Add(&share)
	}
	gk, err := frost.SerializePoint(y0)
	if err != nil {
		return err
	}
	if gk[0] == 0x03 {
		s.Negate()
	}
	yi, err := frost.SerializePoint(frost.MulBase(&s))
	if err != nil {
		return err
	}
	if !bytes.Equal(yi[:], v.VerifyingShare) {
		return errors.New("derived share does not match the chain's verifying share")
	}
	sb := s.Bytes()
	if err := p.Store.WriteShares(secret.EpochShares{
		Epoch: st.Epoch, GroupKey: v.Epoch.GroupKey, N: v.Epoch.N, T: v.Epoch.T,
		Shares: []secret.Share{{Index: idx, Secret: sb[:], VerifyingShare: yi[:]}},
	}); err != nil {
		return err
	}
	return p.Store.DeletePolynomials(st.Epoch)
}
