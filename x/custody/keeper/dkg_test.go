package keeper_test

import (
	"bytes"
	"encoding/hex"
	"slices"
	"testing"

	"github.com/stretchr/testify/require"

	"cosmossdk.io/collections"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/OWNER/keystone/crypto/frost"
	"github.com/OWNER/keystone/crypto/frost/secret"
	"github.com/OWNER/keystone/sidecar/dkg"
	"github.com/OWNER/keystone/x/custody/keeper"
	"github.com/OWNER/keystone/x/custody/types"
	"github.com/OWNER/keystone/x/validators"
)

type dkgFixture struct {
	ctx   sdk.Context
	k     *keeper.Keeper
	parts []*dkg.Participant
	// absent participants send nothing.
	absent map[int]bool
	// errs counts rejected messages by error text.
	errs []string
}

// newDKG is a regtest custody with 4 operators that registered custody keys and no
// epoch: the epoch-1 DKG opens in the EndBlock of height 1.
func newDKG(t *testing.T) *dkgFixture {
	t.Helper()
	fv := &fakeVals{}
	gs := types.DefaultGenesis()
	gs.Params.BitcoinNetwork = types.NetworkRegtest
	f := &dkgFixture{absent: map[int]bool{}}
	for n := byte(1); n <= 4; n++ {
		op := sdk.AccAddress(bytes.Repeat([]byte{n}, 20))
		hot := sdk.AccAddress(bytes.Repeat([]byte{0x40 + n}, 20))
		fv.ops = append(fv.ops, validators.Validator{OperatorAddress: op.String(), ConsensusPubkey: bytes.Repeat([]byte{n}, 32), Power: 1})
		enc, err := secret.RandomScalar(nil)
		require.NoError(t, err)
		pub, err := frost.SerializePoint(frost.MulBase(&enc))
		require.NoError(t, err)
		gs.CustodyKeys = append(gs.CustodyKeys, types.CustodyKey{Operator: op.String(), EncPubkey: pub[:], HotKey: hot.String()})
		f.parts = append(f.parts, &dkg.Participant{Operator: op, HotKey: hot.String(), EncKey: enc, Store: &dkg.MemStore{}})
	}
	ctx, k := setupWithVals(t, gs, fv)
	f.ctx, f.k = ctx.WithBlockHeight(1), k
	require.NoError(t, k.EndBlock(f.ctx))
	return f
}

func (f *dkgFixture) view(t *testing.T) dkg.View {
	t.Helper()
	v := dkg.View{ChainID: f.ctx.ChainID(), Height: f.ctx.BlockHeight()}
	require.NoError(t, f.k.CustodyKeys.Walk(f.ctx, nil, func(_ sdk.AccAddress, ck types.CustodyKey) (bool, error) {
		v.Keys = append(v.Keys, ck)
		return false, nil
	}))
	run, ok, err := f.k.GetDKG(f.ctx, 1)
	require.NoError(t, err)
	if ok {
		v.State, v.Dealers, v.Complaints = &run.State, run.Dealers, run.Complaints
	}
	if e, err := f.k.Epochs.Get(f.ctx, 1); err == nil {
		v.Epoch = &e
	}
	return v
}

// block delivers every participant's messages for the next height, then runs
// EndBlock. Participants derive shares from the view of the committed state.
func (f *dkgFixture) block(t *testing.T) {
	t.Helper()
	var msgs []sdk.Msg
	for i, p := range f.parts {
		if f.absent[i] {
			continue
		}
		v := f.view(t)
		if v.State != nil && v.State.Phase == types.DKG_PHASE_DONE {
			idx, err := f.k.ShareIndex.Get(f.ctx, collections.Join(uint64(1), p.Operator))
			require.NoError(t, err)
			v.VerifyingShare, err = f.k.VerifyingShares.Get(f.ctx, collections.Join(uint64(1), idx))
			require.NoError(t, err)
		}
		ms, err := p.Step(v)
		require.NoError(t, err, "participant %d at height %d", i, f.ctx.BlockHeight())
		msgs = append(msgs, ms...)
	}
	f.ctx = f.ctx.WithBlockHeight(f.ctx.BlockHeight() + 1).WithEventManager(sdk.NewEventManager())
	srv := keeper.NewMsgServerImpl(f.k)
	for _, m := range msgs {
		var err error
		switch m := m.(type) {
		case *types.MsgDKGCommit:
			_, err = srv.DKGCommit(f.ctx, m)
		case *types.MsgDKGDeal:
			_, err = srv.DKGDeal(f.ctx, m)
		case *types.MsgDKGComplaint:
			_, err = srv.DKGComplaint(f.ctx, m)
		}
		if err != nil {
			f.errs = append(f.errs, err.Error())
		}
	}
	require.NoError(t, f.k.EndBlock(f.ctx))
}

