package keeper_test

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/logicvoid0x/keystone/app/protocol/vetypes"
	"github.com/logicvoid0x/keystone/crypto/frost"
	"github.com/logicvoid0x/keystone/crypto/frost/secret"
	"github.com/logicvoid0x/keystone/testutil/devdealer"
	"github.com/logicvoid0x/keystone/x/custody/types"
)

func commitment(t *testing.T, s *simSigner, seq uint64) vetypes.NonceCommitment {
	t.Helper()
	n, err := secret.NewNonces(frost.Taproot, nil, s.share)
	require.NoError(t, err)
	d, e, err := n.Commitment()
	require.NoError(t, err)
	return vetypes.NonceCommitment{Epoch: devdealer.Epoch, ShareIndex: s.index, Seq: seq, Hiding: d[:], Binding: e[:]}
}

// stepFrost runs one block where only signer s votes, with the given FROST payload.
func (f *signFixture) stepFrost(t *testing.T, s *simSigner, p *vetypes.FrostPayload) {
	t.Helper()
	f.ctx = f.ctx.WithBlockHeight(f.ctx.BlockHeight() + 1).WithEventManager(sdk.NewEventManager())
	b, err := f.k.BuildOps(f.ctx, []types.Vote{{ConsAddr: s.cons, Frost: p}})
	require.NoError(t, err)
	require.NoError(t, f.k.ApplyOps(f.ctx, b))
}

func seqs(t *testing.T, f *signFixture, share uint32) []uint64 {
	t.Helper()
	_, pool, err := f.k.NoncePool(f.ctx, devdealer.Epoch, share)
	require.NoError(t, err)
	var out []uint64
	for _, c := range pool {
		out = append(out, c.Seq)
	}
	return out
}

func TestRecordCommitments(t *testing.T) {
	f := newSigning(t)
	s, other := f.signers[0], f.signers[1]

	// At most 2 new per share per block; seq must rise.
	f.stepFrost(t, s, &vetypes.FrostPayload{Commitments: []vetypes.NonceCommitment{commitment(t, s, 1), commitment(t, s, 2), commitment(t, s, 3)}})
	require.Equal(t, []uint64{1, 2}, seqs(t, f, s.index))
	f.stepFrost(t, s, &vetypes.FrostPayload{Commitments: []vetypes.NonceCommitment{commitment(t, s, 2), commitment(t, s, 5)}})
	require.Equal(t, []uint64{1, 2, 5}, seqs(t, f, s.index), "seq 2 is not above the last recorded; 5 is")

	// Only the operator's own share of an existing epoch counts; points must decode.
	c := commitment(t, other, 6)
	bad := commitment(t, s, 6)
	bad.Hiding = append([]byte{0x05}, bad.Hiding[1:]...) // not a compressed point
	wrongEpoch := commitment(t, s, 7)
	wrongEpoch.Epoch = 9
	f.stepFrost(t, s, &vetypes.FrostPayload{Commitments: []vetypes.NonceCommitment{c, bad, wrongEpoch}})
	require.Equal(t, []uint64{1, 2, 5}, seqs(t, f, s.index))
	require.Empty(t, seqs(t, f, other.index))

	// The pool stops at NoncePool (8).
	for seq := uint64(10); seq < 20; seq += 2 {
		f.stepFrost(t, s, &vetypes.FrostPayload{Commitments: []vetypes.NonceCommitment{commitment(t, s, seq), commitment(t, s, seq+1)}})
	}
	require.Len(t, seqs(t, f, s.index), 8)

	// revoke_all_nonces discards the pool before recording the same payload's
	// commitments; the last recorded seq stays.
	f.stepFrost(t, s, &vetypes.FrostPayload{RevokeAllNonces: true, Commitments: []vetypes.NonceCommitment{commitment(t, s, 30)}})
	require.Equal(t, []uint64{30}, seqs(t, f, s.index))
	f.stepFrost(t, s, &vetypes.FrostPayload{Commitments: []vetypes.NonceCommitment{commitment(t, s, 29)}})
	require.Equal(t, []uint64{30}, seqs(t, f, s.index))
	_, ok := evAttr(f.ctx, types.EventTypeNoncesRevoked, types.AttributeKeyOperator)
	require.False(t, ok)
}

// RBF: after RbfAfterBtcBlocks of anchor progress the signed batch is rebuilt at
// 1.5× the feerate, re-signed, and the extra fee is charged to the fee reserve; the
// older version can still confirm, refunding the reserve.
func TestRBF(t *testing.T) {
	f := newSigning(t)
	f.fund(t, 0x11, 50_000_000)
	carol := sdk.AccAddress(bytes.Repeat([]byte{0xCC}, 20))
	// Several requests so the fee shares leave a reserve for the bump.
	for n := byte(0); n < 6; n++ {
		f.queueClaim(t, carol, wpkh(0x30+n), 1_000_000)
	}
	f.bitcoin = &vetypes.BitcoinPayload{FeerateSatVb: 4, Anchor: &vetypes.BtcBlockRef{Height: 200, Hash: h32(0xA0)}}
	b := f.runUntil(t, 1, types.BATCH_STATUS_SIGNED, 12)
	require.Equal(t, uint32(200), b.AnchorHeightAtSign)
	v0 := b.Txs[0]
	reserve0, err := f.k.GetFeeReserve(f.ctx)
	require.NoError(t, err)

	f.bitcoin = &vetypes.BitcoinPayload{FeerateSatVb: 4, Anchor: &vetypes.BtcBlockRef{Height: 206, Hash: h32(0xA6)}}
	for range 12 {
		f.block(t)
		if b, err = f.k.GetBatch(f.ctx, 1); err == nil && len(b.Txs) == 2 && b.Status == types.BATCH_STATUS_SIGNED {
			break
		}
	}
	require.Len(t, b.Txs, 2)
	require.Equal(t, uint32(1), b.RbfCount)
	v1 := b.Txs[1]
	require.Equal(t, uint64(6), v1.FeerateSatVb)
	extra := v1.FeeSat - v0.FeeSat
	require.Equal(t, v0.ChangeSat-extra, v1.ChangeSat)
	reserve1, _ := f.k.GetFeeReserve(f.ctx)
	require.Equal(t, reserve0.SubRaw(int64(extra)), reserve1)
	verifyTx(t, f, b)

	// The original version confirms: the reserve gets the bump back.
	f.bitcoin = &vetypes.BitcoinPayload{FeerateSatVb: 4, Anchor: &vetypes.BtcBlockRef{Height: 206, Hash: h32(0xA6)},
		Confirms: []vetypes.TxConfirmed{{Txid: v0.Txid, Block: &vetypes.BtcBlockRef{Height: 207, Hash: h32(0xA7)}}}}
	f.block(t)
	b, err = f.k.GetBatch(f.ctx, 1)
	require.NoError(t, err)
	require.Equal(t, types.BATCH_STATUS_CONFIRMED, b.Status)
	require.Equal(t, v0.Txid, b.ConfirmedTxid)
	reserve2, _ := f.k.GetFeeReserve(f.ctx)
	require.Equal(t, reserve0, reserve2)
	change, err := f.k.Utxos.Get(f.ctx, collectionsJoin(v0.Txid, 6))
	require.NoError(t, err)
	require.Equal(t, v0.ChangeSat, change.AmountSat)
}
