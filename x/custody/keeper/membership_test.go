package keeper_test

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"

	"cosmossdk.io/collections"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/logicvoid0x/keystone/app/protocol/vetypes"
	"github.com/logicvoid0x/keystone/crypto/frost"
	"github.com/logicvoid0x/keystone/crypto/frost/secret"
	"github.com/logicvoid0x/keystone/sidecar/dkg"
	"github.com/logicvoid0x/keystone/x/custody/keeper"
	"github.com/logicvoid0x/keystone/x/custody/types"
	"github.com/logicvoid0x/keystone/x/validators"
)

// deliver runs DKG and custody-key messages through the msg server; failures are
// test errors.
func deliver(t *testing.T, ctx sdk.Context, k *keeper.Keeper, msgs []sdk.Msg) {
	t.Helper()
	srv := keeper.NewMsgServerImpl(k)
	for _, m := range msgs {
		var err error
		switch m := m.(type) {
		case *types.MsgDKGCommit:
			_, err = srv.DKGCommit(ctx, m)
		case *types.MsgDKGDeal:
			_, err = srv.DKGDeal(ctx, m)
		case *types.MsgDKGComplaint:
			_, err = srv.DKGComplaint(ctx, m)
		case *types.MsgRegisterCustodyKey:
			_, err = srv.RegisterCustodyKey(ctx, m)
		}
		require.NoError(t, err, "%T at height %d", m, ctx.BlockHeight())
	}
}

// dkgView is the participant's view of epoch's run from the fixture's state.
func dkgView(t *testing.T, ctx sdk.Context, k *keeper.Keeper, epoch uint64, op sdk.AccAddress) dkg.View {
	t.Helper()
	v := dkg.View{ChainID: ctx.ChainID(), Height: ctx.BlockHeight()}
	require.NoError(t, k.CustodyKeys.Walk(ctx, nil, func(_ sdk.AccAddress, ck types.CustodyKey) (bool, error) {
		v.Keys = append(v.Keys, ck)
		return false, nil
	}))
	run, ok, err := k.GetDKG(ctx, epoch)
	require.NoError(t, err)
	if !ok {
		return v
	}
	v.State, v.Dealers, v.Complaints = &run.State, run.Dealers, run.Complaints
	if e, err := k.Epochs.Get(ctx, epoch); err == nil {
		v.Epoch = &e
		if idx, err := k.ShareIndex.Get(ctx, collections.Join(epoch, op)); err == nil {
			v.VerifyingShare, _ = k.VerifyingShares.Get(ctx, collections.Join(epoch, idx))
		}
	}
	return v
}