// runToActive steps until epoch 1 is ACTIVE and every participant wrote its share.
func (f *dkgFixture) runToActive(t *testing.T, max int) types.KeyEpoch {
	t.Helper()
	for range max {
		f.block(t)
		e, ok, err := f.k.GetActiveEpoch(f.ctx)
		require.NoError(t, err)
		if !ok {
			continue
		}
		done := true
		for i, p := range f.parts {
			if f.absent[i] {
				continue
			}
			if has, _ := p.Store.HasShares(1); !has {
				done = false
			}
		}
		if done {
			return e
		}
	}
	t.Fatalf("epoch 1 not ACTIVE with shares after %d blocks (rejected: %v)", max, f.errs)
	return types.KeyEpoch{}
}

// signWith checks that the derived shares of signers (indices into parts) produce a
// valid BIP-340 signature for the group key (no tweak) and for a vault tweak.
func signWith(t *testing.T, f *dkgFixture, e types.KeyEpoch, signers ...int) {
	t.Helper()
	for _, tweak := range [][]byte{nil, frost.TapTweak(mustPoint(t, e.GroupKey), []byte("vault"))} {
		key, err := frost.NewKey(frost.Taproot, e.GroupKey, tweak)
		require.NoError(t, err)
		msg := bytes.Repeat([]byte{7}, 32)
		var cs []frost.Commitment
		nonces := map[uint32]secret.Nonces{}
		shares := map[uint32]frost.Scalar{}
		var ids []uint32
		for _, i := range signers {
			es := f.parts[i].Store.(*dkg.MemStore).Shares[1]
			sh := es.Shares[0]
			s, err := frost.ParseScalar(sh.Secret)
			require.NoError(t, err)
			n, err := secret.NewNonces(frost.Taproot, nil, s)
			require.NoError(t, err)
			d, eb, err := n.Commitment()
			require.NoError(t, err)
			c, err := frost.ParseCommitment(sh.Index, d[:], eb[:])
			require.NoError(t, err)
			cs = append(cs, c)
			nonces[sh.Index], shares[sh.Index] = n, s
			ids = append(ids, sh.Index)
		}
		slices.SortFunc(cs, func(a, b frost.Commitment) int { return int(a.ID) - int(b.ID) })
		sg, err := frost.NewSigning(key, msg, cs)
		require.NoError(t, err)
		var zs []frost.Scalar
		for _, id := range ids {
			z, err := secret.Sign(sg, id, shares[id], nonces[id])
			require.NoError(t, err)
			yb, err := f.k.VerifyingShares.Get(f.ctx, collections.Join(uint64(1), id))
			require.NoError(t, err)
			require.True(t, sg.VerifyShares([]uint32{id}, []frost.Point{mustPoint(t, yb)}, z))
			zs = append(zs, z)
		}
		require.True(t, frost.Verify(key, msg, sg.Aggregate(zs).Bytes(frost.Taproot)))
	}
}

func mustPoint(t *testing.T, b []byte) frost.Point {
	t.Helper()
	p, err := frost.ParsePoint(b)
	require.NoError(t, err)
	return p
}

func activated(ctx sdk.Context) (groupKey, qualified string) {
	g, _ := evAttr(ctx, types.EventTypeEpochActivated, types.AttributeKeyGroupKey)
	q, _ := evAttr(ctx, types.EventTypeEpochActivated, types.AttributeKeyQualified)
	return g, q
}

func TestDKGWindowsMatch(t *testing.T) { require.Equal(t, keeper.DKGWindow, dkg.Window) }

// A full 4-operator DKG: epoch 1 is ACTIVE at the end of the complaint window (height
// 2 + 3·20 − 1), the group key is even-y, every operator's derived share matches its
// verifying share, and any 3 shares sign for the key and a tweaked key.
func TestDKGHonest(t *testing.T) {
	f := newDKG(t)
	st, err := f.k.DKGStates.Get(f.ctx, 1)
	require.NoError(t, err)
	require.Equal(t, int64(2), st.StartHeight)
	require.Equal(t, uint32(3), st.T)
	var gk, q string
	for f.ctx.BlockHeight() < 2+3*keeper.DKGWindow-1 {
		f.block(t)
		if g, qq := activated(f.ctx); g != "" {
			gk, q = g, qq
		}
	}
	require.NotEmpty(t, gk, "epoch_activated in the last block of the complaint window")
	e := f.runToActive(t, 3)
	require.Equal(t, hex.EncodeToString(e.GroupKey), gk)
	require.Equal(t, byte(0x02), e.GroupKey[0])
	require.Equal(t, uint32(4), e.N)
	require.Len(t, bytes.Split([]byte(q), []byte(",")), 4)
	require.Empty(t, f.errs)
	for _, p := range f.parts {
		es := p.Store.(*dkg.MemStore).Shares[1]
		require.Equal(t, e.GroupKey, es.GroupKey)
	}
	signWith(t, f, e, 0, 1, 2)
	signWith(t, f, e, 1, 2, 3)
	signWith(t, f, e, 0, 1, 2, 3)
	// Deposits are possible now: the active epoch has a treasury address.
	_, err = f.k.TreasuryAddress(f.ctx, e)
	require.NoError(t, err)
}

// One dealer sends a bad share: the recipient's complaint holds, the dealer is
// disqualified, and the key forms from the other 3 dealers.
func TestDKGBadShareComplaint(t *testing.T) {
	f := newDKG(t)
	f.parts[0].CorruptFor = 2
	e := f.runToActive(t, 70)
	st, err := f.k.DKGStates.Get(f.ctx, 1)
	require.NoError(t, err)
	require.Equal(t, []string{f.parts[0].Operator.String()}, st.Disqualified)
	run, _, err := f.k.GetDKG(f.ctx, 1)
	require.NoError(t, err)
	require.Len(t, run.Complaints, 1)
	require.True(t, run.Complaints[0].Upheld)
	require.Equal(t, f.parts[1].Operator.String(), run.Complaints[0].Complainer)
	signWith(t, f, e, 0, 1, 3)
}

// A false complaint disqualifies the complainer.
func TestDKGFalseComplaint(t *testing.T) {
	f := newDKG(t)
	f.parts[2].AccuseFalsely = f.parts[3].Operator.String()
	e := f.runToActive(t, 70)
	st, err := f.k.DKGStates.Get(f.ctx, 1)
	require.NoError(t, err)
	require.Equal(t, []string{f.parts[2].Operator.String()}, st.Disqualified)
	run, _, err := f.k.GetDKG(f.ctx, 1)
	require.NoError(t, err)
	require.Len(t, run.Complaints, 1)
	require.False(t, run.Complaints[0].Upheld)
	signWith(t, f, e, 1, 2, 3)
}

// With two of four operators absent fewer than 2/3 qualify: the run restarts once and
// the second attempt, with everyone back, activates the epoch.
func TestDKGRestart(t *testing.T) {
	f := newDKG(t)
	f.absent[0], f.absent[1] = true, true
	for f.ctx.BlockHeight() < 2+3*keeper.DKGWindow-1 {
		f.block(t)
	}
	_, ok := evAttr(f.ctx, types.EventTypeDKGRestarted, types.AttributeKeyQualified)
	require.True(t, ok)
	st, err := f.k.DKGStates.Get(f.ctx, 1)
	require.NoError(t, err)
	require.Equal(t, uint32(1), st.Attempt)
	require.Equal(t, f.ctx.BlockHeight()+1, st.StartHeight)
	f.absent = map[int]bool{}
	e := f.runToActive(t, 70)
	signWith(t, f, e, 0, 1, 2)

	// A second failure would not restart again; checked by the attempt counter only.
	require.Equal(t, types.DKG_PHASE_DONE, mustDKG(t, f).Phase)
}

func mustDKG(t *testing.T, f *dkgFixture) types.DKGState {
	t.Helper()
	st, err := f.k.DKGStates.Get(f.ctx, 1)
	require.NoError(t, err)
	return st
}

// Messages are checked on submission.
func TestDKGMessageChecks(t *testing.T) {
	f := newDKG(t)
	f.ctx = f.ctx.WithBlockHeight(2)
	srv := keeper.NewMsgServerImpl(f.k)
	v := f.view(t)
	ms, err := f.parts[0].Step(dkg.View{ChainID: v.ChainID, Height: 1, State: v.State, Keys: v.Keys})
	require.NoError(t, err)
	commit := ms[0].(*types.MsgDKGCommit)

	bad := *commit
	bad.Proof = append([]byte{}, commit.Proof...)
	bad.Proof[40] ^= 1
	_, err = srv.DKGCommit(f.ctx, &bad)
	require.ErrorContains(t, err, "proof of knowledge")
	stranger := *commit
	stranger.HotKey = sdk.AccAddress(bytes.Repeat([]byte{0x99}, 20)).String()
	_, err = srv.DKGCommit(f.ctx, &stranger)
	require.ErrorIs(t, err, types.ErrNotFound)
	short := *commit
	short.Commitments = commit.Commitments[:2]
	_, err = srv.DKGCommit(f.ctx, &short)
	require.ErrorIs(t, err, types.ErrDKG)
	_, err = srv.DKGCommit(f.ctx, commit)
	require.NoError(t, err)
	_, err = srv.DKGCommit(f.ctx, commit)
	require.ErrorContains(t, err, "already committed")
	_, err = srv.DKGDeal(f.ctx, &types.MsgDKGDeal{HotKey: commit.HotKey, Epoch: 1})
	require.ErrorContains(t, err, "not DKG_PHASE_DEAL")
}