// Design §9.4 / spec M5-T2: with funds in custody the admin removes an operator; the
// change is pending for MembershipDelayBlocks; then the epoch-2 DKG runs among the
// remaining 3, epoch 1 retires, its UTXOs are swept (signed by epoch-1 operators) to
// epoch 2's treasury, and after RetireWatchBlocks epoch 1 is RETIRED.
func TestMembershipChangeSweepsAndRetires(t *testing.T) {
	f := newSigning(t)
	p, err := f.k.Params.Get(f.ctx)
	require.NoError(t, err)
	p.RetireWatchBlocks = 5
	require.NoError(t, f.k.Params.Set(f.ctx, p))
	f.fund(t, 0x11, 30_000_000, 50_000_000, 5_000) // the 5,000 sat deposit funds the fee reserve
	reserve, _ := f.k.GetFeeReserve(f.ctx)
	require.Equal(t, i(5_000), reserve)

	// Every operator registers custody keys (as operators added after genesis would).
	var parts []*dkg.Participant
	var reg []sdk.Msg
	for n, s := range f.signers {
		enc, err := secret.RandomScalar(nil)
		require.NoError(t, err)
		pub, _ := frost.SerializePoint(frost.MulBase(&enc))
		hot := sdk.AccAddress(append([]byte{0x70 + byte(n)}, make([]byte, 19)...))
		reg = append(reg, &types.MsgRegisterCustodyKey{Operator: s.op.String(), EncPubkey: pub[:], HotKey: hot.String()})
		parts = append(parts, &dkg.Participant{Operator: s.op, HotKey: hot.String(), EncKey: enc, Store: &dkg.MemStore{}})
	}
	deliver(t, f.ctx, f.k, reg)
	f.block(t)
	_, ok := evAttr(f.ctx, types.EventTypeMembershipPending, types.AttributeKeyAdded)
	require.False(t, ok, "the same set: nothing pending")

	// The admin removes the last operator (power 0); it stops voting.
	gone := f.signers[3]
	for j := range f.fv.ops {
		if f.fv.ops[j].OperatorAddress == gone.op.String() {
			f.fv.ops[j].Power = 0
		}
	}
	gone.absent = true
	f.block(t)
	removed, ok := evAttr(f.ctx, types.EventTypeMembershipPending, types.AttributeKeyRemoved)
	require.True(t, ok)
	require.Equal(t, gone.op.String(), removed)
	pend, err := keeper.NewQuerier(f.k).PendingMembership(f.ctx, &types.QueryPendingMembershipRequest{})
	require.NoError(t, err)
	require.Equal(t, f.ctx.BlockHeight()+int64(p.MembershipDelayBlocks), pend.EffectiveHeight)

	// The remaining participants run their part of the DKG each block.
	f.hook = func() []sdk.Msg {
		var msgs []sdk.Msg
		for n, pp := range parts[:3] {
			ms, err := pp.Step(dkgView(t, f.ctx, f.k, 2, f.signers[n].op))
			require.NoError(t, err)
			msgs = append(msgs, ms...)
		}
		return msgs
	}
	var swept *types.WithdrawalBatch
	for range int(p.MembershipDelayBlocks) + 3*keeper.DKGWindow + 30 {
		f.block(t)
		_ = f.k.Batches.Walk(f.ctx, nil, func(_ uint64, b types.WithdrawalBatch) (bool, error) {
			if b.Kind == types.BATCH_KIND_SWEEP && b.Status == types.BATCH_STATUS_SIGNED {
				swept = &b
				return true, nil
			}
			return false, nil
		})
		if swept != nil {
			break
		}
	}
	require.NotNil(t, swept, "the epoch-1 UTXOs are swept")
	e2, ok, err := f.k.GetActiveEpoch(f.ctx)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, uint64(2), e2.Number)
	require.Equal(t, uint32(3), e2.N)
	e1, err := f.k.Epochs.Get(f.ctx, 1)
	require.NoError(t, err)
	require.Equal(t, types.EPOCH_STATUS_RETIRING, e1.Status)
	require.Equal(t, uint64(2), swept.ChangeEpoch)
	require.Len(t, swept.Inputs, 3)
	verifyTx(t, f, *swept)

	// The guard now protects epoch 2 as well: removing another operator would leave
	// 2 < t = 3 of epoch 1 (still funded) active.
	err = f.k.RemovalGuard().AllowRemoval(f.ctx, []sdk.AccAddress{sdk.MustAccAddressFromBech32(f.fv.ops[2].OperatorAddress)})
	require.ErrorIs(t, err, types.ErrRemovalGuard)

	// Confirmation: the UTXOs move to epoch 2; after RetireWatchBlocks epoch 1 retires.
	cur := swept.Txs[len(swept.Txs)-1]
	f.bitcoin = &vetypes.BitcoinPayload{FeerateSatVb: 2, Confirms: []vetypes.TxConfirmed{{Txid: cur.Txid, Block: &vetypes.BtcBlockRef{Height: 300, Hash: h32(0xE1)}}}}
	f.block(t)
	f.bitcoin = &vetypes.BitcoinPayload{FeerateSatVb: 2}
	u, err := f.k.Utxos.Get(f.ctx, collectionsJoin(cur.Txid, 0))
	require.NoError(t, err)
	require.Equal(t, uint64(2), u.Epoch)
	require.Equal(t, uint64(80_005_000)-cur.FeeSat, u.AmountSat)
	reserve, _ = f.k.GetFeeReserve(f.ctx)
	require.Equal(t, i(5_000-int64(cur.FeeSat)), reserve, "the sweep fee is charged to the fee reserve")
	retired := false
	for range 10 {
		f.block(t)
		if e1, _ = f.k.Epochs.Get(f.ctx, 1); e1.Status == types.EPOCH_STATUS_RETIRED {
			retired = true
			break
		}
	}
	require.True(t, retired)
}

// Design §9.8 step 8 / spec M5-T4: with more than 50 AVAILABLE UTXOs and a feerate of at
// most 5 sat/vB, the 8 smallest are consolidated into one treasury output, the fee
// charged to the fee reserve.
func TestConsolidation(t *testing.T) {
	f := newSigning(t)
	sats := []uint64{5_000} // fee reserve
	for n := 0; n < 51; n++ {
		sats = append(sats, uint64(100_000+1_000*n))
	}
	f.fund(t, 0x11, sats...)
	var b *types.WithdrawalBatch
	for range 15 {
		f.block(t)
		if got, err := f.k.GetBatch(f.ctx, 1); err == nil && got.Status == types.BATCH_STATUS_SIGNED {
			b = &got
			break
		}
	}
	require.NotNil(t, b)
	require.Equal(t, types.BATCH_KIND_CONSOLIDATE, b.Kind)
	require.Len(t, b.Inputs, 8)
	var sum uint64
	for _, a := range b.InputAmounts {
		require.Less(t, a, uint64(108_000), "the smallest UTXOs")
		sum += a
	}
	require.Equal(t, sum-b.Txs[0].FeeSat, b.Txs[0].ChangeSat)
	verifyTx(t, f, *b)

	// At a high feerate nothing consolidates.
	g := newSigning(t)
	g.fund(t, 0x11, sats...)
	g.bitcoin = &vetypes.BitcoinPayload{FeerateSatVb: 6}
	for range 5 {
		g.block(t)
	}
	_, err := g.k.GetBatch(g.ctx, 1)
	require.Error(t, err)
}

// EmergencyRekey (design §9.4): the admin removes the offline operators (the guard
// allows it while t remain) and sets the param; the next epoch's DKG then starts at
// once instead of after MembershipDelayBlocks.
func TestEmergencyRekey(t *testing.T) {
	f := newSigning(t)
	var reg []sdk.Msg
	for n, s := range f.signers {
		enc, _ := secret.RandomScalar(nil)
		pub, _ := frost.SerializePoint(frost.MulBase(&enc))
		reg = append(reg, &types.MsgRegisterCustodyKey{Operator: s.op.String(), EncPubkey: pub[:],
			HotKey: sdk.AccAddress(append([]byte{0x60 + byte(n)}, make([]byte, 19)...)).String()})
	}
	deliver(t, f.ctx, f.k, reg)
	p, err := f.k.Params.Get(f.ctx)
	require.NoError(t, err)
	p.EmergencyRekey = true
	require.NoError(t, f.k.Params.Set(f.ctx, p))
	f.fv.ops[3].Power = 0
	f.signers[3].absent = true
	f.block(t)
	st, err := f.k.DKGStates.Get(f.ctx, 2)
	require.NoError(t, err, "the epoch-2 DKG started without the delay")
	require.Equal(t, f.ctx.BlockHeight()+1, st.StartHeight)
	require.Len(t, st.Participants, 3)
}

// Design §9.2 / spec M7-T4: with unequal power the custody set is the top N; a power
// change inside CustodyEntryMarginBps does not make a membership change pending, one
// past it does.
func TestTopNMembershipHysteresis(t *testing.T) {
	f := newSigning(t)
	p, err := f.k.Params.Get(f.ctx)
	require.NoError(t, err)
	p.CustodySetSize = 4
	p.CustodyEntryMarginBps = 1000
	require.NoError(t, f.k.Params.Set(f.ctx, p))

	// A fifth validator with a custody key; every operator has custody keys.
	fifth := sdk.AccAddress(bytes.Repeat([]byte{5}, 20))
	f.fv.ops = append(f.fv.ops, validators.Validator{OperatorAddress: fifth.String(), ConsensusPubkey: bytes.Repeat([]byte{5}, 32), Power: 1})
	for _, op := range f.fv.ops {
		a := sdk.MustAccAddressFromBech32(op.OperatorAddress)
		require.NoError(t, f.k.CustodyKeys.Set(f.ctx, a, types.CustodyKey{Operator: a.String(), EncPubkey: bytes.Repeat([]byte{2}, 33), HotKey: a.String()}))
	}
	for i := range f.fv.ops[:4] {
		f.fv.ops[i].Power = 100
	}
	pending := func() bool {
		has, err := f.k.Pending.Has(f.ctx)
		require.NoError(t, err)
		return has
	}

	f.fv.ops[4].Power = 110 // +10%: not above the margin
	f.block(t)
	require.False(t, pending(), "within the margin")

	f.fv.ops[4].Power = 111 // +11%: displaces the weakest incumbent (ties: highest address)
	f.block(t)
	require.True(t, pending())
	pm, err := f.k.Pending.Get(f.ctx)
	require.NoError(t, err)
	require.Equal(t, []string{fifth.String()}, pm.Added)
	require.Equal(t, []string{f.fv.ops[3].OperatorAddress}, pm.Removed)

	f.fv.ops[4].Power = 105 // back under the margin: the pending change is dropped
	f.block(t)
	require.False(t, pending())
}
